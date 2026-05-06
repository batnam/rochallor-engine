package job

import (
	"context"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// JobStore is the data-access contract for the job package. All
// transactional methods take a db.Tx; non-transactional reads (PollJobs)
// do not.
type JobStore interface {
	// GetJobForComplete loads the job row with FOR UPDATE for the Complete path.
	GetJobForComplete(ctx context.Context, tx db.Tx, jobID string) (instanceID, stepExecID string, retriesRemaining int, err error)

	// GetJobStatusForIdempotency reads status without locking
	// (idempotency short-circuit for Complete).
	GetJobStatusForIdempotency(ctx context.Context, tx db.Tx, jobID string) (string, error)

	// GetStepExecutionStepID resolves the step_id for a step_execution_id
	// inside the transaction (used by Complete to derive the next-step hint).
	GetStepExecutionStepID(ctx context.Context, tx db.Tx, stepExecID string) (string, error)

	// MarkJobCompleted sets job status='COMPLETED' with worker_id.
	MarkJobCompleted(ctx context.Context, tx db.Tx, jobID, workerID string) error

	// MarkStepExecutionCompleted marks a step_execution COMPLETED with output snapshot.
	MarkStepExecutionCompleted(ctx context.Context, tx db.Tx, stepExecID string, outputSnapshot []byte) error

	// GetJobForFail loads the job row with FOR UPDATE for the Fail / Retry paths.
	// The returned DispatchJob is fully populated (used to re-Enqueue on retry).
	GetJobForFail(ctx context.Context, tx db.Tx, jobID string) (dispatch.DispatchJob, error)

	// MarkJobFailed sets job status='FAILED' with worker_id.
	MarkJobFailed(ctx context.Context, tx db.Tx, jobID, workerID string) error

	// MarkStepExecutionFailed marks a step_execution FAILED with reason.
	MarkStepExecutionFailed(ctx context.Context, tx db.Tx, stepExecID, reason string) error

	// MarkInstanceFailed sets workflow_instance status='FAILED' (terminal job failure).
	MarkInstanceFailed(ctx context.Context, tx db.Tx, instanceID, reason string) error

	// ReenqueueJob resets job to UNLOCKED and decrements retries_remaining
	// (used by Fail when a retry remains).
	ReenqueueJob(ctx context.Context, tx db.Tx, jobID string, newRetriesRemaining int) error

	// UnlockJob resets a LOCKED job to UNLOCKED (used by Retry and the lease
	// sweeper). Does NOT change retries_remaining. Returns rows-affected so
	// callers can detect "job was not LOCKED".
	UnlockJob(ctx context.Context, tx db.Tx, jobID string) (int64, error)

	// GetExpiredLeases returns LOCKED jobs whose lock_expires_at is in the
	// past. Uses FOR UPDATE SKIP LOCKED so concurrent sweeps don't contend.
	GetExpiredLeases(ctx context.Context, tx db.Tx) ([]dispatch.DispatchJob, error)

	// PollJobs atomically claims up to max UNLOCKED jobs of the supplied
	// jobTypes for workerID via UPDATE…FOR UPDATE SKIP LOCKED. Returns the
	// claimed rows. No db.Tx — the entire operation is one SQL statement.
	PollJobs(ctx context.Context, workerID string, jobTypes []string, max int, lockDurationSeconds int) ([]instance.Job, error)
}
