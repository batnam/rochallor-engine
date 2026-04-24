package job

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

// StartLeaseSweeper runs a background goroutine that periodically unlocks
// jobs whose lock_expires_at has passed (worker crash / slow worker).
// It exits when ctx is cancelled.
//
// Across multiple engine replicas the sweep is gated by a PostgreSQL
// advisory lock (LeaseSweeperLockKey) so only one replica sweeps per
// interval (FR-004, SC-004).
func StartLeaseSweeper(ctx context.Context, pool *pgxpool.Pool, d dispatch.Dispatcher, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepExpiredLeases(ctx, pool, d)
			}
		}
	}()
}

func sweepExpiredLeases(ctx context.Context, pool *pgxpool.Pool, d dispatch.Dispatcher) {
	acquired, release, err := postgres.TryAcquireAdvisoryLock(ctx, pool, postgres.LeaseSweeperLockKey)
	if err != nil {
		slog.Error("lease sweeper: advisory lock acquire failed", "err", err)
		return
	}
	if !acquired {
		return
	}
	defer release()

	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
                        SELECT id, instance_id, step_execution_id, job_type, retries_remaining, payload, created_at
                        FROM   job
                        WHERE  status = 'LOCKED'
                        AND    lock_expires_at < now()
                        FOR UPDATE SKIP LOCKED`)
		if err != nil {
			return err
		}
		defer rows.Close()

		var expired []dispatch.DispatchJob
		for rows.Next() {
			var j dispatch.DispatchJob
			if err := rows.Scan(&j.ID, &j.InstanceID, &j.StepExecutionID, &j.JobType, &j.RetriesRemaining, &j.Payload, &j.CreatedAt); err != nil {
				return err
			}
			expired = append(expired, j)
		}

		if len(expired) == 0 {
			return nil
		}

		for _, j := range expired {
			// Re-enqueue to the configured dispatcher (FR-014).
			// Polling mode: no-op. kafka_outbox mode: writes outbox row.
			if err := d.Enqueue(ctx, tx, j); err != nil {
				return fmt.Errorf("re-enqueue job %q: %w", j.ID, err)
			}

			_, err = tx.Exec(ctx, `
                                UPDATE job
                                SET    status = 'UNLOCKED',
                                       worker_id = NULL,
                                       locked_at = NULL,
                                       lock_expires_at = NULL
                                WHERE  id = $1`, j.ID)
			if err != nil {
				return err
			}
		}

		obs.JobTimeoutTotal.Add(float64(len(expired)))
		slog.Info("lease sweeper: reclaimed expired jobs", "count", len(expired))
		return nil
	})

	if err != nil {
		slog.Error("lease sweeper: sweep failed", "err", err)
	}
}
