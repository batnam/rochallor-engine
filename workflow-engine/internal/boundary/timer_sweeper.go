// Package boundary implements the background TIMER boundary-event sweeper.
package boundary

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

// InstanceDispatcher is satisfied by *instance.Service (injected to avoid
// import cycles between the boundary and instance packages).
type InstanceDispatcher interface {
	// DispatchBoundaryStep routes an instance to targetStepID outside a
	// normal job-complete flow (non-interrupting timer path).
	DispatchBoundaryStep(ctx context.Context, instanceID, targetStepID string) error
}

// StartTimerSweeper runs a background goroutine that fires due boundary events.
// It exits when ctx is cancelled.
func StartTimerSweeper(ctx context.Context, pool *pgxpool.Pool, svc InstanceDispatcher, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepTimers(ctx, pool, svc)
			}
		}
	}()
}

type dueEvent struct {
	id           string
	instanceID   string
	targetStepID string
}

func sweepTimers(ctx context.Context, pool *pgxpool.Pool, svc InstanceDispatcher) {
	acquired, release, err := postgres.TryAcquireAdvisoryLock(ctx, pool, postgres.TimerSweeperLockKey)
	if err != nil {
		slog.Error("timer sweeper: advisory lock acquire failed", "err", err)
		return
	}
	if !acquired {
		return
	}
	defer release()

	rows, err := pool.Query(ctx, `
		UPDATE boundary_event_schedule
		SET    fired = true
		WHERE  fired = false
		AND    fire_at <= now()
		RETURNING id, instance_id, target_step_id`)
	if err != nil {
		slog.Error("timer sweeper: query failed", "err", err)
		return
	}
	defer rows.Close()

	var due []dueEvent
	for rows.Next() {
		var e dueEvent
		if err := rows.Scan(&e.id, &e.instanceID, &e.targetStepID); err != nil {
			slog.Error("timer sweeper: scan failed", "err", err)
			return
		}
		due = append(due, e)
	}
	if err := rows.Err(); err != nil {
		slog.Error("timer sweeper: rows error", "err", err)
		return
	}

	for _, e := range due {
		if err := svc.DispatchBoundaryStep(ctx, e.instanceID, e.targetStepID); err != nil {
			slog.Error("timer sweeper: dispatch failed",
				"event_id", e.id,
				"instance_id", e.instanceID,
				"target_step_id", e.targetStepID,
				"err", err,
			)
		} else {
			slog.Info("timer sweeper: fired boundary event",
				"event_id", e.id,
				"instance_id", e.instanceID,
				"target_step_id", e.targetStepID,
			)
		}
	}
}
