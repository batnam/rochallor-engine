package job

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
)

// Retry re-enqueues a LOCKED job without decrementing retries_remaining.
// This is used for infrastructure-level retries (e.g., worker crash) distinct
// from business-logic retries handled in Fail.
func Retry(ctx context.Context, pool *pgxpool.Pool, d dispatch.Dispatcher, jobID string) error {
	return pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var j dispatch.DispatchJob
		err := tx.QueryRow(ctx,
			`SELECT id, instance_id, step_execution_id, job_type, retries_remaining, payload, created_at
                         FROM job WHERE id = $1 FOR UPDATE`,
			jobID,
		).Scan(&j.ID, &j.InstanceID, &j.StepExecutionID, &j.JobType, &j.RetriesRemaining, &j.Payload, &j.CreatedAt)
		if err != nil {
			return fmt.Errorf("load job for retry: %w", err)
		}

		// Re-enqueue to the configured dispatcher.
		// Polling mode: no-op. kafka_outbox mode: writes outbox row.
		if err := d.Enqueue(ctx, tx, j); err != nil {
			return fmt.Errorf("re-enqueue job for retry: %w", err)
		}

		tag, err := tx.Exec(ctx,
			`UPDATE job
                         SET status = 'UNLOCKED',
                             worker_id = NULL,
                             locked_at = NULL,
                             lock_expires_at = NULL
                         WHERE id = $1 AND status = 'LOCKED'`,
			jobID,
		)
		if err != nil {
			return fmt.Errorf("retry job update %q: %w", jobID, err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("retry: job %q not found or not LOCKED", jobID)
		}
		return nil
	})
}
