//go:build integration

package invariants_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/id"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
	pgstore "github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
	"google.golang.org/protobuf/proto"
)

type callbackFixture struct {
	ctx                   context.Context
	svc                   *instance.Service
	dispatcher            dispatch.Dispatcher
	inst                  *instance.WorkflowInstance
	jobType, stepID, mode string
}

func newCallbackFixture(t *testing.T, mode string, retries int) *callbackFixture {
	t.Helper()
	ctx := t.Context()
	d := polling.New().Dispatcher()
	if mode == "kafka_outbox" {
		d = kafka_outbox.New(kafka_outbox.Config{Store: pgstore.NewOutboxStore(gPool)}).Dispatcher()
	}
	uid := id.New()
	def := buildSimpleChainDef("callbacks-"+uid, uid)
	def.Steps[0].RetryCount = retries
	def.Steps[0].OutputsSchema = &definition.Schema{
		Properties: map[string]definition.PropertyDescriptor{"result": {Type: definition.SchemaTypeString}},
	}
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatal(err)
	}
	svc := instance.NewService(ctx, gDbConn, pgstore.NewInstanceStore(gPool), gDefRepo, d)
	inst, err := svc.Start(ctx, def.ID, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return &callbackFixture{ctx: ctx, svc: svc, dispatcher: d, inst: inst, jobType: def.Steps[0].JobType, stepID: def.Steps[0].ID, mode: mode}
}

// Kafka tests decode the actual persisted event, without using the polling path.
func (f *callbackFixture) delivery(t *testing.T, worker string) instance.Job {
	t.Helper()
	if f.mode == "polling" {
		jobs, err := job.Poll(f.ctx, gJobStore, worker, []string{f.jobType}, 1)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("poll: count=%d err=%v", len(jobs), err)
		}
		return jobs[0]
	}
	var payload []byte
	if err := gPool.QueryRow(f.ctx, `SELECT o.payload FROM dispatch_outbox o JOIN job j ON j.id=o.job_id WHERE j.instance_id=$1 AND j.status='UNLOCKED'`, f.inst.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var event workflowv1.JobDispatchEvent
	if err := proto.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	return instance.Job{ID: event.JobId, InstanceID: event.InstanceId, StepExecutionID: event.StepExecutionId, JobType: event.JobType, RetriesRemaining: int(event.RetriesRemaining)}
}

// Compare all persisted business state, snapshots and dispatch rows so a no-op
// cannot quietly rewrite history, consume retries or enqueue extra work.
func (f *callbackFixture) snapshot(t *testing.T) string {
	t.Helper()
	var snapshot string
	err := gPool.QueryRow(f.ctx, `SELECT jsonb_build_object(
  'instance',to_jsonb(i),
  'jobs',(SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id) FROM job j WHERE j.instance_id=i.id),
  'steps',(SELECT jsonb_agg(to_jsonb(s) ORDER BY s.id) FROM step_execution s WHERE s.instance_id=i.id),
  'outbox',(SELECT jsonb_agg(to_jsonb(o) ORDER BY o.id) FROM dispatch_outbox o WHERE o.instance_id=i.id)
 )::text FROM workflow_instance i WHERE i.id=$1`, f.inst.ID).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f *callbackFixture) assertUnchanged(t *testing.T, before string) {
	t.Helper()
	if after := f.snapshot(t); after != before {
		t.Fatalf("callback changed persisted state\nbefore: %s\nafter: %s", before, after)
	}
}

func (f *callbackFixture) complete(t *testing.T, j instance.Job, worker string) {
	t.Helper()
	if err := f.svc.CompleteJobAndAdvance(f.ctx, j.ID, worker, map[string]any{"result": "current"}); err != nil {
		t.Fatal(err)
	}
}

func TestJobCallbacksPreserveTerminalState(t *testing.T) {
	for _, mode := range []string{"polling", "kafka_outbox"} {
		for _, terminal := range []string{"COMPLETED", "FAILED", "CANCELLED"} {
			t.Run(mode+"/"+terminal, func(t *testing.T) {
				f := newCallbackFixture(t, mode, 3)
				j := f.delivery(t, "worker")
				switch terminal {
				case "COMPLETED":
					f.complete(t, j, "worker")
				case "FAILED":
					if err := job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, j.ID, "worker", "original failure", false); err != nil {
						t.Fatal(err)
					}
				case "CANCELLED":
					if _, err := f.svc.Cancel(f.ctx, f.inst.ID, "cancelled"); err != nil {
						t.Fatal(err)
					}
				}
				before := f.snapshot(t)
				for _, result := range []any{"late", 123} {
					if err := f.svc.CompleteJobAndAdvance(f.ctx, j.ID, "worker", map[string]any{"result": result}); err != nil {
						t.Fatal(err)
					}
					f.assertUnchanged(t, before)
				}
				for _, retryable := range []bool{true, false} {
					if err := job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, j.ID, "worker", "late failure", retryable); err != nil {
						t.Fatal(err)
					}
					f.assertUnchanged(t, before)
				}
				if err := job.Complete(f.ctx, gDbConn, gJobStore, nil, j.ID, "worker", map[string]any{"result": "legacy"}); err != nil {
					t.Fatal(err)
				}
				f.assertUnchanged(t, before)
			})
		}
	}
}

func TestJobCallbacksRetryIsIdempotent(t *testing.T) {
	for _, mode := range []string{"polling", "kafka_outbox"} {
		t.Run(mode, func(t *testing.T) {
			f := newCallbackFixture(t, mode, 3)
			old := f.delivery(t, "worker")
			errs := make(chan error, 8)
			start := make(chan struct{})
			for range 8 {
				go func() {
					<-start
					errs <- job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, old.ID, "worker", "same failure", true)
				}()
			}
			close(start)
			for range 8 {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}
			fresh := f.delivery(t, "worker")
			if fresh.ID == old.ID || fresh.StepExecutionID != old.StepExecutionID || fresh.RetriesRemaining != 2 {
				t.Fatalf("incorrect retry identity/budget: old=%+v new=%+v", old, fresh)
			}
			var jobs, outbox int
			if err := gPool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM job WHERE instance_id=$1),(SELECT count(*) FROM dispatch_outbox WHERE instance_id=$1)`, f.inst.ID).Scan(&jobs, &outbox); err != nil {
				t.Fatal(err)
			}
			expectedOutbox := 0
			if mode == "kafka_outbox" {
				expectedOutbox = 2
			}
			if jobs != 2 || outbox != expectedOutbox {
				t.Fatalf("duplicate retry: jobs=%d outbox=%d", jobs, outbox)
			}
			before := f.snapshot(t)
			f.complete(t, old, "worker")
			if err := job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, old.ID, "worker", "late", false); err != nil {
				t.Fatal(err)
			}
			f.assertUnchanged(t, before)
			f.complete(t, fresh, "worker")
			before = f.snapshot(t)
			f.complete(t, fresh, "worker")
			f.assertUnchanged(t, before)
			got, err := f.svc.Get(f.ctx, f.inst.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != instance.InstanceStatusCompleted {
				t.Fatalf("current attempt did not finish: %s", got.Status)
			}
		})
	}
}

func TestJobCallbacksOldJobAfterManualRetry(t *testing.T) {
	for _, mode := range []string{"polling", "kafka_outbox"} {
		t.Run(mode, func(t *testing.T) {
			f := newCallbackFixture(t, mode, 0)
			old := f.delivery(t, "worker")
			if err := job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, old.ID, "worker", "terminal", false); err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.RetryFailedStep(f.ctx, f.inst.ID, f.stepID, nil); err != nil {
				t.Fatal(err)
			}
			fresh := f.delivery(t, "worker")
			if fresh.ID == old.ID || fresh.StepExecutionID == old.StepExecutionID {
				t.Fatal("manual retry reused execution identity")
			}
			before := f.snapshot(t)
			f.complete(t, old, "worker")
			if err := job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, old.ID, "worker", "late", true); err != nil {
				t.Fatal(err)
			}
			f.assertUnchanged(t, before)
			f.complete(t, fresh, "worker")
		})
	}
}

func TestJobCallbacksLeaseRecovery(t *testing.T) {
	for _, newWorker := range []string{"original-worker", "different-worker"} {
		t.Run(newWorker, func(t *testing.T) {
			f := newCallbackFixture(t, "polling", 3)
			old := f.delivery(t, "original-worker")
			if _, err := gPool.Exec(f.ctx, `UPDATE job SET lock_expires_at=now()-interval '1 second' WHERE id=$1`, old.ID); err != nil {
				t.Fatal(err)
			}
			before := f.snapshot(t)
			f.complete(t, old, "original-worker")
			if err := job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, old.ID, "original-worker", "expired", true); err != nil {
				t.Fatal(err)
			}
			f.assertUnchanged(t, before)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			job.StartLeaseSweeper(ctx, gDbConn, gJobStore, f.dispatcher, 10*time.Millisecond)
			var fresh instance.Job
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				jobs, err := job.Poll(f.ctx, gJobStore, newWorker, []string{f.jobType}, 1)
				if err != nil {
					t.Fatal(err)
				}
				if len(jobs) > 0 {
					fresh = jobs[0]
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			if fresh.ID == "" || fresh.ID == old.ID || fresh.RetriesRemaining != old.RetriesRemaining {
				t.Fatalf("lease not replaced correctly: old=%+v new=%+v", old, fresh)
			}
			before = f.snapshot(t)
			f.complete(t, old, "original-worker")
			if err := job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, old.ID, "original-worker", "stale", false); err != nil {
				t.Fatal(err)
			}
			f.assertUnchanged(t, before)
			f.complete(t, fresh, newWorker)
		})
	}
}

func TestJobCallbacksWrongPollingWorker(t *testing.T) {
	f := newCallbackFixture(t, "polling", 2)
	j := f.delivery(t, "owner")
	before := f.snapshot(t)
	f.complete(t, j, "other")
	if err := job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, j.ID, "other", "wrong lease", true); err != nil {
		t.Fatal(err)
	}
	f.assertUnchanged(t, before)
	f.complete(t, j, "owner")
}

type failingDispatch struct{ dispatch.Dispatcher }

func (d failingDispatch) Enqueue(ctx context.Context, tx db.Tx, j dispatch.DispatchJob) error {
	if err := d.Dispatcher.Enqueue(ctx, tx, j); err != nil {
		return err
	}
	return errors.New("injected enqueue failure")
}

func TestJobCallbacksReplacementRollback(t *testing.T) {
	f := newCallbackFixture(t, "kafka_outbox", 2)
	j := f.delivery(t, "worker")
	before := f.snapshot(t)
	if err := job.Fail(f.ctx, gDbConn, gJobStore, failingDispatch{f.dispatcher}, j.ID, "worker", "retry", true); err == nil {
		t.Fatal("expected dispatch failure")
	}
	f.assertUnchanged(t, before)
	if err := job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, j.ID, "worker", "retry", true); err != nil {
		t.Fatal(err)
	}
	fresh := f.delivery(t, "worker")
	f.complete(t, fresh, "worker")
}

func TestJobCallbacksConcurrentCompleteAndFail(t *testing.T) {
	for _, mode := range []string{"polling", "kafka_outbox"} {
		t.Run(mode, func(t *testing.T) {
			f := newCallbackFixture(t, mode, 0)
			j := f.delivery(t, "worker")
			ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
			defer cancel()
			start := make(chan struct{})
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				errs <- f.svc.CompleteJobAndAdvance(ctx, j.ID, "worker", map[string]any{"result": "winner"})
			}()
			go func() {
				defer wg.Done()
				<-start
				errs <- job.Fail(ctx, gDbConn, gJobStore, f.dispatcher, j.ID, "worker", "winner", false)
			}()
			close(start)
			wg.Wait()
			for range 2 {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}
			var iStatus, jStatus, sStatus string
			if err := gPool.QueryRow(f.ctx, `SELECT i.status,j.status,s.status FROM workflow_instance i JOIN job j ON j.instance_id=i.id JOIN step_execution s ON s.id=j.step_execution_id WHERE j.id=$1`, j.ID).Scan(&iStatus, &jStatus, &sStatus); err != nil {
				t.Fatal(err)
			}
			if (iStatus != "COMPLETED" && iStatus != "FAILED") || iStatus != jStatus || jStatus != sStatus {
				t.Fatalf("inconsistent winner: instance=%s job=%s step=%s", iStatus, jStatus, sStatus)
			}
		})
	}
}

func TestJobRetryPreservesInactiveAttempt(t *testing.T) {
	f := newCallbackFixture(t, "polling", 2)
	j := f.delivery(t, "worker")
	if _, err := f.svc.Cancel(f.ctx, f.inst.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot(t)
	if err := job.Retry(f.ctx, gDbConn, gJobStore, f.dispatcher, j.ID); err != nil {
		t.Fatal(err)
	}
	f.assertUnchanged(t, before)
}
