// Package definition provides the Go representation of the JSON workflow
// definition format. These types match the legacy fixture shape exactly
// (see data-model §1) so any *.json file from legacy-workflow-engine can
// be unmarshalled directly into WorkflowDefinition without transformation.
//
// The delegateClass field is preserved as an advisory string per R-010 —
// the Engine never loads or instantiates it.
package definition

import "encoding/json"

// StepType discriminates the behaviour of a workflow step.
type StepType string

const (
	StepTypeServiceTask      StepType = "SERVICE_TASK"
	StepTypeUserTask         StepType = "USER_TASK"
	StepTypeDecision         StepType = "DECISION"
	StepTypeTransformation   StepType = "TRANSFORMATION"
	StepTypeWait             StepType = "WAIT"
	StepTypeParallelGateway  StepType = "PARALLEL_GATEWAY"
	StepTypeJoinGateway      StepType = "JOIN_GATEWAY"
	StepTypeEnd              StepType = "END"
)

// BoundaryEventType discriminates boundary-event behaviour.
// Only TIMER is in scope per R-005.
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
	NextStep         string   `json:"nextStep,omitempty"`
	ParallelNextSteps []string `json:"parallelNextSteps,omitempty"`
	JoinStep         string   `json:"joinStep,omitempty"`

	// DECISION: keys are R-003 expressions, values are target step IDs.
	ConditionalNextSteps map[string]string `json:"conditionalNextSteps,omitempty"`

	// TRANSFORMATION: variable name → literal or ${expression}.
	Transformations map[string]json.RawMessage `json:"transformations,omitempty"`

	// SERVICE_TASK / USER_TASK
	JobType       string `json:"jobType,omitempty"`
	DelegateClass string `json:"delegateClass,omitempty"` // advisory only — R-010
	RetryCount    int    `json:"retryCount,omitempty"`

	// Boundary events (SERVICE_TASK, USER_TASK, WAIT)
	BoundaryEvents []BoundaryEvent `json:"boundaryEvents,omitempty"`
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
}
