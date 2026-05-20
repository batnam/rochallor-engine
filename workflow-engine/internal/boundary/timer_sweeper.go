// Package boundary implements the background TIMER boundary-event sweeper.
package boundary

import (
	"context"
	"log/slog"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
)

// timerSweeperLockKey gates the timer sweeper across replicas via a
// session-level PostgreSQL advisory lock. The literal value matches the
// pre-refactor postgres.TimerSweeperLockKey so a rolling deploy across
// multiple replicas does not lose the gate.
const timerSweeperLockKey int64 = 0x6C756F6E_676C7473 // "luonglts" low bits

// InstanceDispatcher is satisfied by *instance.Service (injected to avoid
// import cycles between the boundary and instance packages).
type InstanceDispatcher interface {
	// DispatchBoundaryStep spawns targetStepID alongside running work
	// (non-interrupting path). stepExecutionID identifies the parent step the
	// boundary is attached to so the dispatcher can suppress firing when it
	// has already left RUNNING.
	DispatchBoundaryStep(ctx context.Context, instanceID, stepExecutionID, targetStepID string) error
	// InterruptStepAndDispatchBoundary cancels the running step_execution
	// identified by stepExecutionID, cancels its job, then dispatches
	// targetStepID (interrupting path).
	InterruptStepAndDispatchBoundary(ctx context.Context, instanceID, stepExecutionID, targetStepID string) error
}

// StartTimerSweeper runs a background goroutine that fires due boundary
// events. It exits when ctx is cancelled.
func StartTimerSweeper(ctx context.Context, dbConn db.DB, store BoundaryStore, svc InstanceDispatcher, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepTimers(ctx, dbConn, store, svc)
			}
		}
	}()
}

func sweepTimers(ctx context.Context, dbConn db.DB, store BoundaryStore, svc InstanceDispatcher) {
	acquired, release, err := dbConn.TryAcquireAdvisoryLock(ctx, timerSweeperLockKey)
	if err != nil {
		slog.Error("timer sweeper: advisory lock acquire failed", "err", err)
		return
	}
	if !acquired {
		return
	}
	defer release()

	due, err := store.FetchAndMarkFiredBoundaryEvents(ctx)
	if err != nil {
		slog.Error("timer sweeper: fetch failed", "err", err)
		return
	}

	for _, e := range due {
		var dispatchErr error
		if e.Interrupting {
			dispatchErr = svc.InterruptStepAndDispatchBoundary(ctx, e.InstanceID, e.StepExecutionID, e.TargetStepID)
		} else {
			dispatchErr = svc.DispatchBoundaryStep(ctx, e.InstanceID, e.StepExecutionID, e.TargetStepID)
		}
		if dispatchErr != nil {
			slog.Error("timer sweeper: dispatch failed",
				"event_id", e.ID,
				"instance_id", e.InstanceID,
				"target_step_id", e.TargetStepID,
				"interrupting", e.Interrupting,
				"err", dispatchErr,
			)
		} else {
			slog.Info("timer sweeper: fired boundary event",
				"event_id", e.ID,
				"instance_id", e.InstanceID,
				"target_step_id", e.TargetStepID,
				"interrupting", e.Interrupting,
			)
		}
	}
}
