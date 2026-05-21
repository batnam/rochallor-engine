// Package definition provides the Go representation of the JSON workflow
// definition format. These types match the legacy fixture shape exactly
//
//	so any *.json file from legacy-workflow-engine can be unmarshalled directly into WorkflowDefinition without transformation.
//
// The delegateClass field is preserved as an advisory string — the Engine
// never loads or instantiates it.
package definition

import "encoding/json"

// StepType discriminates the behaviour of a workflow step.
type StepType string

const (
	StepTypeServiceTask     StepType = "SERVICE_TASK"
	StepTypeUserTask        StepType = "USER_TASK"
	StepTypeDecision        StepType = "DECISION"
	StepTypeTransformation  StepType = "TRANSFORMATION"
	StepTypeWait            StepType = "WAIT"
	StepTypeParallelGateway StepType = "PARALLEL_GATEWAY"
	StepTypeJoinGateway     StepType = "JOIN_GATEWAY"
	StepTypeEnd             StepType = "END"
	StepTypeDecisionTable   StepType = "DECISION_TABLE"
)

// BoundaryEventType discriminates boundary-event behaviour.
// Only TIMER is in scope.
type BoundaryEventType string

const (
	BoundaryEventTypeTimer BoundaryEventType = "TIMER"
)

// BoundaryEvent represents an event that can interrupt or run alongside a
// step while it is executing. Only TIMER events appear in the legacy fixtures.
type BoundaryEvent struct {
	// Type is the discriminator. Only "TIMER" is supported.
	Type BoundaryEventType `json:"type"`
	// Duration is an ISO-8601 duration string (e.g. "PT30S").
	// Required for TIMER events.
	Duration string `json:"duration,omitempty"`
	// Interrupting indicates whether the timer cancels the current step.
	// Only false is observed in the fixtures; true is accepted but logs a warning.
	Interrupting bool `json:"interrupting"`
	// TargetStepId is the step the instance advances to when the timer fires.
	TargetStepId string `json:"targetStepId"`
}

// WorkflowStep is a single node in the definition's step graph.
// The Type field is the discriminator; type-specific fields are present
// only for the relevant step type (others are zero values and round-trip
// cleanly via omitempty).
type WorkflowStep struct {
	// Common fields (all step types)
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        StepType `json:"type"`
	Description string   `json:"description,omitempty"`

	// Sequencing
	NextStep          string   `json:"nextStep,omitempty"`
	ParallelNextSteps []string `json:"parallelNextSteps,omitempty"`
	JoinStep          string   `json:"joinStep,omitempty"`

	// DECISION: keys are expressions, values are target step IDs.
	// Insertion order is preserved at JSON decode time, so the engine
	// evaluates expressions in the order the author wrote them (per
	// docs/workflow-format.md → Expression Reference).
	ConditionalNextSteps *ConditionalBranches `json:"conditionalNextSteps,omitempty"`

	// TRANSFORMATION: variable name → literal or ${expression}.
	Transformations map[string]json.RawMessage `json:"transformations,omitempty"`

	// DECISION_TABLE: tabular rule grid. Populated only when Type == StepTypeDecisionTable.
	// The table produces output variables (per rule.Outputs) and unconditionally
	// advances to NextStep (the step-level field above) once a hit policy has
	// determined which matched rules contribute. Routing is the downstream
	// DECISION step's job, not the table's. See specs/007-decision-table-outputs/.
	DecisionTable *DecisionTable `json:"decisionTable,omitempty"`

	// DECISION_TABLE: hit policy code. One of "U", "F", "A", "R", "C",
	// "C+", "C#", "C>", "C<". Omitted/empty defaults to "U" (Unique) at
	// runtime per specs/007-decision-table-outputs/research.md.
	HitPolicy string `json:"hitPolicy,omitempty"`

	// SERVICE_TASK / USER_TASK
	JobType       string `json:"jobType,omitempty"`
	DelegateClass string `json:"delegateClass,omitempty"` // advisory only
	RetryCount    int    `json:"retryCount,omitempty"`

	// Boundary events (SERVICE_TASK, USER_TASK, WAIT)
	BoundaryEvents []BoundaryEvent `json:"boundaryEvents,omitempty"`

	// SERVICE_TASK only: optional schema for the variables the worker is
	// expected to return. Validated at job completion before merge.
	OutputsSchema *Schema `json:"outputs_schema,omitempty"`
}

// DecisionTable is the payload for a DECISION_TABLE step. Rules are
// evaluated against pre-step instance variables; which matched rules
// contribute to the output variables is governed by the step-level
// HitPolicy (one of U, F, A, R, C, C+, C#, C>, C<). After outputs are
// merged into the variable map, the instance unconditionally advances to
// the step-level NextStep. No-match under any policy fails the step with
// the error string "DecisionTableNoRuleMatched"; authors who want
// fallback behaviour write a catch-all rule (empty When map) at the
// bottom of the rules list. See specs/007-decision-table-outputs/
// data-model.md §1.
type DecisionTable struct {
	// Rules is the ordered list of rule rows. Must be non-empty.
	Rules []DecisionTableRule `json:"rules"`
}

// DecisionTableRule is one row of a decision table.
type DecisionTableRule struct {
	// When maps an input column name to a boolean cell expression in the
	// existing expression dialect. Empty/missing cells are wildcards
	// (always match). An empty When map matches everything (catch-all).
	When map[string]string `json:"when,omitempty"`

	// Outputs is an optional map of variable name → JSON literal or
	// "${expression}" string, using the same encoding as
	// TRANSFORMATION.transformations. Whether and how each rule's
	// Outputs contribute to the final variable map depends on the
	// step-level HitPolicy. Rules omitting an output column that other
	// rules in the same match set declare contribute null for that
	// column under R/C policies.
	Outputs map[string]json.RawMessage `json:"outputs,omitempty"`
}

// MarshalJSON is provided so tests can call it directly; encoding/json handles the rest.
func (d *WorkflowDefinition) MarshalJSON() ([]byte, error) {
	type Alias WorkflowDefinition
	return json.Marshal((*Alias)(d))
}

// WorkflowDefinition is the top-level object in every workflow JSON file.
// It is the canonical input to the Engine's definition-upload endpoint and
// the output of the definition-get endpoints.
type WorkflowDefinition struct {
	// ID is the natural key for this definition (e.g. "LOS::loan-application-workflow").
	ID string `json:"id"`
	// Version is 0 on upload (assigned by the Engine) and positive on read.
	Version int `json:"version,omitempty"`
	// Name is the human-readable label.
	Name string `json:"name"`
	// Description is free-form text.
	Description string `json:"description,omitempty"`
	// AutoStartNextWorkflow, when true, causes the Engine to automatically
	// start NextWorkflowId when this definition's instance reaches END.
	AutoStartNextWorkflow bool `json:"autoStartNextWorkflow,omitempty"`
	// NextWorkflowId is required iff AutoStartNextWorkflow == true.
	NextWorkflowId string `json:"nextWorkflowId,omitempty"`
	// Steps is the ordered list of step nodes. The first element is the entry point.
	Steps []WorkflowStep `json:"steps"`
	// Metadata is free-form, stored as opaque JSONB and round-tripped untouched.
	Metadata map[string]json.RawMessage `json:"metadata,omitempty"`
	// InputSchema is an optional declaration of expected starting variables.
	// When set, the engine validates StartInstance.Variables against it and
	// rejects the call on violation.
	InputSchema *Schema `json:"input_schema,omitempty"`
}
