package job

import (
	"context"
	"encoding/json"

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
		state, err := store.LockJobExecution(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if !state.AcceptsCallback(workerID) {
			return nil
		}
		_, stepExecID, _, err := store.GetJobForComplete(ctx, tx, jobID)
		if err != nil {
			return err
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

// Fail retires this delivery attempt. If retryable and retries remain, it
// creates a new job for the same step execution. Otherwise it also fails the
// step and instance. Repeated or late callbacks cannot change terminal state.
func Fail(ctx context.Context, dbConn db.DB, store JobStore, d dispatch.Dispatcher, jobID, workerID, errorMessage string, retryable bool) error {
	return dbConn.RunInTx(ctx, "job.fail", func(tx db.Tx) error {
		state, err := store.LockJobExecution(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if !state.AcceptsCallback(workerID) {
			return nil
		}
		j, err := store.GetJobForFail(ctx, tx, jobID)
		if err != nil {
			return err
		}

		if err := store.MarkJobFailed(ctx, tx, jobID, workerID); err != nil {
			return err
		}
		if retryable && j.RetriesRemaining > 0 {
			j.RetriesRemaining--
			return enqueueReplacement(ctx, tx, store, d, j)
		}

		// Non-retryable or retries exhausted — terminal failure.
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
