package instance

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// Tests for the input/output schema hooks. These exercise the Service layer
// with the in-memory mockStore.

// Service.Start validates against def.InputSchema.

func TestStart_InputSchemaValid_InstanceCreated(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "us2-ok", Version: 1,
		InputSchema: &definition.Schema{
			Properties: map[string]definition.PropertyDescriptor{"amount": {Type: definition.SchemaTypeNumber}},
			Required:   []string{"amount"},
		},
		Steps: []definition.WorkflowStep{{ID: "end", Type: definition.StepTypeEnd}},
	}
	svc, store, _, _ := newServiceForTest(def)

	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"amount": 100.0}, "")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if _, ok := store.instances[inst.ID]; !ok {
		t.Errorf("instance not persisted")
	}
}

func TestStart_InputSchemaTypeMismatch_NoInstancePersisted(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "us2-bad-type", Version: 1,
		InputSchema: &definition.Schema{
			Properties: map[string]definition.PropertyDescriptor{"amount": {Type: definition.SchemaTypeNumber}},
		},
		Steps: []definition.WorkflowStep{{ID: "end", Type: definition.StepTypeEnd}},
	}
	svc, store, _, _ := newServiceForTest(def)

	_, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"amount": "abc"}, "")
	if err == nil {
		t.Fatal("expected SchemaViolationError, got nil")
	}
	sv, ok := definition.IsSchemaViolation(err)
	if !ok {
		t.Fatalf("expected *SchemaViolationError, got %T: %v", err, err)
	}
	if !strings.Contains(sv.Error(), `field "amount" expected number, got string`) {
		t.Errorf("error message: %s", sv.Error())
	}
	if len(store.instances) != 0 {
		t.Errorf("instance was persisted despite validation failure; got %d instances", len(store.instances))
	}
}

func TestStart_InputSchemaRequiredMissing_ReportsAll(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "us2-missing", Version: 1,
		InputSchema: &definition.Schema{
			Properties: map[string]definition.PropertyDescriptor{
				"amount":      {Type: definition.SchemaTypeNumber},
				"customer_id": {Type: definition.SchemaTypeString},
			},
			Required: []string{"amount", "customer_id"},
		},
		Steps: []definition.WorkflowStep{{ID: "end", Type: definition.StepTypeEnd}},
	}
	svc, _, _, _ := newServiceForTest(def)

	_, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, "")
	var sv *definition.SchemaViolationError
	if !errors.As(err, &sv) {
		t.Fatalf("expected *SchemaViolationError, got %T: %v", err, err)
	}
	if len(sv.Violations) != 2 {
		t.Errorf("expected 2 violations (both required), got %d: %v", len(sv.Violations), sv.Violations)
	}
}

func TestStart_NoInputSchema_BehavesAsBefore(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "us2-no-schema", Version: 1,
		Steps: []definition.WorkflowStep{{ID: "end", Type: definition.StepTypeEnd}},
	}
	svc, store, _, _ := newServiceForTest(def)

	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"anything": "goes"}, ""); err != nil {
		t.Fatalf("expected success with no input_schema, got: %v", err)
	}
	if len(store.instances) != 1 {
		t.Errorf("expected 1 instance, got %d", len(store.instances))
	}
}
