// Package definition re-exports the public surface of the workflow definition
// types for use by external modules (workflow-sdk-go, etc.)
// that cannot access the internal package.
package definition

import (
	"io"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// Re-export types.
type (
	WorkflowDefinition = definition.WorkflowDefinition
	WorkflowStep       = definition.WorkflowStep
	BoundaryEvent      = definition.BoundaryEvent
	StepType           = definition.StepType
	BoundaryEventType  = definition.BoundaryEventType
)

// Re-export constants.
const (
	StepTypeServiceTask     = definition.StepTypeServiceTask
	StepTypeUserTask        = definition.StepTypeUserTask
	StepTypeDecision        = definition.StepTypeDecision
	StepTypeTransformation  = definition.StepTypeTransformation
	StepTypeWait            = definition.StepTypeWait
	StepTypeParallelGateway = definition.StepTypeParallelGateway
	StepTypeJoinGateway     = definition.StepTypeJoinGateway
	StepTypeEnd             = definition.StepTypeEnd

	BoundaryEventTypeTimer = definition.BoundaryEventTypeTimer
)

// Parse delegates to internal/definition.Parse.
func Parse(r io.Reader) (*WorkflowDefinition, error) {
	return definition.Parse(r)
}

// ParseBytes delegates to internal/definition.ParseBytes.
func ParseBytes(data []byte) (*WorkflowDefinition, error) {
	return definition.ParseBytes(data)
}

// Validate delegates to internal/definition.Validate.
func Validate(def *WorkflowDefinition) error {
	return definition.Validate(def)
}
