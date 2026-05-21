package definition_test

import (
	"strings"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

func TestSchema_ValidateAllTypesPass(t *testing.T) {
	s := &definition.Schema{
		Properties: map[string]definition.PropertyDescriptor{
			"name":   {Type: definition.SchemaTypeString},
			"amount": {Type: definition.SchemaTypeNumber},
			"count":  {Type: definition.SchemaTypeInteger},
			"active": {Type: definition.SchemaTypeBoolean},
		},
	}
	vs := s.Validate(map[string]any{
		"name":   "alice",
		"amount": 12.5,
		"count":  3.0,
		"active": true,
	})
	if len(vs) != 0 {
		t.Fatalf("expected no violations, got: %v", vs)
	}
}

func TestSchema_TypeMismatches(t *testing.T) {
	s := &definition.Schema{
		Properties: map[string]definition.PropertyDescriptor{
			"name":   {Type: definition.SchemaTypeString},
			"amount": {Type: definition.SchemaTypeNumber},
			"active": {Type: definition.SchemaTypeBoolean},
		},
	}
	vs := s.Validate(map[string]any{
		"name":   123,    // number, expected string
		"amount": "10",   // string, expected number
		"active": "true", // string, expected boolean (no coercion!)
	})
	if len(vs) != 3 {
		t.Fatalf("expected 3 violations, got %d: %v", len(vs), vs)
	}
	// Verify stable alphabetical order
	wantOrder := []string{"active", "amount", "name"}
	for i, v := range vs {
		if v.Path != wantOrder[i] {
			t.Errorf("violation %d: want path %q, got %q", i, wantOrder[i], v.Path)
		}
		if v.Reason != definition.ViolationTypeMismatch {
			t.Errorf("violation %d: want type_mismatch, got %s", i, v.Reason)
		}
	}
}

func TestSchema_RequiredMissing(t *testing.T) {
	s := &definition.Schema{
		Properties: map[string]definition.PropertyDescriptor{
			"amount":      {Type: definition.SchemaTypeNumber},
			"customer_id": {Type: definition.SchemaTypeString},
		},
		Required: []string{"amount", "customer_id"},
	}
	vs := s.Validate(map[string]any{"amount": 100.0}) // customer_id missing
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(vs), vs)
	}
	v := vs[0]
	if v.Path != "customer_id" {
		t.Errorf("want path customer_id, got %s", v.Path)
	}
	if v.Reason != definition.ViolationRequiredMissing {
		t.Errorf("want required_missing, got %s", v.Reason)
	}
}

func TestSchema_MultipleViolationsBothKinds(t *testing.T) {
	s := &definition.Schema{
		Properties: map[string]definition.PropertyDescriptor{
			"amount":      {Type: definition.SchemaTypeNumber},
			"customer_id": {Type: definition.SchemaTypeString},
		},
		Required: []string{"amount", "customer_id"},
	}
	vs := s.Validate(map[string]any{"amount": "abc"})
	if len(vs) != 2 {
		t.Fatalf("expected 2 violations, got %d: %v", len(vs), vs)
	}
	err := &definition.SchemaViolationError{Violations: vs}
	msg := err.Error()
	if !strings.Contains(msg, `field "amount" expected number, got string`) {
		t.Errorf("missing type_mismatch text: %s", msg)
	}
	if !strings.Contains(msg, `required field "customer_id" missing`) {
		t.Errorf("missing required_missing text: %s", msg)
	}
}

func TestSchema_IntegerAcceptsWholeFloat(t *testing.T) {
	s := &definition.Schema{
		Properties: map[string]definition.PropertyDescriptor{
			"count": {Type: definition.SchemaTypeInteger},
		},
	}
	if vs := s.Validate(map[string]any{"count": 3.0}); len(vs) != 0 {
		t.Errorf("whole-float 3.0 must pass integer; got: %v", vs)
	}
	if vs := s.Validate(map[string]any{"count": 3.5}); len(vs) != 1 {
		t.Errorf("fractional 3.5 must fail integer; got: %v", vs)
	}
}

func TestSchema_NullOnOptionalIsAllowed(t *testing.T) {
	s := &definition.Schema{
		Properties: map[string]definition.PropertyDescriptor{
			"note": {Type: definition.SchemaTypeString},
		},
	}
	if vs := s.Validate(map[string]any{"note": nil}); len(vs) != 0 {
		t.Errorf("null on optional property must pass; got: %v", vs)
	}
}

func TestSchema_NullOnRequiredFails(t *testing.T) {
	s := &definition.Schema{
		Properties: map[string]definition.PropertyDescriptor{
			"note": {Type: definition.SchemaTypeString},
		},
		Required: []string{"note"},
	}
	vs := s.Validate(map[string]any{"note": nil})
	if len(vs) != 1 {
		t.Fatalf("null on required must fail; got: %v", vs)
	}
	if vs[0].Actual != "null" {
		t.Errorf("expected actual=null, got %q", vs[0].Actual)
	}
}

func TestSchema_ExtraFieldsAreIgnored(t *testing.T) {
	s := &definition.Schema{
		Properties: map[string]definition.PropertyDescriptor{
			"a": {Type: definition.SchemaTypeString},
		},
	}
	if vs := s.Validate(map[string]any{"a": "x", "b": "extra", "c": 42.0}); len(vs) != 0 {
		t.Errorf("extra fields must be ignored; got: %v", vs)
	}
}

func TestSchema_ArrayAndObjectTypeMismatches(t *testing.T) {
	s := &definition.Schema{
		Properties: map[string]definition.PropertyDescriptor{
			"x": {Type: definition.SchemaTypeNumber},
		},
	}
	vs := s.Validate(map[string]any{"x": []any{1, 2}})
	if len(vs) != 1 || vs[0].Actual != "array" {
		t.Errorf("array operand must be reported as array; got: %v", vs)
	}
	vs = s.Validate(map[string]any{"x": map[string]any{"y": 1}})
	if len(vs) != 1 || vs[0].Actual != "object" {
		t.Errorf("object operand must be reported as object; got: %v", vs)
	}
}

func TestSchema_NilReceiverIsNoOp(t *testing.T) {
	var s *definition.Schema
	if vs := s.Validate(map[string]any{"x": 1}); vs != nil {
		t.Errorf("nil schema must return nil; got: %v", vs)
	}
}

func TestSchemaViolationError_IsSchemaViolation(t *testing.T) {
	err := &definition.SchemaViolationError{
		Violations: []definition.SchemaViolation{{Path: "x", Expected: "number", Actual: "string", Reason: definition.ViolationTypeMismatch}},
	}
	got, ok := definition.IsSchemaViolation(err)
	if !ok || got != err {
		t.Errorf("IsSchemaViolation must return (err, true); got (%v, %v)", got, ok)
	}
	if _, ok := definition.IsSchemaViolation(nil); ok {
		t.Errorf("IsSchemaViolation(nil) must return false")
	}
}
