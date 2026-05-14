package definition_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// Spec (docs/workflow-format.md, Expression Reference): "Expressions are
// evaluated in the order they appear in the JSON object." That requires
// JSON unmarshal to capture insertion order — Go map iteration is
// intentionally randomised, so a plain map[string]string loses the order.
//
// These tests pin the contract: UnmarshalJSON captures order and
// MarshalJSON reproduces it byte-for-byte.

func TestConditionalBranches_UnmarshalJSON_PreservesInsertionOrder(t *testing.T) {
	raw := []byte(`{
		"creditScore >= 750": "premium",
		"creditScore >= 700": "gold",
		"creditScore >= 600": "silver",
		"creditScore >= 500": "bronze",
		"true": "fallback"
	}`)
	var c definition.ConditionalBranches
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	wantExprs := []string{
		"creditScore >= 750",
		"creditScore >= 700",
		"creditScore >= 600",
		"creditScore >= 500",
		"true",
	}
	if len(c.Exprs) != len(wantExprs) {
		t.Fatalf("Exprs length = %d, want %d", len(c.Exprs), len(wantExprs))
	}
	for i, want := range wantExprs {
		if c.Exprs[i] != want {
			t.Errorf("Exprs[%d] = %q, want %q", i, c.Exprs[i], want)
		}
	}
	wantTargets := map[string]string{
		"creditScore >= 750": "premium",
		"creditScore >= 700": "gold",
		"creditScore >= 600": "silver",
		"creditScore >= 500": "bronze",
		"true":               "fallback",
	}
	for k, v := range wantTargets {
		if c.Targets[k] != v {
			t.Errorf("Targets[%q] = %q, want %q", k, c.Targets[k], v)
		}
	}
}

func TestConditionalBranches_MarshalJSON_ReproducesOrder(t *testing.T) {
	c := definition.NewConditionalBranches(
		"a", "premium",
		"b", "gold",
		"c", "silver",
	)
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	want := `{"a":"premium","b":"gold","c":"silver"}`
	if string(out) != want {
		t.Errorf("MarshalJSON output\n got: %s\nwant: %s", out, want)
	}
}

// Spec invariant: order must be preserved even when keys contain JSON HTML
// escape triggers (`<`, `>`, `&`). Compare by the position each key appears
// in the output, not by byte equality, because json.Marshal post-escapes
// these characters.
func TestConditionalBranches_MarshalJSON_OrderPreservedAcrossEscapedKeys(t *testing.T) {
	c := definition.NewConditionalBranches(
		"creditScore >= 750", "premium",
		"creditScore >= 700", "gold",
		"creditScore >= 600", "silver",
	)
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	// `>` in keys is HTML-escaped to `>` by json.Marshal. Use the
	// target step IDs (which round-trip verbatim) to verify positional
	// order in the output.
	want := []string{"premium", "gold", "silver"}
	prev := -1
	for _, target := range want {
		idx := strings.Index(string(out), `"`+target+`"`)
		if idx < 0 {
			t.Fatalf("target %q not present in output %s", target, out)
		}
		if idx <= prev {
			t.Errorf("target %q appears at byte %d, which is not after the previous target (byte %d); output: %s",
				target, idx, prev, out)
		}
		prev = idx
	}
}

func TestConditionalBranches_RoundTripPreservesOrder(t *testing.T) {
	// Use 6 keys; with a buggy map-based implementation the chance of all
	// being in the same order after round-trip is 1/720 ≈ 0.14%.
	raw := []byte(`{"a":"1","b":"2","c":"3","d":"4","e":"5","f":"6"}`)
	var c definition.ConditionalBranches
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := json.Marshal(&c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(out) != string(raw) {
		t.Errorf("round-trip lost order\n got: %s\nwant: %s", out, raw)
	}
}

func TestConditionalBranches_UnmarshalJSON_NullIsNoOp(t *testing.T) {
	c := definition.NewConditionalBranches("a", "1")
	if err := json.Unmarshal([]byte("null"), c); err != nil {
		t.Fatalf("UnmarshalJSON null: %v", err)
	}
	// A `null` should not clobber existing content.
	if len(c.Exprs) != 1 {
		t.Errorf("expected `null` unmarshal to be a no-op, got %v", c.Exprs)
	}
}

func TestConditionalBranches_UnmarshalJSON_BadShape_ReturnsError(t *testing.T) {
	cases := []string{
		`[]`,           // array, not object
		`"x"`,          // string
		`{"k": 42}`,    // non-string value
		`{"k": [1,2]}`, // non-string value
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			var c definition.ConditionalBranches
			if err := json.Unmarshal([]byte(in), &c); err == nil {
				t.Errorf("expected error for %q, got nil (got %+v)", in, c)
			}
		})
	}
}

// Spec (docs/workflow-format.md): full Parse() path. Verify that uploading
// a workflow definition through Parse() preserves the author's DECISION
// expression order — the path real REST callers use.
func TestParse_DECISION_PreservesAuthorOrder(t *testing.T) {
	src := `{
		"id": "test::decision-order",
		"name": "Decision Order",
		"steps": [
			{
				"id": "decide",
				"name": "Decide",
				"type": "DECISION",
				"conditionalNextSteps": {
					"first":  "a",
					"second": "b",
					"third":  "c",
					"fourth": "d",
					"fifth":  "e"
				}
			},
			{"id": "a", "name": "A", "type": "END"},
			{"id": "b", "name": "B", "type": "END"},
			{"id": "c", "name": "C", "type": "END"},
			{"id": "d", "name": "D", "type": "END"},
			{"id": "e", "name": "E", "type": "END"}
		]
	}`
	def, err := definition.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	step := def.Steps[0]
	if step.ConditionalNextSteps == nil {
		t.Fatalf("ConditionalNextSteps is nil")
	}
	want := []string{"first", "second", "third", "fourth", "fifth"}
	for i, w := range want {
		if step.ConditionalNextSteps.Exprs[i] != w {
			t.Errorf("Exprs[%d] = %q, want %q (full Exprs: %v)", i, step.ConditionalNextSteps.Exprs[i], w, step.ConditionalNextSteps.Exprs)
		}
	}
}
