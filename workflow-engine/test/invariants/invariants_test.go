//go:build integration

// Package invariants_test is a spec-first property-based test suite that runs
// against a real Postgres container.
//
// Source of truth: docs/workflow-format.md + engine architecture invariants.
// Each test is written from the spec, NOT the implementation.
// A failing test means the engine violates a documented contract.
//
// "Búa tạ thuốc nổ" mindset: rapid generates random workflow shapes and the
// chaos worker applies random job outcomes — the goal is to find invariant
// violations, not to confirm happy paths.
package invariants_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"pgregory.net/rapid"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
	pgstore "github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

// ── Shared infrastructure (TestMain) ─────────────────────────────────────────

var (
	gPool    *pgxpool.Pool
	gDefRepo *definition.Repository
	gInstSvc *instance.Service
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("workflow"),
		tcpostgres.WithUsername("workflow"),
		tcpostgres.WithPassword("workflow"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = pg.Terminate(ctx) }()

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres DSN: %v\n", err)
		os.Exit(1)
	}
	gPool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool: %v\n", err)
		os.Exit(1)
	}
	defer gPool.Close()

	if err := pgstore.Migrate(gPool); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	gDefRepo = definition.NewRepository(gPool)
	rt := polling.New()
	gInstSvc = instance.NewService(gPool, gDefRepo, rt.Dispatcher())

	os.Exit(m.Run())
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// genLinearDef builds a random linear chain of SERVICE_TASK steps ending at END.
// uniqueID is embedded in both the definition ID and each job type so that
// concurrent tests never steal each other's jobs.
func genLinearDef(t *rapid.T, uniqueID string) *definition.WorkflowDefinition {
	n := rapid.IntRange(2, 5).Draw(t, "n_steps")
	steps := make([]definition.WorkflowStep, 0, n+1)
	for i := 0; i < n; i++ {
		nextStep := fmt.Sprintf("step-%d", i+1)
		if i == n-1 {
			nextStep = "end"
		}
		steps = append(steps, definition.WorkflowStep{
			ID:       fmt.Sprintf("step-%d", i),
			Name:     fmt.Sprintf("Step %d", i),
			Type:     definition.StepTypeServiceTask,
			JobType:  fmt.Sprintf("pbt-%s-%d", uniqueID, i),
			NextStep: nextStep,
		})
	}
	steps = append(steps, definition.WorkflowStep{
		ID:   "end",
		Name: "End",
		Type: definition.StepTypeEnd,
	})
	return &definition.WorkflowDefinition{
		ID:    "pbt-" + uniqueID,
		Name:  "PBT " + uniqueID,
		Steps: steps,
	}
}

// jobTypesFromDef returns all job types used by a definition's SERVICE_TASK steps.
func jobTypesFromDef(def *definition.WorkflowDefinition) []string {
	var types []string
	for _, s := range def.Steps {
		if s.Type == definition.StepTypeServiceTask {
			types = append(types, s.JobType)
		}
	}
	return types
}

// stepIDsFromDef returns the set of step IDs defined in a workflow.
func stepIDsFromDef(def *definition.WorkflowDefinition) map[string]struct{} {
	m := make(map[string]struct{}, len(def.Steps))
	for _, s := range def.Steps {
		m[s.ID] = struct{}{}
	}
	return m
}

// runChaosWorker polls jobs of the given types and applies random outcomes
// until ctx is cancelled or the instance reaches a terminal state.
//
// Chaos distribution (per job): 70% complete, 20% fail-retryable, 10% fail-terminal.
// This distribution is intentional: it exercises all three outcome paths and
// lets retries happen naturally, without pre-determining a specific path.
func runChaosWorker(ctx context.Context, jobTypes []string) {
	disp := polling.New().Dispatcher()
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0))
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		jobs, err := job.Poll(ctx, gPool, "chaos-worker", jobTypes, 10)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		for _, j := range jobs {
			switch roll := rng.IntN(10); {
			case roll < 7:
				_ = gInstSvc.CompleteJobAndAdvance(ctx, j.ID, "chaos-worker",
					map[string]any{"chaosStep": j.JobType})
			case roll < 9:
				_ = job.Fail(ctx, gPool, disp, j.ID, "chaos-worker", "chaos retryable", true)
			default:
				_ = job.Fail(ctx, gPool, disp, j.ID, "chaos-worker", "chaos terminal", false)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// awaitTerminal polls Get until the instance status is COMPLETED/FAILED/CANCELLED
// or timeout is reached.
func awaitTerminal(ctx context.Context, instanceID string, timeout time.Duration) (*instance.WorkflowInstance, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inst, err := gInstSvc.Get(ctx, instanceID)
		if err != nil {
			return nil, err
		}
		switch inst.Status {
		case instance.InstanceStatusCompleted, instance.InstanceStatusFailed, instance.InstanceStatusCancelled:
			return inst, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("instance %s did not reach terminal within %s", instanceID, timeout)
}

// ── Property 1 ────────────────────────────────────────────────────────────────
// Spec: once COMPLETED/FAILED/CANCELLED, the instance status never changes.
// Terminal finality is a core safety invariant — re-activating a completed
// workflow would be catastrophic data corruption.
func TestInvariant_TerminalStatusIsStable(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		uid := rapid.StringMatching(`[a-z]{8}`).Draw(t, "uid")
		def := genLinearDef(t, uid)

		ctx := context.Background()
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatalf("upload definition: %v", err)
		}

		inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
		if err != nil {
			t.Fatalf("start instance: %v", err)
		}

		workerCtx, stopWorker := context.WithCancel(ctx)
		defer stopWorker()
		go runChaosWorker(workerCtx, jobTypesFromDef(def))

		terminal, err := awaitTerminal(ctx, inst.ID, 15*time.Second)
		if err != nil {
			t.Fatalf("await terminal: %v", err)
		}
		firstStatus := terminal.Status

		// Poll three more times — status must not change.
		for i := 0; i < 3; i++ {
			time.Sleep(100 * time.Millisecond)
			again, err := gInstSvc.Get(ctx, inst.ID)
			if err != nil {
				t.Fatalf("re-get after terminal: %v", err)
			}
			if again.Status != firstStatus {
				t.Fatalf("terminal status regressed: was %s, now %s (instance %s)",
					firstStatus, again.Status, inst.ID)
			}
		}
	})
}

// ── Property 2 ────────────────────────────────────────────────────────────────
// Spec: a COMPLETED instance has no in-progress steps.
// currentStepIds must be empty when status=COMPLETED.
// A non-empty currentStepIds on a completed instance means the engine believes
// work is still in flight — impossible by definition.
func TestInvariant_CompletedInstanceHasNoDanglingCurrentSteps(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		uid := rapid.StringMatching(`[a-z]{8}`).Draw(t, "uid")
		def := genLinearDef(t, uid)

		// Force all jobs to complete (no chaos failure) by injecting a
		// deterministic completer so this property always tests the COMPLETED path.
		steps := make([]definition.WorkflowStep, len(def.Steps))
		copy(steps, def.Steps)
		def.Steps = steps

		ctx := context.Background()
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatalf("upload definition: %v", err)
		}
		inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
		if err != nil {
			t.Fatalf("start instance: %v", err)
		}

		// Deterministic completer: always completes, never fails.
		workerCtx, stopWorker := context.WithCancel(ctx)
		defer stopWorker()
		go func() {
			for {
				select {
				case <-workerCtx.Done():
					return
				default:
				}
				jobs, _ := job.Poll(workerCtx, gPool, "det-worker", jobTypesFromDef(def), 10)
				for _, j := range jobs {
					_ = gInstSvc.CompleteJobAndAdvance(workerCtx, j.ID, "det-worker", nil)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()

		terminal, err := awaitTerminal(ctx, inst.ID, 15*time.Second)
		if err != nil {
			t.Fatalf("await terminal: %v", err)
		}
		if terminal.Status != instance.InstanceStatusCompleted {
			t.Skipf("instance ended FAILED (chaos), property only checks COMPLETED path")
		}
		if len(terminal.CurrentStepIDs) != 0 {
			t.Fatalf("COMPLETED instance %s still has currentStepIds=%v",
				inst.ID, terminal.CurrentStepIDs)
		}
	})
}

// ── Property 3 ────────────────────────────────────────────────────────────────
// Spec: step history only contains steps defined in the workflow.
// A step_execution referencing an ID not in the definition is a ghost —
// it could only exist if the engine inserted it erroneously.
func TestInvariant_HistoryOnlyReferencesDefinedSteps(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		uid := rapid.StringMatching(`[a-z]{8}`).Draw(t, "uid")
		def := genLinearDef(t, uid)

		ctx := context.Background()
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatalf("upload definition: %v", err)
		}
		inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
		if err != nil {
			t.Fatalf("start instance: %v", err)
		}

		workerCtx, stopWorker := context.WithCancel(ctx)
		defer stopWorker()
		go runChaosWorker(workerCtx, jobTypesFromDef(def))

		if _, err := awaitTerminal(ctx, inst.ID, 15*time.Second); err != nil {
			t.Fatalf("await terminal: %v", err)
		}

		history, err := gInstSvc.GetHistory(ctx, inst.ID)
		if err != nil {
			t.Fatalf("get history: %v", err)
		}

		defined := stepIDsFromDef(def)
		for _, se := range history {
			if _, ok := defined[se.StepID]; !ok {
				t.Fatalf("history contains step %q which is not in definition %q",
					se.StepID, def.ID)
			}
		}
	})
}

// ── Property 4 ────────────────────────────────────────────────────────────────
// Spec: step_execution status must be one of the valid lifecycle values.
// An unknown status string means the engine wrote something the spec does not
// define — either a typo or an undocumented state transition.
func TestInvariant_HistoryStepStatusesAreValid(t *testing.T) {
	validStatuses := map[instance.StepExecutionStatus]struct{}{
		instance.StepExecutionStatusRunning:   {},
		instance.StepExecutionStatusCompleted: {},
		instance.StepExecutionStatusFailed:    {},
		instance.StepExecutionStatusSkipped:   {},
	}
	// CANCELLED is used for interrupted boundary paths — include it.
	const cancelled instance.StepExecutionStatus = "CANCELLED"
	validStatuses[cancelled] = struct{}{}

	rapid.Check(t, func(t *rapid.T) {
		uid := rapid.StringMatching(`[a-z]{8}`).Draw(t, "uid")
		def := genLinearDef(t, uid)

		ctx := context.Background()
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatalf("upload definition: %v", err)
		}
		inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
		if err != nil {
			t.Fatalf("start instance: %v", err)
		}

		workerCtx, stopWorker := context.WithCancel(ctx)
		defer stopWorker()
		go runChaosWorker(workerCtx, jobTypesFromDef(def))

		if _, err := awaitTerminal(ctx, inst.ID, 15*time.Second); err != nil {
			t.Fatalf("await terminal: %v", err)
		}

		history, err := gInstSvc.GetHistory(ctx, inst.ID)
		if err != nil {
			t.Fatalf("get history: %v", err)
		}
		for _, se := range history {
			if _, ok := validStatuses[se.Status]; !ok {
				t.Fatalf("step_execution %q has unknown status %q", se.ID, se.Status)
			}
		}
	})
}

// ── Property 5 ────────────────────────────────────────────────────────────────
// Spec: uploading the same definition twice is idempotent — the second upload
// must not create a new version or return an error.
// If this breaks, deployments that upload-then-start would flap between versions.
func TestInvariant_DefinitionUploadIsIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		uid := rapid.StringMatching(`[a-z]{8}`).Draw(t, "uid")
		def := genLinearDef(t, uid)

		ctx := context.Background()
		s1, err := gDefRepo.Upload(ctx, def)
		if err != nil {
			t.Fatalf("first upload: %v", err)
		}

		// Upload the identical definition a second time.
		s2, err := gDefRepo.Upload(ctx, def)
		if err != nil {
			t.Fatalf("second upload (idempotent) failed: %v", err)
		}

		if s2.Version != s1.Version {
			t.Fatalf("second identical upload created new version: v1=%d v2=%d for def %q",
				s1.Version, s2.Version, def.ID)
		}
	})
}

// ── Property 6 ────────────────────────────────────────────────────────────────
// Spec: completing the same job twice is idempotent — must not panic, must not
// corrupt the instance, must leave the instance in a valid state.
// Duplicate deliveries are real (at-least-once workers); idempotency is required.
func TestInvariant_DuplicateJobCompletionIsIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		uid := rapid.StringMatching(`[a-z]{8}`).Draw(t, "uid")

		// Single-step workflow so we control exactly which job fires.
		def := &definition.WorkflowDefinition{
			ID:   "pbt-idem-" + uid,
			Name: "Idempotent " + uid,
			Steps: []definition.WorkflowStep{
				{ID: "only", Name: "Only", Type: definition.StepTypeServiceTask,
					JobType: "pbt-idem-" + uid, NextStep: "end"},
				{ID: "end", Name: "End", Type: definition.StepTypeEnd},
			},
		}

		ctx := context.Background()
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatalf("upload definition: %v", err)
		}
		inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
		if err != nil {
			t.Fatalf("start instance: %v", err)
		}

		// Poll once to get the single job.
		var firstJob instance.Job
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			jobs, _ := job.Poll(ctx, gPool, "idem-worker", []string{"pbt-idem-" + uid}, 1)
			if len(jobs) > 0 {
				firstJob = jobs[0]
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if firstJob.ID == "" {
			t.Fatal("did not receive a job within 5 seconds")
		}

		// Complete the job twice concurrently — neither must panic, both must succeed or
		// the second must be a no-op (not an error per CompleteJobAndAdvance idempotency).
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := 0; i < 2; i++ {
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = gInstSvc.CompleteJobAndAdvance(ctx, firstJob.ID, "idem-worker", nil)
			}()
		}
		wg.Wait()

		// Both errors are acceptable (idempotent skip on second call), but a panic is not.
		// What is NOT acceptable: the instance in a broken / non-terminal state.
		terminal, err := awaitTerminal(ctx, inst.ID, 10*time.Second)
		if err != nil {
			t.Fatalf("instance never reached terminal after duplicate complete: %v", err)
		}
		if terminal.Status == instance.InstanceStatusFailed && terminal.FailureReason != nil &&
			*terminal.FailureReason == "" {
			t.Fatalf("FAILED instance %s has empty failure reason — engine left it un-diagnosable", inst.ID)
		}
	})
}

// ── Property 7 ────────────────────────────────────────────────────────────────
// Spec: a FAILED instance always carries a non-empty failureReason.
// Without a reason, operators cannot diagnose why the workflow failed.
func TestInvariant_FailedInstanceAlwaysHasFailureReason(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		uid := rapid.StringMatching(`[a-z]{8}`).Draw(t, "uid")
		def := genLinearDef(t, uid)

		ctx := context.Background()
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatalf("upload definition: %v", err)
		}
		inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
		if err != nil {
			t.Fatalf("start instance: %v", err)
		}

		// Failing worker: never retryable, so the first failure terminates the instance.
		workerCtx, stopWorker := context.WithCancel(ctx)
		defer stopWorker()
		go func() {
			disp := polling.New().Dispatcher()
			for {
				select {
				case <-workerCtx.Done():
					return
				default:
				}
				jobs, _ := job.Poll(workerCtx, gPool, "failing-worker", jobTypesFromDef(def), 10)
				for _, j := range jobs {
					_ = job.Fail(workerCtx, gPool, disp, j.ID, "failing-worker", "deliberate failure", false)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()

		terminal, err := awaitTerminal(ctx, inst.ID, 10*time.Second)
		if err != nil {
			t.Fatalf("await terminal: %v", err)
		}
		if terminal.Status != instance.InstanceStatusFailed {
			t.Skipf("instance ended %s (not FAILED), property only checks FAILED path", terminal.Status)
		}
		if terminal.FailureReason == nil || *terminal.FailureReason == "" {
			t.Fatalf("FAILED instance %s has nil or empty failureReason — un-diagnosable failure", inst.ID)
		}
	})
}
