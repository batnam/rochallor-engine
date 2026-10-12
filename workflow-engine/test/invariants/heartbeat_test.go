//go:build integration

package invariants_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/id"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
)

func TestHeartbeatRejectsInactiveAttemptsWithoutMutation(t *testing.T) {
	for _, state := range []string{"wrong-worker", "expired", "completed", "failed", "cancelled", "replaced", "unlocked"} {
		t.Run(state, func(t *testing.T) {
			f := newCallbackFixture(t, "polling", 3)
			j := f.delivery(t, "owner")
			worker := "owner"
			switch state {
			case "wrong-worker":
				worker = "other"
			case "expired":
				_, err := gPool.Exec(f.ctx, `UPDATE job SET lock_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID)
				if err != nil {
					t.Fatal(err)
				}
			case "completed":
				f.complete(t, j, "owner")
			case "failed":
				if err := job.Fail(f.ctx, gDbConn, gJobStore, f.dispatcher, j.ID, "owner", "failure", false); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				if _, err := f.svc.Cancel(f.ctx, f.inst.ID, "cancelled"); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				if err := job.Retry(f.ctx, gDbConn, gJobStore, f.dispatcher, j.ID); err != nil {
					t.Fatal(err)
				}
			case "unlocked":
				_, err := gPool.Exec(f.ctx, `UPDATE job SET status='UNLOCKED' WHERE id=$1`, j.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			before := f.snapshot(t)
			if _, err := job.ExtendLease(f.ctx, gDbConn, gJobStore, j.ID, worker, 30); !errors.Is(err, job.ErrLeaseLost) {
				t.Fatalf("expected lease lost: %v", err)
			}
			f.assertUnchanged(t, before)
		})
	}
}

func TestHeartbeatRacesWithLifecycle(t *testing.T) {
	for _, operation := range []string{"complete", "fail", "cancel", "timer"} {
		for iteration := range 10 {
			t.Run(fmt.Sprintf("%s/%d", operation, iteration), func(t *testing.T) {
				f := newCallbackFixture(t, "polling", 3)
				var timerID string
				if operation == "timer" {
					def := timerDefinition(id.New(), true)
					def.Steps[0].RetryCount = 3
					if _, err := gDefRepo.Upload(f.ctx, def); err != nil {
						t.Fatal(err)
					}
					inst, err := gInstSvc.Start(f.ctx, def.ID, 0, nil, "")
					if err != nil {
						t.Fatal(err)
					}
					f.inst = inst
					f.svc = gInstSvc
					f.jobType = def.Steps[0].JobType
					_, err = gPool.Exec(f.ctx, `UPDATE boundary_event_schedule SET fire_at=clock_timestamp()-interval '1 second' WHERE instance_id=$1`, inst.ID)
					if err != nil {
						t.Fatal(err)
					}
					timerID = dueTimer(t, inst.ID).ID
				}
				j := f.delivery(t, "owner")
				ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
				defer cancel()
				start := make(chan struct{})
				errs := make(chan error, 2)
				go func() {
					<-start
					_, err := job.ExtendLease(ctx, gDbConn, gJobStore, j.ID, "owner", 60)
					if errors.Is(err, job.ErrLeaseLost) {
						err = nil
					}
					errs <- err
				}()
				go func() {
					<-start
					var err error
					switch operation {
					case "complete":
						err = f.svc.CompleteJobAndAdvance(ctx, j.ID, "owner", map[string]any{"result": "done"})
					case "fail":
						err = job.Fail(ctx, gDbConn, gJobStore, f.dispatcher, j.ID, "owner", "failure", true)
					case "cancel":
						_, err = f.svc.Cancel(ctx, f.inst.ID, "cancelled")
					case "timer":
						err = f.svc.FireBoundaryEvent(ctx, timerID, f.inst.ID)
					}
					errs <- err
				}()
				close(start)
				for range 2 {
					if err := <-errs; err != nil {
						t.Fatal(err)
					}
				}
				before := f.snapshot(t)
				if _, err := job.ExtendLease(f.ctx, gDbConn, gJobStore, j.ID, "owner", 60); !errors.Is(err, job.ErrLeaseLost) {
					t.Fatalf("terminal attempt renewed: %v", err)
				}
				f.assertUnchanged(t, before)
				var active int
				if err := gPool.QueryRow(f.ctx, `SELECT count(*) FROM job WHERE step_execution_id=$1 AND status IN ('LOCKED','UNLOCKED')`, j.StepExecutionID).Scan(&active); err != nil {
					t.Fatal(err)
				}
				if active > 1 {
					t.Fatalf("multiple active attempts: %d", active)
				}
			})
		}
	}
}

func TestHeartbeatCannotReviveLeaseExpiredWhileWaitingForLock(t *testing.T) {
	f := newCallbackFixture(t, "polling", 2)
	j := f.delivery(t, "owner")
	tx, err := gPool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `SELECT id FROM workflow_instance WHERE id=$1 FOR UPDATE`, f.inst.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE job SET lock_expires_at=clock_timestamp()+interval '200 milliseconds' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := job.ExtendLease(f.ctx, gDbConn, gJobStore, j.ID, "owner", 30); result <- err }()
	time.Sleep(300 * time.Millisecond)
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, job.ErrLeaseLost) {
		t.Fatalf("expired lease revived: %v", err)
	}
}

func TestHeartbeatAndSweeperChooseOneAttempt(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			f := newCallbackFixture(t, "polling", 3)
			j := f.delivery(t, "owner")
			if !valid {
				if _, err := gPool.Exec(f.ctx, `UPDATE job SET lock_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			job.StartLeaseSweeper(ctx, gDbConn, gJobStore, f.dispatcher, 5*time.Millisecond)
			for range 5 {
				_, err := job.ExtendLease(f.ctx, gDbConn, gJobStore, j.ID, "owner", 30)
				if valid && err != nil {
					t.Fatal(err)
				}
				if !valid && !errors.Is(err, job.ErrLeaseLost) {
					t.Fatalf("expired heartbeat: %v", err)
				}
			}
			if valid {
				time.Sleep(100 * time.Millisecond)
				var count int
				if err := gPool.QueryRow(f.ctx, `SELECT count(*) FROM job WHERE instance_id=$1`, f.inst.ID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("live lease replaced: %d", count)
				}
			} else {
				var fresh instance.Job
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					jobs, err := gJobStore.PollJobs(f.ctx, "owner", []string{f.jobType}, 1, 30)
					if err != nil {
						t.Fatal(err)
					}
					if len(jobs) > 0 {
						fresh = jobs[0]
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if fresh.ID == "" || fresh.ID == j.ID || fresh.RetriesRemaining != j.RetriesRemaining {
					t.Fatalf("invalid replacement: %+v", fresh)
				}
				var count int
				if err := gPool.QueryRow(f.ctx, `SELECT count(*) FROM job WHERE instance_id=$1`, f.inst.ID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 2 {
					t.Fatalf("replacement count=%d", count)
				}
			}
		})
	}
}
