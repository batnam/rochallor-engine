//go:build integration

package invariants_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
)

// TestInvariant_JoinGatewayDoesNotFireEarly verifies that a JOIN_GATEWAY only
// advances past the join when ALL parallel branches have completed, not before.
//
// Regression guard for the branch-counting logic in handleJoinGateway:
// count must reflect exactly the number of arrived branches without over- or
// under-counting, regardless of the transaction isolation used in the query.
func TestInvariant_JoinGatewayDoesNotFireEarly(t *testing.T) {
	ctx := context.Background()

	def := &definition.WorkflowDefinition{
		ID:   "inv-join-not-early",
		Name: "Join Not Early",
		Steps: []definition.WorkflowStep{
			{
				ID:                "split",
				Name:              "Split",
				Type:              definition.StepTypeParallelGateway,
				ParallelNextSteps: []string{"branch-left", "branch-right"},
				JoinStep:          "join",
			},
			{
				ID:       "branch-left",
				Name:     "Branch Left",
				Type:     definition.StepTypeServiceTask,
				JobType:  "inv-join-left",
				NextStep: "join",
			},
			{
				ID:       "branch-right",
				Name:     "Branch Right",
				Type:     definition.StepTypeServiceTask,
				JobType:  "inv-join-right",
				NextStep: "join",
			},
			{
				ID:       "join",
				Name:     "Join",
				Type:     definition.StepTypeJoinGateway,
				NextStep: "after-join",
			},
			{
				ID:       "after-join",
				Name:     "After Join",
				Type:     definition.StepTypeServiceTask,
				JobType:  "inv-join-after",
				NextStep: "end",
			},
			{
				ID:   "end",
				Name: "End",
				Type: definition.StepTypeEnd,
			},
		},
	}

	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload def: %v", err)
	}

	inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
	if err != nil {
		t.Fatalf("start instance: %v", err)
	}

	pollOne := func(jobType string) instance.Job {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			jobs, err := job.Poll(ctx, gJobStore, "test-worker", []string{jobType}, 1)
			if err != nil {
				t.Fatalf("poll %s: %v", jobType, err)
			}
			if len(jobs) > 0 {
				return jobs[0]
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("no job of type %q appeared within timeout", jobType)
		return instance.Job{}
	}

	complete := func(j instance.Job) {
		t.Helper()
		if err := gInstSvc.CompleteJobAndAdvance(ctx, j.ID, "test-worker", nil); err != nil {
			t.Fatalf("complete job %s: %v", j.ID, err)
		}
	}

	getStatus := func() instance.InstanceStatus {
		t.Helper()
		cur, err := gInstSvc.Get(ctx, inst.ID)
		if err != nil {
			t.Fatalf("get instance: %v", err)
		}
		return cur.Status
	}

	// Complete branch-left only.
	complete(pollOne("inv-join-left"))

	// After first branch: join must NOT have fired. The after-join job must not
	// exist yet; the instance must still be active (or waiting on the right branch).
	noJobs, _ := job.Poll(ctx, gJobStore, "test-worker", []string{"inv-join-after"}, 1)
	if len(noJobs) > 0 {
		t.Fatalf("join fired after only one branch — after-join job appeared prematurely")
	}
	if s := getStatus(); s == instance.InstanceStatusCompleted {
		t.Fatalf("instance COMPLETED after only one branch — join fired too early")
	}

	// Complete branch-right.
	complete(pollOne("inv-join-right"))

	// Join should now fire. after-join job must appear.
	afterJoinJob := pollOne("inv-join-after")
	complete(afterJoinJob)

	// Instance must reach COMPLETED.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := getStatus(); s == instance.InstanceStatusCompleted {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("instance %s did not reach COMPLETED after all branches and after-join step completed", inst.ID)
}

// captureHandler is an slog.Handler that collects records for test assertions.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *captureHandler) hasError() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == slog.LevelError {
			return true
		}
	}
	return false
}

// TestAutoStartNextWorkflow_ErrorIsLogged verifies that when autoStartNextWorkflow
// fails (because the target definition does not exist), the error is logged at
// ERROR level rather than silently discarded.
//
// With the original code the goroutine uses _, _ = s.Start(...) so no log
// entry is produced and this test times out. After the fix, slog.Error is
// called and the test finds the record within the polling window.
func TestAutoStartNextWorkflow_ErrorIsLogged(t *testing.T) {
	capture := &captureHandler{}
	orig := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(orig) })

	ctx := context.Background()

	def := &definition.WorkflowDefinition{
		ID:                    "inv-autostart-err",
		Name:                  "AutoStart Error",
		AutoStartNextWorkflow: true,
		NextWorkflowId:        "inv-nonexistent-workflow-99",
		Steps: []definition.WorkflowStep{
			{ID: "task", Name: "Task", Type: definition.StepTypeServiceTask, JobType: "inv-autostart-task", NextStep: "end"},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}
	inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Poll and complete the single task so handleEnd fires.
	deadline := time.Now().Add(5 * time.Second)
	var j instance.Job
	for time.Now().Before(deadline) {
		jobs, _ := job.Poll(ctx, gJobStore, "test-worker", []string{"inv-autostart-task"}, 1)
		if len(jobs) > 0 {
			j = jobs[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if j.ID == "" {
		t.Fatalf("no job appeared for instance %s", inst.ID)
	}
	if err := gInstSvc.CompleteJobAndAdvance(ctx, j.ID, "test-worker", nil); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	// The goroutine starts the next workflow asynchronously. Poll until an
	// ERROR log record appears (the definition does not exist, so Start fails).
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if capture.hasError() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("expected ERROR log from failed autoStartNextWorkflow; none appeared within timeout")
}
