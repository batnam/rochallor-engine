package job

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
)

// Complete marks a LOCKED job as COMPLETED and marks the corresponding
// step_execution COMPLETED with output. The state transition is idempotent:
// a second call with the same jobID is a no-op (the job will already be COMPLETED).
//
// Note: this entry point is preserved for legacy SDK clients that hit it
// directly. New clients should use instance.Service.CompleteJobAndAdvance,
// which also performs the next-step dispatch.
func Complete(ctx context.Context, dbConn db.DB, store JobStore, _ InstanceAdvancer, jobID, workerID string, variablesToSet map[string]any) error {
	return dbConn.RunInTx(ctx, "job.complete", func(tx db.Tx) error {
		_, stepExecID, _, err := store.GetJobForComplete(ctx, tx, jobID)
		if err != nil {
			return err
		}
		// Idempotency: if already COMPLETED, skip.
		status, _ := store.GetJobStatusForIdempotency(ctx, tx, jobID)
		if status == "COMPLETED" {
			return nil
		}
		if _, err := store.GetStepExecutionStepID(ctx, tx, stepExecID); err != nil {
			return err
		}
		if err := store.MarkJobCompleted(ctx, tx, jobID, workerID); err != nil {
			return err
		}
		outputJSON, _ := json.Marshal(variablesToSet)
		if err := store.MarkStepExecutionCompleted(ctx, tx, stepExecID, outputJSON); err != nil {
			return err
		}
		return nil
	})
}

// Fail records a job failure. If retryable and retries remain, re-enqueues
// the job (UNLOCKED) using the provided dispatcher. Otherwise marks it
// FAILED and transitions the instance to FAILED.
func Fail(ctx context.Context, dbConn db.DB, store JobStore, d dispatch.Dispatcher, jobID, workerID, errorMessage string, retryable bool) error {
	return dbConn.RunInTx(ctx, "job.fail", func(tx db.Tx) error {
		j, err := store.GetJobForFail(ctx, tx, jobID)
		if err != nil {
			return err
		}

		if retryable && j.RetriesRemaining > 0 {
			next := j.RetriesRemaining - 1
			retryJob := j
			retryJob.RetriesRemaining = next
			if err := d.Enqueue(ctx, tx, retryJob); err != nil {
				return fmt.Errorf("re-enqueue job for fail: %w", err)
			}
			if err := store.ReenqueueJob(ctx, tx, jobID, next); err != nil {
				return err
			}
			return nil
		}

		// Non-retryable or retries exhausted — terminal failure.
		if err := store.MarkJobFailed(ctx, tx, jobID, workerID); err != nil {
			return err
		}
		if err := store.MarkStepExecutionFailed(ctx, tx, j.StepExecutionID, errorMessage); err != nil {
			return err
		}
		if err := store.MarkInstanceFailed(ctx, tx, j.InstanceID, errorMessage); err != nil {
			return err
		}
		return nil
	})
}

// InstanceAdvancer is satisfied by *instance.Service — declared here to
// avoid an import cycle between the job and instance packages.
type InstanceAdvancer interface {
	Advance(ctx context.Context, instanceID, stepID, nextStepID string, variablesDelta map[string]any) (interface{}, error)
}
