package instance

import "errors"

// Sentinel errors returned by the resume primitives (CompleteUserTaskAndAdvance,
// SignalWaitAndAdvance, and the legacy ULID-based user-task completion path).
// REST / gRPC handlers use errors.Is to map these to HTTP 404 / 409 / 400
// and their gRPC equivalents (NotFound, FailedPrecondition, InvalidArgument).
var (
	// ErrInstanceNotFound — no workflow_instance row for the supplied id.
	ErrInstanceNotFound = errors.New("workflow instance not found")

	// ErrUserTaskNotFound — no OPEN user_task exists for the supplied
	// (instance_id, step_id) pair (either the step id is wrong, or the task
	// was already completed / cancelled by a boundary event).
	ErrUserTaskNotFound = errors.New("user task not found")

	// ErrInstanceTerminal — the instance is in a terminal state
	// (COMPLETED, FAILED, CANCELLED) and cannot accept further mutations.
	ErrInstanceTerminal = errors.New("instance is in a terminal state")

	// ErrWaitStepNotParked — the signalled step is not currently parked on
	// the instance: either its id is not in current_step_ids, its step_execution
	// is not RUNNING, or the definition step type is not WAIT.
	ErrWaitStepNotParked = errors.New("wait step is not currently parked on the instance")

	// ErrStepTypeMismatch — caller used an endpoint whose step type does not
	// match the definition (e.g. signal route on a USER_TASK, or user-task
	// complete route on a WAIT).
	ErrStepTypeMismatch = errors.New("step type does not match the endpoint")
)
