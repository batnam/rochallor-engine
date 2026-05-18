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

	// ErrBusinessKeyConflict — another in-flight instance (ACTIVE or WAITING)
	// of the same definition already holds this business_key. Enforced by the
	// partial UNIQUE index (business_key, definition_id) WHERE status IN
	// ('ACTIVE','WAITING') added in migration 0010. Mapped to HTTP 409 /
	// gRPC ALREADY_EXISTS.
	ErrBusinessKeyConflict = errors.New("business key already in use by an in-flight instance of this definition")
)

// Decision-table runtime failure prefixes.
//
// These are NOT sentinel errors — they are the leading tokens of the human
// readable message strings passed to failInstance() when a DECISION_TABLE
// step fails at evaluation time. Lifted here as named constants so unit
// tests in handlers_unit_test.go can assert on the prefix without
// duplicating literal strings across files.
//
// See specs/007-decision-table-outputs/data-model.md §4 for the contract.
const (
	// DecisionTableNoRuleMatched — zero rules matched under any hit
	// policy. Carried over from the 005 design.
	DecisionTableNoRuleMatched = "DecisionTableNoRuleMatched"

	// DecisionTableCellError — a rule's `when` cell expression raised an
	// evaluator error or returned a non-bool value. Carried over from
	// the 005 design.
	DecisionTableCellError = "DecisionTableCellError"

	// DecisionTableOutputError — a rule's `outputs` value failed to
	// unmarshal as JSON, or its `${expression}` failed to evaluate.
	// Carried over from the 005 design.
	DecisionTableOutputError = "DecisionTableOutputError"

	// DecisionTableUniqueViolation — hit policy "U" and two or more
	// rules matched the same input vector. New in 007. The message
	// names the matching rule indices and the policy.
	DecisionTableUniqueViolation = "DecisionTableUniqueViolation"

	// DecisionTableAnyConflict — hit policy "A" and the matching rules
	// produced disagreeing outputs on at least one column. New in 007.
	// The message names the disagreeing column(s) and conflicting
	// values.
	DecisionTableAnyConflict = "DecisionTableAnyConflict"

	// DecisionTableAggregatorTypeError — a Collect aggregator (+, >, <)
	// was applied to a non-numeric output value. New in 007. The
	// message names the offending column and value. The "#" (count)
	// aggregator is type-agnostic and never raises this error.
	DecisionTableAggregatorTypeError = "DecisionTableAggregatorTypeError"
)
