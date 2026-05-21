package definition_test

import (
	"strings"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// Tests for definition.Validate covering the input_schema / outputs_schema
// rules.

func defWithSchemas(input *definition.Schema, stepSchema *definition.Schema, stepType definition.StepType) *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		ID:   "LOS::schema-test",
		Name: "Schema Test",
		Steps: []definition.WorkflowStep{
			{
				ID: "task", Name: "Task", Type: stepType, JobType: "x",
				NextStep:      "end",
				OutputsSchema: stepSchema,
			},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
		InputSchema: input,
	}
}

func TestValidate_InputSchemaHappyPath(t *testing.T) {
	def := defWithSchemas(
		&definition.Schema{
			Properties: map[string]definition.PropertyDescriptor{"amount": {Type: definition.SchemaTypeNumber}},
			Required:   []string{"amount"},
		},
		nil, definition.StepTypeServiceTask,
	)
	if err := definition.Validate(def); err != nil {
		t.Errorf("expected valid def, got: %v", err)
	}
}

func TestValidate_InputSchemaUnsupportedType(t *testing.T) {
	def := defWithSchemas(
		&definition.Schema{Properties: map[string]definition.PropertyDescriptor{"amount": {Type: "decimal"}}},
		nil, definition.StepTypeServiceTask,
	)
	err := definition.Validate(def)
	if err == nil {
		t.Fatal("expected validation error for unsupported type")
	}
	if !strings.Contains(err.Error(), "unsupported type \"decimal\"") {
		t.Errorf("error should cite the unsupported type, got: %v", err)
	}
}

func TestValidate_InputSchemaRequiredMissingFromProperties(t *testing.T) {
	def := defWithSchemas(
		&definition.Schema{
			Properties: map[string]definition.PropertyDescriptor{"amount": {Type: definition.SchemaTypeNumber}},
			Required:   []string{"customer_id"}, // not in properties
		},
		nil, definition.StepTypeServiceTask,
	)
	err := definition.Validate(def)
	if err == nil {
		t.Fatal("expected validation error for required-not-in-properties")
	}
	if !strings.Contains(err.Error(), `required name "customer_id" does not appear in properties`) {
		t.Errorf("error should call out the orphan required name, got: %v", err)
	}
}

func TestValidate_InputSchemaEmptyProperties(t *testing.T) {
	def := defWithSchemas(&definition.Schema{}, nil, definition.StepTypeServiceTask)
	err := definition.Validate(def)
	if err == nil || !strings.Contains(err.Error(), "properties must not be empty") {
		t.Errorf("expected properties-empty error, got: %v", err)
	}
}

func TestValidate_OutputsSchemaOnServiceTaskHappy(t *testing.T) {
	def := defWithSchemas(
		nil,
		&definition.Schema{
			Properties: map[string]definition.PropertyDescriptor{"payment_id": {Type: definition.SchemaTypeString}},
			Required:   []string{"payment_id"},
		},
		definition.StepTypeServiceTask,
	)
	if err := definition.Validate(def); err != nil {
		t.Errorf("expected valid SERVICE_TASK with outputs_schema, got: %v", err)
	}
}

func TestValidate_OutputsSchemaOnDecisionRejected(t *testing.T) {
	// DECISION needs conditionalNextSteps to be valid in other respects; build
	// the def manually to focus on the schema check.
	def := &definition.WorkflowDefinition{
		ID:   "LOS::schema-bad",
		Name: "Schema Bad",
		Steps: []definition.WorkflowStep{
			{
				ID: "branch", Name: "Branch", Type: definition.StepTypeDecision,
				ConditionalNextSteps: definition.NewConditionalBranches("true", "end"),
				OutputsSchema: &definition.Schema{
					Properties: map[string]definition.PropertyDescriptor{"x": {Type: definition.SchemaTypeString}},
				},
			},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
	err := definition.Validate(def)
	if err == nil {
		t.Fatal("expected validation error: outputs_schema on DECISION")
	}
	if !strings.Contains(err.Error(), "outputs_schema is only supported on SERVICE_TASK") {
		t.Errorf("error should mention SERVICE_TASK-only guard, got: %v", err)
	}
}
