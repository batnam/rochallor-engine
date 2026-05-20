package instance

import (
	"context"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
)

// Store is the data-access contract for the instance package.
//
// Methods that mutate state take a db.Tx (the caller has already opened a
// transaction via db.DB.RunInTx). Read-only queries do not take a Tx — they
// run on the underlying pool. Implementations are expected to record
// observability metrics (lock-wait duration on FOR UPDATE statements,
// transaction duration via db.DB.RunInTx) so domain code stays free of
// instrumentation concerns.
type Store interface {
	// ─── non-transactional reads ─────────────────────────────────────────────

	// GetInstanceDefinitionInfo returns the (definition_id, definition_version)
	// pair for an instance. Used as a pre-tx peek so the FOR UPDATE lock in
	// the subsequent transaction is held only for the state-write window.
	GetInstanceDefinitionInfo(ctx context.Context, instanceID string) (defID string, defVersion int, err error)

	// GetInstance returns a full WorkflowInstance row by id.
	GetInstance(ctx context.Context, instanceID string) (*WorkflowInstance, error)

	// ListInstances returns a paginated, optionally-filtered list of instances
	// (definitionID, status, businessKey are AND-combined; empty strings are
	// ignored) plus the total matching count for the same filter.
	ListInstances(ctx context.Context, definitionID, status, businessKey string, page, pageSize int) (ListResult, error)

	// GetHistory returns all step_execution rows for an instance ordered by
	// started_at ascending.
	GetHistory(ctx context.Context, instanceID string) ([]StepExecution, error)

	// CancelInstance transitions an ACTIVE/WAITING instance to CANCELLED in a
	// single UPDATE...RETURNING. Returns ErrInstanceNotFound (or pgx.ErrNoRows)
	// if no row matched the eligibility predicate.
	CancelInstance(ctx context.Context, instanceID, reason string) (*WorkflowInstance, error)

	// GetJobInstanceAndStepExec resolves the instance_id and step_execution_id
	// of a job by id. Used by CompleteJobAndAdvance before opening a tx.
	GetJobInstanceAndStepExec(ctx context.Context, jobID string) (instanceID, stepExecID string, err error)

	// GetStepExecutionStepIDByID resolves the step_id for a step_execution_id
	// (non-transactional, used in the pre-tx peek window).
	GetStepExecutionStepIDByID(ctx context.Context, stepExecID string) (string, error)

	// ─── transactional writes ────────────────────────────────────────────────

	// InsertInstance inserts a new workflow_instance row and returns the
	// persisted record (started_at populated by the database).
	InsertInstance(ctx context.Context, tx db.Tx,
		id, defID string, defVersion int, status InstanceStatus,
		currentStepIDs []string, variables []byte, businessKey *string,
	) (*WorkflowInstance, error)

	// GetInstanceForUpdate returns a workflow_instance row locked FOR UPDATE.
	// Implementations MUST wrap the query in obs.DBLockWaitDuration via
	// ObserveLockWait("instance.for_update", ...).
	GetInstanceForUpdate(ctx context.Context, tx db.Tx, instanceID string) (*WorkflowInstance, error)

	// UpdateInstanceCurrentSteps persists a new current_step_ids value.
	UpdateInstanceCurrentSteps(ctx context.Context, tx db.Tx, instanceID string, stepIDs []string) error

	// UpdateInstanceStatus persists a new status value (no current_step_ids change).
	UpdateInstanceStatus(ctx context.Context, tx db.Tx, instanceID string, status InstanceStatus) error

	// UpdateInstanceStatusAndSteps persists status and current_step_ids atomically.
	UpdateInstanceStatusAndSteps(ctx context.Context, tx db.Tx, instanceID string, status InstanceStatus, stepIDs []string) error

	// CompleteInstance sets status=COMPLETED, completed_at=now(), and the
	// supplied current_step_ids (typically empty after handleEnd).
	CompleteInstance(ctx context.Context, tx db.Tx, instanceID string, stepIDs []string) error

	// FailInstance sets status=FAILED with the supplied reason and completed_at=now().
	FailInstance(ctx context.Context, tx db.Tx, instanceID, reason string) error

	// UpdateInstanceVariablesPartial applies a chained jsonb_set per top-level
	// key in patch (only the changed keys travel over the wire). No-op for an
	// empty patch.
	UpdateInstanceVariablesPartial(ctx context.Context, tx db.Tx, instanceID string, patch map[string]any) error

	// CountStepAttempts returns the number of existing step_execution rows for
	// the (instanceID, stepID) pair. Used to derive the next attempt_number.
	CountStepAttempts(ctx context.Context, tx db.Tx, instanceID, stepID string) (int, error)

	// InsertStepExecution creates a RUNNING step_execution row with the
	// supplied input_snapshot.
	InsertStepExecution(ctx context.Context, tx db.Tx,
		id, instanceID, stepID, stepType string, attempt int, inputSnapshot []byte,
	) error

	// CompleteStepExecutionByID marks a step_execution COMPLETED (with optional
	// output_snapshot — pass nil to leave NULL).
	CompleteStepExecutionByID(ctx context.Context, tx db.Tx, stepExecID string, outputSnapshot []byte) error

	// CompleteStepExecutionByStep marks the RUNNING step_execution for
	// (instanceID, stepID) COMPLETED with output_snapshot. Returns rows-affected
	// (0 = idempotent race, caller may treat as ErrWaitStepNotParked).
	CompleteStepExecutionByStep(ctx context.Context, tx db.Tx, instanceID, stepID string, outputSnapshot []byte) (int64, error)

	// CompleteStepExecutionByStepNoOutput marks the RUNNING step_execution for
	// (instanceID, stepID) COMPLETED without touching output_snapshot. Used by
	// DECISION and PARALLEL_GATEWAY which produce no per-step output payload.
	CompleteStepExecutionByStepNoOutput(ctx context.Context, tx db.Tx, instanceID, stepID string) error

	// FailStepExecutionByID marks a step_execution FAILED by its id.
	FailStepExecutionByID(ctx context.Context, tx db.Tx, stepExecID, reason string) error

	// FailStepExecutionByStep marks the RUNNING step_execution for
	// (instanceID, stepID) FAILED.
	FailStepExecutionByStep(ctx context.Context, tx db.Tx, instanceID, stepID, reason string) error

	// GetStepExecutionStepID returns the step_id for a step_execution_id
	// (transactional variant).
	GetStepExecutionStepID(ctx context.Context, tx db.Tx, stepExecID string) (string, error)

	// GetStepExecutionStatusByID returns the lifecycle status of a
	// step_execution by id (transactional). Used by the boundary dispatch path
	// to suppress firing when the parent step has already left RUNNING.
	GetStepExecutionStatusByID(ctx context.Context, tx db.Tx, stepExecID string) (StepExecutionStatus, error)

	// InsertJob creates an UNLOCKED job row with the supplied payload.
	InsertJob(ctx context.Context, tx db.Tx,
		id, instanceID, stepExecID, jobType string, retriesRemaining int, payload []byte,
	) error

	// GetJobStatusForUpdate reads job status with FOR UPDATE (idempotency guard
	// in CompleteJobAndAdvance).
	GetJobStatusForUpdate(ctx context.Context, tx db.Tx, jobID string) (string, error)

	// MarkJobCompleted sets status='COMPLETED' and worker_id by job id.
	MarkJobCompleted(ctx context.Context, tx db.Tx, jobID, workerID string) error

	// CancelJobByStepExecution sets status='CANCELLED' on PENDING/UNLOCKED/LOCKED
	// jobs for the given step_execution_id. Used by interrupting boundary timers.
	CancelJobByStepExecution(ctx context.Context, tx db.Tx, stepExecID string) error

	// InsertUserTask creates an OPEN user_task row.
	InsertUserTask(ctx context.Context, tx db.Tx,
		id, instanceID, stepExecID, stepID string, payload []byte,
	) error

	// CompleteUserTask marks the OPEN user_task for (instanceID, stepID)
	// COMPLETED with the supplied result. Returns rows-affected so callers can
	// detect concurrent completion (zero ⇒ ErrUserTaskNotFound).
	CompleteUserTask(ctx context.Context, tx db.Tx, instanceID, stepID string, result []byte) (int64, error)

	// CancelUserTaskByStepExecution marks the user_task CANCELLED (interrupting
	// boundary path). No-op if no row matches.
	CancelUserTaskByStepExecution(ctx context.Context, tx db.Tx, stepExecID string) error

	// InsertBoundaryEventSchedule creates a boundary_event_schedule row.
	InsertBoundaryEventSchedule(ctx context.Context, tx db.Tx,
		id, instanceID, stepExecID, targetStepID string,
		fireAt time.Time, interrupting bool,
	) error

	// CountCompletedBranchLeafs counts distinct COMPLETED step_ids from the
	// supplied set on the given instance. Used by handleJoinGateway.
	CountCompletedBranchLeafs(ctx context.Context, tx db.Tx, instanceID string, leafStepIDs []string) (int, error)

	// GetLatestStepExecutionStatus returns the status of the most recent
	// step_execution row for (instanceID, stepID), ordered by attempt_number
	// desc. Returns ErrStepNotRetryable if no row exists for the pair.
	// Used by RetryFailedStep to validate the target step actually failed.
	GetLatestStepExecutionStatus(ctx context.Context, tx db.Tx, instanceID, stepID string) (StepExecutionStatus, error)

	// ReactivateInstance flips a FAILED instance back to ACTIVE, clearing
	// failure_reason and completed_at. Used by RetryFailedStep. Returns
	// ErrBusinessKeyConflict if the partial unique index on
	// (business_key, definition_id) WHERE status IN ('ACTIVE','WAITING')
	// rejects the transition because another in-flight instance already
	// holds the same business_key.
	ReactivateInstance(ctx context.Context, tx db.Tx, instanceID string) error
}
