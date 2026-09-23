//go:build integration

package invariants_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
	"github.com/jackc/pgx/v5/pgxpool"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/boundary"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/id"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	pgstore "github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

// Fail at the database commit boundary, after the transaction body has run.
// All writes still use real PostgreSQL and must roll back together.
type rollbackOnceDB struct {
	db.DB
	armed        atomic.Bool
	failed       chan struct{}
	beforeCommit func(context.Context, db.Tx) error
}

func (d *rollbackOnceDB) RunInTx(ctx context.Context, name string, fn func(db.Tx) error) error {
	if !d.armed.Swap(false) {
		return d.DB.RunInTx(ctx, name, fn)
	}
	err := d.DB.RunInTx(ctx, name, func(tx db.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if d.beforeCommit != nil {
			return d.beforeCommit(ctx, tx)
		}
		return errors.New("injected failure before commit")
	})
	close(d.failed)
	return err
}

func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func timerDefinition(uid string, interrupting bool) *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{ID: "timer-" + uid, Name: "Durable timer", Steps: []definition.WorkflowStep{
		{ID: "work", Name: "Work", Type: definition.StepTypeServiceTask, JobType: "work-" + uid, NextStep: "end", BoundaryEvents: []definition.BoundaryEvent{{Type: definition.BoundaryEventTypeTimer, Duration: "PT1S", TargetStepId: "timeout", Interrupting: interrupting}}},
		{ID: "timeout", Name: "Timeout", Type: definition.StepTypeServiceTask, JobType: "timeout-" + uid, NextStep: "end"},
		{ID: "end", Name: "End", Type: definition.StepTypeEnd},
	}}
}

func TestTimerRecoversAfterTransactionRollback(t *testing.T) {
	for _, mode := range []string{"polling", "kafka_outbox"} {
		for _, interrupting := range []bool{false, true} {
			for _, disconnect := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/interrupt=%t/disconnect=%t", mode, interrupting, disconnect), func(t *testing.T) { testTimerRecovery(t, mode, interrupting, disconnect) })
			}
		}
	}
}

func dispatcherForDurability(mode string) dispatch.Dispatcher {
	if mode == "kafka_outbox" {
		return kafka_outbox.New(kafka_outbox.Config{Store: pgstore.NewOutboxStore(gPool)}).Dispatcher()
	}
	return polling.New().Dispatcher()
}

func testTimerRecovery(t *testing.T, mode string, interrupting, disconnect bool) {
	ctx := t.Context()
	def := timerDefinition(id.New(), interrupting)
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatal(err)
	}
	fault := &rollbackOnceDB{DB: gDbConn, failed: make(chan struct{})}
	if disconnect {
		fault.beforeCommit = func(ctx context.Context, tx db.Tx) error {
			// Kill the PostgreSQL backend while it still owns the uncommitted work.
			_, err := gPool.Exec(ctx, "SELECT pg_terminate_backend($1)", pgstore.Unwrap(tx).Conn().PgConn().PID())
			return err
		}
	}
	svc := instance.NewService(ctx, fault, pgstore.NewInstanceStore(gPool), gDefRepo, dispatcherForDurability(mode))
	inst, err := svc.Start(ctx, def.ID, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	fault.armed.Store(true)
	firstCtx, stopFirst := context.WithCancel(ctx)
	defer stopFirst()
	boundary.StartTimerSweeper(firstCtx, gDbConn, pgstore.NewBoundaryStore(gPool), svc, 10*time.Millisecond)
	select {
	case <-fault.failed:
	case <-time.After(5 * time.Second):
		t.Fatal("timer never attempted dispatch")
	}
	stopFirst()
	history, err := svc.GetHistory(ctx, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Status != instance.StepExecutionStatusRunning {
		t.Fatalf("rollback changed history: %+v", history)
	}
	// A fresh service represents a restarted engine, without any in-memory retry.
	restarted := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, dispatcherForDurability(mode))
	resumedCtx, stopResumed := context.WithCancel(ctx)
	defer stopResumed()
	boundary.StartTimerSweeper(resumedCtx, gDbConn, pgstore.NewBoundaryStore(gPool), restarted, 10*time.Millisecond)
	waitFor(t, func() bool {
		history, err = restarted.GetHistory(ctx, inst.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(history) == 2
	})
	expected := instance.StepExecutionStatusRunning
	if interrupting {
		expected = instance.StepExecutionStatusFailed
	}
	if history[0].Status != expected || history[1].StepID != "timeout" {
		t.Fatalf("timer did not recover: %+v", history)
	}
	assertDurableDispatchCounts(t, inst.ID, mode, 2)
}

func chainDefinitions(uid string) (*definition.WorkflowDefinition, *definition.WorkflowDefinition) {
	child := &definition.WorkflowDefinition{ID: "child-" + uid, Name: "Child", Steps: []definition.WorkflowStep{{ID: "end", Name: "End", Type: definition.StepTypeEnd}}}
	parent := &definition.WorkflowDefinition{ID: "parent-" + uid, Name: "Parent", AutoStartNextWorkflow: true, NextWorkflowId: child.ID, Steps: []definition.WorkflowStep{{ID: "end", Name: "End", Type: definition.StepTypeEnd}}}
	return parent, child
}

func TestWorkflowChainDoesNotEscapeParentRollback(t *testing.T) {
	ctx := t.Context()
	parent, child := chainDefinitions(id.New())
	for _, def := range []*definition.WorkflowDefinition{child, parent} {
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatal(err)
		}
	}
	fault := &rollbackOnceDB{DB: gDbConn, failed: make(chan struct{})}
	fault.armed.Store(true)
	svc := instance.NewService(ctx, fault, pgstore.NewInstanceStore(gPool), gDefRepo, polling.New().Dispatcher())
	if _, err := svc.Start(ctx, parent.ID, 0, map[string]any{"amount": 42}, "rollback-key"); err == nil {
		t.Fatal("expected parent rollback")
	}
	if err := svc.ProcessPendingChains(ctx); err != nil {
		t.Fatal(err)
	}
	// Allow the asynchronous path to run; neither parent nor child may exist.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, def := range []*definition.WorkflowDefinition{parent, child} {
			instances, err := svc.List(ctx, def.ID, "", "", 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			if instances.Total != 0 {
				t.Fatalf("rolled-back parent created %s: %+v", def.ID, instances.Items)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertDurableDispatchCounts(t *testing.T, instanceID, mode string, want int) {
	t.Helper()
	var jobs, outbox int
	err := gPool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM job WHERE instance_id=$1),(SELECT count(*) FROM dispatch_outbox WHERE instance_id=$1)`, instanceID).Scan(&jobs, &outbox)
	if err != nil {
		t.Fatal(err)
	}
	expectedOutbox := 0
	if mode == "kafka_outbox" {
		expectedOutbox = want
	}
	if jobs != want || outbox != expectedOutbox {
		t.Fatalf("dispatch counts: jobs=%d outbox=%d; want %d/%d", jobs, outbox, want, expectedOutbox)
	}
}

func dueTimer(t *testing.T, instanceID string) boundary.DueEvent {
	t.Helper()
	var result boundary.DueEvent
	waitFor(t, func() bool {
		due, err := pgstore.NewBoundaryStore(gPool).ListDueBoundaryEvents(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range due {
			if e.InstanceID == instanceID {
				result = e
				return true
			}
		}
		return false
	})
	return result
}

func TestTimerConcurrentRedeliveryDispatchesOnce(t *testing.T) {
	for _, mode := range []string{"polling", "kafka_outbox"} {
		for _, interrupting := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/interrupt=%t", mode, interrupting), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				def := timerDefinition(id.New(), interrupting)
				if _, err := gDefRepo.Upload(ctx, def); err != nil {
					t.Fatal(err)
				}
				svc := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, dispatcherForDurability(mode))
				inst, err := svc.Start(ctx, def.ID, 0, nil, "")
				if err != nil {
					t.Fatal(err)
				}

				fixture := &callbackFixture{ctx: ctx, svc: svc, dispatcher: dispatcherForDurability(mode), inst: inst, jobType: def.Steps[0].JobType, mode: mode}
				original := fixture.delivery(t, "worker")
				e := dueTimer(t, inst.ID)
				errs := make(chan error, 8)
				for range 8 {
					go func() { errs <- svc.FireBoundaryEvent(ctx, e.ID, inst.ID) }()
				}
				for range 8 {
					if err := <-errs; err != nil {
						t.Fatal(err)
					}
				}
				history, err := svc.GetHistory(ctx, inst.ID)
				if err != nil {
					t.Fatal(err)
				}
				expected := instance.StepExecutionStatusRunning
				if interrupting {
					expected = instance.StepExecutionStatusFailed
				}
				if len(history) != 2 || history[0].Status != expected || history[1].StepID != "timeout" {
					t.Fatalf("unexpected timer history: %+v", history)
				}
				// Replay after a fresh service starts is still a no-op.
				fresh := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, dispatcherForDurability(mode))
				if err := fresh.FireBoundaryEvent(ctx, e.ID, inst.ID); err != nil {
					t.Fatal(err)
				}
				assertDurableDispatchCounts(t, inst.ID, mode, 2)
				if interrupting {
					before := fixture.snapshot(t)
					fixture.complete(t, original, "worker")
					if err := job.Fail(ctx, gDbConn, gJobStore, fixture.dispatcher, original.ID, "worker", "late after interruption", true); err != nil {
						t.Fatal(err)
					}
					fixture.assertUnchanged(t, before)
				}

			})
		}
	}
}

func TestTimerDispatchFailureRollsBackInterruptionAndOutbox(t *testing.T) {
	ctx := t.Context()
	def := timerDefinition(id.New(), true)
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatal(err)
	}
	dispatcher := dispatcherForDurability("kafka_outbox")
	svc := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, dispatcher)
	inst, err := svc.Start(ctx, def.ID, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	e := dueTimer(t, inst.ID)
	failing := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, failingDispatch{dispatcher})
	if err := failing.FireBoundaryEvent(ctx, e.ID, inst.ID); err == nil {
		t.Fatal("expected enqueue failure")
	}
	history, err := svc.GetHistory(ctx, inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Status != instance.StepExecutionStatusRunning {
		t.Fatalf("failed dispatch interrupted parent: %+v", history)
	}
	assertDurableDispatchCounts(t, inst.ID, "kafka_outbox", 1)
	if err := svc.FireBoundaryEvent(ctx, e.ID, inst.ID); err != nil {
		t.Fatal(err)
	}
	assertDurableDispatchCounts(t, inst.ID, "kafka_outbox", 2)
}

func TestTimerSuppressedAfterCompletionOrCancel(t *testing.T) {
	for _, action := range []string{"complete", "step-complete", "cancel"} {
		t.Run(action, func(t *testing.T) {
			ctx := t.Context()
			def := timerDefinition(id.New(), true)
			if action == "step-complete" {
				def.Steps[0].NextStep = "park"
				def.Steps = append(def.Steps, definition.WorkflowStep{ID: "park", Name: "Park", Type: definition.StepTypeWait, NextStep: "end"})
			}
			if _, err := gDefRepo.Upload(ctx, def); err != nil {
				t.Fatal(err)
			}
			svc := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, polling.New().Dispatcher())
			inst, err := svc.Start(ctx, def.ID, 0, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			jobs, err := job.Poll(ctx, gJobStore, "worker", []string{def.Steps[0].JobType}, 1)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("poll: %+v %v", jobs, err)
			}
			if action == "complete" || action == "step-complete" {
				err = svc.CompleteJobAndAdvance(ctx, jobs[0].ID, "worker", nil)
			} else {
				_, err = svc.Cancel(ctx, inst.ID, "operator")
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := svc.GetHistory(ctx, inst.ID)
			if err != nil {
				t.Fatal(err)
			}
			e := dueTimer(t, inst.ID)
			if err := svc.FireBoundaryEvent(ctx, e.ID, inst.ID); err != nil {
				t.Fatal(err)
			}
			after, err := svc.GetHistory(ctx, inst.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Fatalf("terminal timer changed history: %+v", after)
			}
			assertDurableDispatchCounts(t, inst.ID, "polling", 1)
		})
	}
}

func TestTimerInterruptsUserTaskWithoutLeavingItCompletable(t *testing.T) {
	ctx := t.Context()
	def := timerDefinition(id.New(), true)
	def.Steps[0].Type = definition.StepTypeUserTask
	def.Steps[0].JobType = ""
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatal(err)
	}
	inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	e := dueTimer(t, inst.ID)
	if err := gInstSvc.FireBoundaryEvent(ctx, e.ID, inst.ID); err != nil {
		t.Fatal(err)
	}
	if err := gInstSvc.CompleteUserTaskAndAdvance(ctx, inst.ID, "work", "operator", map[string]any{"stale": true}); !errors.Is(err, instance.ErrUserTaskNotFound) {
		t.Fatalf("interrupted user task still completable: %v", err)
	}
}

func TestWorkflowChainRecoversMissingDefinitionAfterRestart(t *testing.T) {
	ctx := t.Context()
	parent, child := chainDefinitions(id.New())
	if _, err := gDefRepo.Upload(ctx, parent); err != nil {
		t.Fatal(err)
	}
	inst, err := gInstSvc.Start(ctx, parent.ID, 0, map[string]any{"amount": 42, "nested": map[string]any{"approved": true}}, "chain-key")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = gPool.Exec(context.Background(), `DELETE FROM workflow_chain WHERE source_instance_id=$1`, inst.ID)
	})
	if err := gInstSvc.ProcessPendingChains(ctx); err == nil {
		t.Fatal("expected missing definition")
	}
	if _, err := gDefRepo.Upload(ctx, child); err != nil {
		t.Fatal(err)
	}
	restarted := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, polling.New().Dispatcher())
	restarted.StartChainWorker(ctx, 10*time.Millisecond)
	var children instance.ListResult
	waitFor(t, func() bool {
		children, err = restarted.List(ctx, child.ID, "", "", 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		return len(children.Items) == 1
	})
	got := children.Items[0]
	var vars struct {
		Amount int
		Nested struct{ Approved bool }
	}
	if err := json.Unmarshal(got.Variables, &vars); err != nil {
		t.Fatal(err)
	}
	if got.Status != instance.InstanceStatusCompleted || got.BusinessKey == nil || *got.BusinessKey != "chain-key" || vars.Amount != 42 || !vars.Nested.Approved {
		t.Fatalf("child lost completion snapshot: %+v", got)
	}
}

func TestWorkflowChainChildCommitFailureIsRetryable(t *testing.T) {
	for _, mode := range []string{"polling", "kafka_outbox"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			parent, child := chainDefinitions(id.New())
			child.Steps = []definition.WorkflowStep{{ID: "task", Name: "Task", Type: definition.StepTypeServiceTask, JobType: child.ID, NextStep: "end"}, child.Steps[0]}
			for _, def := range []*definition.WorkflowDefinition{child, parent} {
				if _, err := gDefRepo.Upload(ctx, def); err != nil {
					t.Fatal(err)
				}
			}
			fault := &rollbackOnceDB{DB: gDbConn, failed: make(chan struct{})}
			svc := instance.NewService(ctx, fault, pgstore.NewInstanceStore(gPool), gDefRepo, dispatcherForDurability(mode))
			inst, err := svc.Start(ctx, parent.ID, 0, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			fault.armed.Store(true)
			if err := svc.ProcessPendingChains(ctx); err == nil {
				t.Fatal("expected child commit failure")
			}
			children, err := svc.List(ctx, child.ID, "", "", 0, 10)
			if err != nil {
				t.Fatal(err)
			}
			if children.Total != 0 {
				t.Fatalf("child escaped rollback: %+v", children)
			}
			got, err := svc.Get(ctx, inst.ID)
			if err != nil || got.Status != instance.InstanceStatusCompleted {
				t.Fatalf("child failure changed completed parent: %+v %v", got, err)
			}
			fresh := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, dispatcherForDurability(mode))
			if err := fresh.ProcessPendingChains(ctx); err != nil {
				t.Fatal(err)
			}
			children, err = fresh.List(ctx, child.ID, "", "", 0, 10)
			if err != nil || len(children.Items) != 1 {
				t.Fatalf("child not recovered: %+v %v", children, err)
			}
			assertDurableDispatchCounts(t, children.Items[0].ID, mode, 1)
		})
	}
}

func TestWorkflowChainConcurrentAndRepeatedDeliveryCreatesOneChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	parent, child := chainDefinitions(id.New())
	for _, def := range []*definition.WorkflowDefinition{child, parent} {
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gInstSvc.Start(ctx, parent.ID, 0, nil, ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fresh := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, polling.New().Dispatcher())
			errs <- fresh.ProcessPendingChains(ctx)
		}()
	}
	wg.Wait()
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	// END-only children are already terminal: no active business-key uniqueness
	// constraint can hide a duplicate chain start.
	for range 3 {
		if err := gInstSvc.ProcessPendingChains(ctx); err != nil {
			t.Fatal(err)
		}
	}
	children, err := gInstSvc.List(ctx, child.ID, "", "", 0, 10)
	if err != nil || children.Total != 1 || len(children.Items) != 1 || children.Items[0].Status != instance.InstanceStatusCompleted {
		t.Fatalf("duplicate child after replay: %+v %v", children, err)
	}
}

type pausedCommitDB struct {
	db.DB
	reached chan struct{}
	release chan struct{}
}

func (d pausedCommitDB) RunInTx(ctx context.Context, name string, fn func(db.Tx) error) error {
	return d.DB.RunInTx(ctx, name, func(tx db.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		close(d.reached)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-d.release:
			return nil
		}
	})
}

func TestWorkflowChainIsInvisibleUntilParentCommits(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	parent, child := chainDefinitions(id.New())
	for _, def := range []*definition.WorkflowDefinition{child, parent} {
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatal(err)
		}
	}
	paused := pausedCommitDB{DB: gDbConn, reached: make(chan struct{}), release: make(chan struct{})}
	svc := instance.NewService(ctx, paused, pgstore.NewInstanceStore(gPool), gDefRepo, polling.New().Dispatcher())
	done := make(chan error, 1)
	go func() { _, err := svc.Start(ctx, parent.ID, 0, nil, ""); done <- err }()
	select {
	case <-paused.reached:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A separate engine can scan the database, but it cannot see an uncommitted request.
	if err := gInstSvc.ProcessPendingChains(ctx); err != nil {
		t.Fatal(err)
	}
	for _, def := range []*definition.WorkflowDefinition{parent, child} {
		got, err := gInstSvc.List(ctx, def.ID, "", "", 0, 10)
		if err != nil || got.Total != 0 {
			t.Fatalf("uncommitted workflow became visible: %+v %v", got, err)
		}
	}
	close(paused.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := gInstSvc.ProcessPendingChains(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := gInstSvc.List(ctx, child.ID, "", "", 0, 10)
	if err != nil || got.Total != 1 {
		t.Fatalf("committed chain not delivered: %+v %v", got, err)
	}
}

func TestWorkflowChainRetriesSchemaAndBusinessKeyFailures(t *testing.T) {
	for _, failure := range []string{"schema", "business-key"} {
		t.Run(failure, func(t *testing.T) {
			ctx := t.Context()
			parent, child := chainDefinitions(id.New())
			if failure == "schema" {
				child.InputSchema = &definition.Schema{Properties: map[string]definition.PropertyDescriptor{"amount": {Type: definition.SchemaTypeString}}}
			} else {
				child.Steps = []definition.WorkflowStep{{ID: "park", Name: "Park", Type: definition.StepTypeWait, NextStep: "end"}, child.Steps[0]}
			}
			for _, def := range []*definition.WorkflowDefinition{child, parent} {
				if _, err := gDefRepo.Upload(ctx, def); err != nil {
					t.Fatal(err)
				}
			}
			var existing *instance.WorkflowInstance
			var err error
			if failure == "business-key" {
				existing, err = gInstSvc.Start(ctx, child.ID, 0, nil, "same-key")
				if err != nil {
					t.Fatal(err)
				}
			}
			inst, err := gInstSvc.Start(ctx, parent.ID, 0, map[string]any{"amount": 42}, "same-key")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = gPool.Exec(context.Background(), `DELETE FROM workflow_chain WHERE source_instance_id=$1`, inst.ID)
			})
			if err := gInstSvc.ProcessPendingChains(ctx); err == nil {
				t.Fatal("expected child start failure")
			}
			if failure == "schema" {
				child.InputSchema = nil
				if _, err := gDefRepo.Upload(ctx, child); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := gInstSvc.Cancel(ctx, existing.ID, "free business key"); err != nil {
					t.Fatal(err)
				}
			}
			if err := gInstSvc.ProcessPendingChains(ctx); err != nil {
				t.Fatal(err)
			}
			status := string(instance.InstanceStatusCompleted)
			if failure == "business-key" {
				status = string(instance.InstanceStatusWaiting)
			}
			got, err := gInstSvc.List(ctx, child.ID, status, "same-key", 0, 10)
			if err != nil || got.Total != 1 || len(got.Items) != 1 {
				t.Fatalf("child was not retried: %+v %v", got, err)
			}
			if failure == "schema" && got.Items[0].DefinitionVersion != 2 {
				t.Fatalf("retry did not use latest fixed definition: %+v", got.Items[0])
			}
		})
	}
}

func TestWorkflowChainFailedRequestDoesNotBlockOtherParents(t *testing.T) {
	ctx := t.Context()
	bad, badChild := chainDefinitions(id.New())
	good, goodChild := chainDefinitions(id.New())
	for _, def := range []*definition.WorkflowDefinition{bad, good, goodChild} {
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatal(err)
		}
	}
	badInst, err := gInstSvc.Start(ctx, bad.ID, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = gPool.Exec(context.Background(), `DELETE FROM workflow_chain WHERE source_instance_id=$1`, badInst.ID)
	})
	if _, err := gInstSvc.Start(ctx, good.ID, 0, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := gInstSvc.ProcessPendingChains(ctx); err == nil {
		t.Fatal("missing target should report failure")
	}
	children, err := gInstSvc.List(ctx, goodChild.ID, "", "", 0, 10)
	if err != nil || children.Total != 1 {
		t.Fatalf("bad request blocked valid parent: %+v %v", children, err)
	}
	if _, err := gDefRepo.Upload(ctx, badChild); err != nil {
		t.Fatal(err)
	}
	if err := gInstSvc.ProcessPendingChains(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTimerRacesWithCompletion(t *testing.T) {
	for _, mode := range []string{"polling", "kafka_outbox"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			def := timerDefinition(id.New(), true)
			if _, err := gDefRepo.Upload(ctx, def); err != nil {
				t.Fatal(err)
			}
			svc := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, dispatcherForDurability(mode))
			inst, err := svc.Start(ctx, def.ID, 0, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			f := &callbackFixture{ctx: ctx, svc: svc, dispatcher: dispatcherForDurability(mode), inst: inst, jobType: def.Steps[0].JobType, mode: mode}
			j := f.delivery(t, "worker")
			e := dueTimer(t, inst.ID)
			start := make(chan struct{})
			errs := make(chan error, 2)
			go func() { <-start; errs <- svc.FireBoundaryEvent(ctx, e.ID, inst.ID) }()
			go func() {
				<-start
				errs <- svc.CompleteJobAndAdvance(ctx, j.ID, "worker", map[string]any{"winner": "completion"})
			}()
			close(start)
			for range 2 {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}
			history, err := svc.GetHistory(ctx, inst.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != 2 {
				t.Fatalf("race dispatched extra work: %+v", history)
			}
			got, err := svc.Get(ctx, inst.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch history[0].Status {
			case instance.StepExecutionStatusCompleted:
				if history[1].StepID != "end" || got.Status != instance.InstanceStatusCompleted {
					t.Fatalf("inconsistent completion winner: %+v %+v", history, got)
				}
				assertDurableDispatchCounts(t, inst.ID, mode, 1)
			case instance.StepExecutionStatusFailed:
				if history[1].StepID != "timeout" || got.Status != instance.InstanceStatusActive || string(got.Variables) != "{}" {
					t.Fatalf("stale completion overrode timer: %+v %+v", history, got)
				}
				assertDurableDispatchCounts(t, inst.ID, mode, 2)
			default:
				t.Fatalf("no coherent race winner: %+v", history)
			}
		})
	}
}

// Hold two transactions open at once to expose pool starvation caused by a
// second pooled query from inside the timer transaction.
type timerBarrierDB struct {
	db.DB
	entered chan struct{}
	release chan struct{}
}

func (d timerBarrierDB) RunInTx(ctx context.Context, name string, fn func(db.Tx) error) error {
	return d.DB.RunInTx(ctx, name, func(tx db.Tx) error {
		d.entered <- struct{}{}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-d.release:
			return fn(tx)
		}
	})
}

func TestTimersProgressWithSmallConnectionPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var timers []boundary.DueEvent
	for range 2 {
		def := timerDefinition(id.New(), true)
		if _, err := gDefRepo.Upload(ctx, def); err != nil {
			t.Fatal(err)
		}
		inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		timers = append(timers, dueTimer(t, inst.ID))
	}
	config := gPool.Config().Copy()
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dbc := timerBarrierDB{DB: pgstore.NewDB(pool), entered: make(chan struct{}, 2), release: make(chan struct{})}
	svc := instance.NewService(ctx, dbc, pgstore.NewInstanceStore(pool), pgstore.NewDefinitionStore(pool), polling.New().Dispatcher())
	errs := make(chan error, 2)
	for _, timer := range timers {
		go func() { errs <- svc.FireBoundaryEvent(ctx, timer.ID, timer.InstanceID) }()
	}
	for range 2 {
		select {
		case <-dbc.entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(dbc.release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("timers stalled with all transaction connections occupied: %v", err)
		}
	}
}

func TestTimerRecoversFromCrashAfterClaimBeforeDispatch(t *testing.T) {
	ctx := t.Context()
	// An AFTER UPDATE trigger kills the PostgreSQL backend after the timer is
	// claimed but before the engine can issue any target-dispatch statements.
	_, err := gPool.Exec(ctx, `CREATE FUNCTION test_crash_after_timer_claim() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
  IF NEW.id = TG_ARGV[0] THEN PERFORM pg_terminate_backend(pg_backend_pid()); END IF;
  RETURN NEW;
 END $$`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = gPool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_timer_claim_crash ON boundary_event_schedule; DROP FUNCTION IF EXISTS test_crash_after_timer_claim()`)
	})
	for _, mode := range []string{"polling", "kafka_outbox"} {
		t.Run(mode, func(t *testing.T) {
			def := timerDefinition(id.New(), true)
			if _, err := gDefRepo.Upload(ctx, def); err != nil {
				t.Fatal(err)
			}
			svc := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, dispatcherForDurability(mode))
			inst, err := svc.Start(ctx, def.ID, 0, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			e := dueTimer(t, inst.ID)
			// e.ID is an engine-generated ULID, not caller-controlled SQL input.
			_, err = gPool.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER test_timer_claim_crash AFTER UPDATE OF fired ON boundary_event_schedule
   FOR EACH ROW WHEN (NEW.fired) EXECUTE FUNCTION test_crash_after_timer_claim('%s')`, e.ID))
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.FireBoundaryEvent(ctx, e.ID, inst.ID); err == nil {
				t.Fatal("expected backend termination during claim")
			}
			if _, err := gPool.Exec(ctx, `DROP TRIGGER test_timer_claim_crash ON boundary_event_schedule`); err != nil {
				t.Fatal(err)
			}
			history, err := svc.GetHistory(ctx, inst.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != 1 || history[0].Status != instance.StepExecutionStatusRunning {
				t.Fatalf("partial interruption escaped crashed claim: %+v", history)
			}
			assertDurableDispatchCounts(t, inst.ID, mode, 1)
			if pending := dueTimer(t, inst.ID); pending.ID != e.ID {
				t.Fatal("crashed claim lost timer")
			}
			fresh := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, dispatcherForDurability(mode))
			if err := fresh.FireBoundaryEvent(ctx, e.ID, inst.ID); err != nil {
				t.Fatal(err)
			}
			if err := fresh.FireBoundaryEvent(ctx, e.ID, inst.ID); err != nil {
				t.Fatal(err)
			}
			assertDurableDispatchCounts(t, inst.ID, mode, 2)
		})
	}
}
