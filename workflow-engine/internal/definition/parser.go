package definition

import (
	"encoding/json"
	"fmt"
	"io"
)

// Parse reads a JSON-encoded WorkflowDefinition from r.
// It returns the parsed definition and any decoding error.
// The Metadata field is preserved verbatim as opaque JSON.
func Parse(r io.Reader) (*WorkflowDefinition, error) {
	var def WorkflowDefinition
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&def); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return &def, nil
}

// ParseBytes is a convenience wrapper for Parse over a byte slice.
func ParseBytes(data []byte) (*WorkflowDefinition, error) {
	var def WorkflowDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return &def, nil
}
