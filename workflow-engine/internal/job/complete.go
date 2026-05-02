package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
)

// Complete marks a LOCKED job as COMPLETED and merges variablesToSet into the
// parent instance's variables, then calls svc.Advance to continue execution.
// The entire state transition is idempotent: a second call with the same jobID
// is a no-op (the job will already be COMPLETED).
func Complete(ctx context.Context, pool *pgxpool.Pool, svc InstanceAdvancer, jobID, workerID string, variablesToSet map[string]any) error {
	return pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// Load the job — must be LOCKED by this worker
		var instanceID, stepExecID, nextStepHint string
		var retriesRemaining int
		err := tx.QueryRow(ctx,
			`SELECT instance_id, step_execution_id, retries_remaining
                          FROM job WHERE id = $1 FOR UPDATE`,
			jobID,
		).Scan(&instanceID, &stepExecID, &retriesRemaining)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("job %q not found", jobID)
			}
			return fmt.Errorf("load job for complete: %w", err)
		}

		// Idempotency: if already COMPLETED, skip
		var status string
		_ = tx.QueryRow(ctx, `SELECT status FROM job WHERE id = $1`, jobID).Scan(&status)
		if status == "COMPLETED" {
			return nil
		}

		// Fetch the step_id from step_execution so we can advance
		err = tx.QueryRow(ctx,
			`SELECT step_id FROM step_execution WHERE id = $1`,
			stepExecID,
		).Scan(&nextStepHint)
		if err != nil {
			return fmt.Errorf("load step_execution for complete: %w", err)
		}

		// Mark job COMPLETED
		if _, err := tx.Exec(ctx,
			`UPDATE job SET status = 'COMPLETED', worker_id = $1 WHERE id = $2`,
			workerID, jobID,
		); err != nil {
			return fmt.Errorf("mark job completed: %w", err)
		}

		// Mark step_execution COMPLETED with output
		outputJSON, _ := json.Marshal(variablesToSet)
		if _, err := tx.Exec(ctx,
			`UPDATE step_execution SET status = 'COMPLETED', ended_at = now(), output_snapshot = $1 WHERE id = $2`,
			outputJSON, stepExecID,
		); err != nil {
			return fmt.Errorf("mark step_execution completed: %w", err)
		}

		return nil
	})
}

// Fail records a job failure. If retryable and retries remain, re-enqueues the
// job (UNLOCKED) using the provided dispatcher. Otherwise marks it FAILED and
// transitions the instance to FAILED.
func Fail(ctx context.Context, pool *pgxpool.Pool, d dispatch.Dispatcher, jobID, workerID, errorMessage string, retryable bool) error {
	return pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var instanceID, stepExecID, jobType string
		var retriesRemaining int
		var payload []byte
		var createdAt time.Time
		err := tx.QueryRow(ctx,
			`SELECT instance_id, step_execution_id, job_type, retries_remaining, payload, created_at FROM job WHERE id = $1 FOR UPDATE`,
			jobID,
		).Scan(&instanceID, &stepExecID, &jobType, &retriesRemaining, &payload, &createdAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("job %q not found", jobID)
			}
			return fmt.Errorf("load job for fail: %w", err)
		}

		if retryable && retriesRemaining > 0 {
			// Re-enqueue
			if err := d.Enqueue(ctx, tx, dispatch.DispatchJob{
				ID:               jobID,
				InstanceID:       instanceID,
				StepExecutionID:  stepExecID,
				JobType:          jobType,
				RetriesRemaining: retriesRemaining - 1,
				Payload:          payload,
				CreatedAt:        createdAt,
			}); err != nil {
				return fmt.Errorf("re-enqueue job for fail: %w", err)
			}

			if _, err := tx.Exec(ctx,
				`UPDATE job SET status = 'UNLOCKED', worker_id = NULL, locked_at = NULL, lock_expires_at = NULL,
                                       retries_remaining = retries_remaining - 1 WHERE id = $1`,
				jobID,
			); err != nil {
				return fmt.Errorf("re-enqueue job update: %w", err)
			}
			return nil
		}

		// Non-retryable or retries exhausted — terminal failure
		if _, err := tx.Exec(ctx,
			`UPDATE job SET status = 'FAILED', worker_id = $1 WHERE id = $2`,
			workerID, jobID,
		); err != nil {
			return fmt.Errorf("mark job failed: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE step_execution SET status = 'FAILED', ended_at = now(), failure_reason = $1 WHERE id = $2`,
			errorMessage, stepExecID,
		); err != nil {
			return fmt.Errorf("mark step_execution failed: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE workflow_instance SET status = 'FAILED', failure_reason = $1, completed_at = now() WHERE id = $2`,
			errorMessage, instanceID,
		); err != nil {
			return fmt.Errorf("mark instance failed: %w", err)
		}
		return nil
	})
}

// InstanceAdvancer is satisfied by *instance.Service — declared here to avoid
// an import cycle between the job and instance packages.
type InstanceAdvancer interface {
	Advance(ctx context.Context, instanceID, stepID, nextStepID string, variablesDelta map[string]any) (interface{}, error)
}
