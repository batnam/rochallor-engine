package definition

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ConditionalBranches is the DECISION step's expression→target-step map,
// preserving JSON insertion order so the engine evaluates expressions in the
// document order the author wrote (see docs/workflow-format.md, Expression
// Reference: "Expressions are evaluated in the order they appear in the JSON
// object").
//
// The wire format is unchanged — a JSON object {expr: target, ...}. Order is
// captured at unmarshal time via Exprs and reproduced on marshal.
type ConditionalBranches struct {
	// Exprs is the slice of expressions in JSON insertion order.
	Exprs []string
	// Targets maps each expression to its target step ID.
	Targets map[string]string
}

// Len reports the number of branches.
func (c *ConditionalBranches) Len() int {
	if c == nil {
		return 0
	}
	return len(c.Exprs)
}

// UnmarshalJSON decodes a JSON object {expr: target, ...} while remembering
// the document order of keys.
func (c *ConditionalBranches) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("conditionalNextSteps: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("conditionalNextSteps: expected JSON object, got %v", tok)
	}
	c.Exprs = nil
	c.Targets = map[string]string{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("conditionalNextSteps: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("conditionalNextSteps: expected string key, got %T", keyTok)
		}
		var val string
		if err := dec.Decode(&val); err != nil {
			return fmt.Errorf("conditionalNextSteps[%q]: %w", key, err)
		}
		c.Exprs = append(c.Exprs, key)
		c.Targets[key] = val
	}
	return nil
}

// MarshalJSON emits the JSON object {expr: target, ...} in the order
// recorded in Exprs, so round-trip preserves order. HTML escaping is
// disabled because expressions contain `<`, `>`, `&` (e.g. `x >= 100`,
// `a && b`) — escaping them as `<` etc. would round-trip-safe but
// hurt log readability and diff hygiene.
func (c *ConditionalBranches) MarshalJSON() ([]byte, error) {
	if c == nil || len(c.Exprs) == 0 {
		return []byte("{}"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range c.Exprs {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := jsonEncodeNoHTMLEscape(key)
		if err != nil {
			return nil, err
		}
		v, err := jsonEncodeNoHTMLEscape(c.Targets[key])
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// jsonEncodeNoHTMLEscape marshals v without HTML-escaping `<`, `>`, `&`.
// json.Marshal always escapes those; only json.Encoder with
// SetEscapeHTML(false) does not.
func jsonEncodeNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encoder.Encode appends a trailing newline; strip it.
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}

// NewConditionalBranches builds a ConditionalBranches from alternating
// expression and target arguments (expr1, target1, expr2, target2, ...).
// Intended for tests and programmatic construction; uploaded JSON
// definitions use the canonical UnmarshalJSON path.
func NewConditionalBranches(pairs ...string) *ConditionalBranches {
	if len(pairs)%2 != 0 {
		panic("NewConditionalBranches: pairs must have even length (expr1, target1, expr2, target2, ...)")
	}
	c := &ConditionalBranches{Targets: make(map[string]string, len(pairs)/2)}
	for i := 0; i < len(pairs); i += 2 {
		expr, target := pairs[i], pairs[i+1]
		if _, dup := c.Targets[expr]; !dup {
			c.Exprs = append(c.Exprs, expr)
		}
		c.Targets[expr] = target
	}
	return c
}
