package definition

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// SchemaType is the set of primitive types supported by Schema in v1.
// Anything else causes definition upload to be rejected.
type SchemaType string

const (
	SchemaTypeString  SchemaType = "string"
	SchemaTypeNumber  SchemaType = "number"
	SchemaTypeInteger SchemaType = "integer"
	SchemaTypeBoolean SchemaType = "boolean"
)

func (t SchemaType) supported() bool {
	switch t {
	case SchemaTypeString, SchemaTypeNumber, SchemaTypeInteger, SchemaTypeBoolean:
		return true
	}
	return false
}

// PropertyDescriptor declares the expected type of one variable. It is a
// strict subset of JSON Schema's "type" assertion; extra keys are rejected
// at definition upload time.
type PropertyDescriptor struct {
	Type SchemaType `json:"type"`
}

// Schema declares the expected shape of a variable bag. Used for both the
// workflow-level input schema and the service-task-level outputs schema.
// Strict semantics: coercion does NOT apply at schema boundaries.
type Schema struct {
	// Properties maps variable name → type descriptor. Variables not listed
	// here are unrestricted (they pass through).
	Properties map[string]PropertyDescriptor `json:"properties"`
	// Required names that must be present. Names not in Properties cause a
	// definition-upload error.
	Required []string `json:"required,omitempty"`
}

// SchemaViolation describes one failed property check. Multiple violations
// are accumulated by Validate and joined into a single error message at
// the call site.
type SchemaViolation struct {
	Path     string     // variable name (top-level for v1)
	Expected SchemaType // expected primitive type
	Actual   string     // lowercased JSON type of the offending value
	Reason   ViolationReason
}

// ViolationReason discriminates the kind of failure.
type ViolationReason string

const (
	ViolationTypeMismatch    ViolationReason = "type_mismatch"
	ViolationRequiredMissing ViolationReason = "required_missing"
)

// String renders one violation as a single-line message in a stable format.
// Used by SchemaViolationError to construct user-facing error text.
func (v SchemaViolation) String() string {
	switch v.Reason {
	case ViolationRequiredMissing:
		return fmt.Sprintf("required field %q missing", v.Path)
	default:
		return fmt.Sprintf("field %q expected %s, got %s", v.Path, v.Expected, v.Actual)
	}
}

// Validate checks values against the schema and returns ALL violations in
// stable (alphabetical-by-path) order. Empty slice means valid.
//
// Strict rules (per data-model.md):
//   - JSON number, type=number → pass
//   - JSON number with no fractional part, type=integer → pass
//   - JSON string, type=string → pass
//   - JSON boolean, type=boolean → pass
//   - JSON null → fail unless absent-from-required
//   - anything else → fail with expected vs actual
//
// Coercion does NOT apply here. Extra fields are ignored.
func (s *Schema) Validate(values map[string]any) []SchemaViolation {
	if s == nil {
		return nil
	}

	var violations []SchemaViolation

	for _, req := range s.Required {
		if _, present := values[req]; !present {
			violations = append(violations, SchemaViolation{
				Path:   req,
				Reason: ViolationRequiredMissing,
			})
		}
	}

	for name, desc := range s.Properties {
		raw, present := values[name]
		if !present {
			continue // covered by Required check above
		}
		if raw == nil {
			// null is only acceptable if not required (already flagged above
			// when it is). Treat as a type mismatch otherwise.
			if !isRequired(s.Required, name) {
				continue
			}
			violations = append(violations, SchemaViolation{
				Path:     name,
				Expected: desc.Type,
				Actual:   "null",
				Reason:   ViolationTypeMismatch,
			})
			continue
		}
		if ok, actual := matchType(raw, desc.Type); !ok {
			violations = append(violations, SchemaViolation{
				Path:     name,
				Expected: desc.Type,
				Actual:   actual,
				Reason:   ViolationTypeMismatch,
			})
		}
	}

	sort.Slice(violations, func(i, j int) bool { return violations[i].Path < violations[j].Path })
	return violations
}

func isRequired(required []string, name string) bool {
	for _, r := range required {
		if r == name {
			return true
		}
	}
	return false
}

// matchType returns (passes, actualTypeName). actualTypeName is the lowercased
// JSON type name used in violation messages — Go-side integer types are
// normalised to "number" so error messages match the wire format.
func matchType(v any, want SchemaType) (bool, string) {
	switch x := v.(type) {
	case string:
		return want == SchemaTypeString, "string"
	case bool:
		return want == SchemaTypeBoolean, "boolean"
	case float64:
		if want == SchemaTypeNumber {
			return true, "number"
		}
		if want == SchemaTypeInteger {
			if x == math.Trunc(x) && !math.IsInf(x, 0) && !math.IsNaN(x) {
				return true, "number"
			}
		}
		return false, "number"
	case float32:
		f := float64(x)
		if want == SchemaTypeNumber {
			return true, "number"
		}
		if want == SchemaTypeInteger && f == math.Trunc(f) {
			return true, "number"
		}
		return false, "number"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		switch want {
		case SchemaTypeNumber, SchemaTypeInteger:
			return true, "number"
		}
		return false, "number"
	case json.Number:
		s := x.String()
		isInt := !strings.ContainsAny(s, ".eE")
		switch want {
		case SchemaTypeNumber:
			return true, "number"
		case SchemaTypeInteger:
			return isInt, "number"
		}
		return false, "number"
	case []any, []string:
		return false, "array"
	case map[string]any:
		return false, "object"
	}
	return false, fmt.Sprintf("%T", v)
}

// SchemaViolationError is the error type returned when input or output
// schema validation fails. Wraps the violations so that boundary code (gRPC
// handlers) can detect this case and map to InvalidArgument.
type SchemaViolationError struct {
	Violations []SchemaViolation
}

func (e *SchemaViolationError) Error() string {
	parts := make([]string, 0, len(e.Violations))
	for _, v := range e.Violations {
		parts = append(parts, v.String())
	}
	return "schema violation: " + strings.Join(parts, "; ")
}

// IsSchemaViolation reports whether err (or anything it wraps) is a
// *SchemaViolationError. Tests and gRPC handlers use this to discriminate.
func IsSchemaViolation(err error) (*SchemaViolationError, bool) {
	for cur := err; cur != nil; {
		if sv, ok := cur.(*SchemaViolationError); ok {
			return sv, true
		}
		type wrapper interface{ Unwrap() error }
		if w, ok := cur.(wrapper); ok {
			cur = w.Unwrap()
			continue
		}
		return nil, false
	}
	return nil, false
}
