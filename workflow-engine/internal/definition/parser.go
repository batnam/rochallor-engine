package definition

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Parse reads a JSON-encoded WorkflowDefinition from r.
// It returns the parsed definition and any decoding error.
// The Metadata field is preserved verbatim as opaque JSON.
//
// Unknown fields are rejected. Two 005-era field names that were removed in
// the redesign — DECISION_TABLE's `then` (per-rule routing) and
// `defaultNextStep` (table-level fallback) — are surfaced with a
// migration-pointing message so existing 005 callers can self-recover.
func Parse(r io.Reader) (*WorkflowDefinition, error) {
	var def WorkflowDefinition
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&def); err != nil {
		return nil, translateLegacyDecisionTableFieldErr(err)
	}
	return &def, nil
}

// ParseBytes is a convenience wrapper for Parse over a byte slice.
func ParseBytes(data []byte) (*WorkflowDefinition, error) {
	var def WorkflowDefinition
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&def); err != nil {
		return nil, translateLegacyDecisionTableFieldErr(err)
	}
	return &def, nil
}

// translateLegacyDecisionTableFieldErr rewrites the strict decoder's
// "json: unknown field" error for the two 005 DECISION_TABLE field names
// that were removed in 007 (per-rule `then`, table-level `defaultNextStep`).
// Other unknown-field errors pass through unchanged. See
// specs/007-decision-table-outputs/data-model.md §3 (V8, V9).
func translateLegacyDecisionTableFieldErr(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, `unknown field "then"`):
		return fmt.Errorf("parse: step (DECISION_TABLE) rules[]: field \"then\" is no longer supported; see the 005→007 migration note in docs/workflow-format.md")
	case strings.Contains(msg, `unknown field "defaultNextStep"`):
		return fmt.Errorf("parse: step (DECISION_TABLE): field \"defaultNextStep\" is no longer supported; see the 005→007 migration note in docs/workflow-format.md")
	default:
		return fmt.Errorf("parse: %w", err)
	}
}
