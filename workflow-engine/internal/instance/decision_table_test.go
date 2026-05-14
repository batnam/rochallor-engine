package instance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/expression"
)

// ─── toFloat64 ───────────────────────────────────────────────────────────────

func TestToFloat64_NumericTypes(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want float64
	}{
		{"float64", float64(3.5), 3.5},
		{"float32", float32(2.5), 2.5},
		{"int", int(7), 7},
		{"int32", int32(8), 8},
		{"int64", int64(9), 9},
		{"json.Number", json.Number("12.5"), 12.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toFloat64(tc.in)
			if !ok {
				t.Fatalf("expected ok=true for %T", tc.in)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestToFloat64_NonNumeric_ReturnsFalse(t *testing.T) {
	cases := []any{"foo", true, nil, []any{1, 2}}
	for _, in := range cases {
		if _, ok := toFloat64(in); ok {
			t.Errorf("expected ok=false for %T (%v), got true", in, in)
		}
	}
}

func TestToFloat64_BadJSONNumber_ReturnsFalse(t *testing.T) {
	if _, ok := toFloat64(json.Number("not-a-number")); ok {
		t.Errorf("expected ok=false for unparseable json.Number")
	}
}

// ─── aggregateColumn ─────────────────────────────────────────────────────────

func TestAggregateColumn_Sum(t *testing.T) {
	got, err := aggregateColumn('+', []any{1.0, 2.0, 3.5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(float64) != 6.5 {
		t.Errorf("got %v, want 6.5", got)
	}
}

func TestAggregateColumn_Count_TypeAgnostic(t *testing.T) {
	got, err := aggregateColumn('#', []any{"foo", 42, true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(int) != 3 {
		t.Errorf("got %v, want 3", got)
	}
}

func TestAggregateColumn_Max(t *testing.T) {
	got, err := aggregateColumn('>', []any{1.0, 5.0, 3.0, 2.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(float64) != 5.0 {
		t.Errorf("got %v, want 5", got)
	}
}

func TestAggregateColumn_Min(t *testing.T) {
	got, err := aggregateColumn('<', []any{4.0, 1.5, 3.0, 2.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(float64) != 1.5 {
		t.Errorf("got %v, want 1.5", got)
	}
}

func TestAggregateColumn_NonNumeric_FailsForSumMaxMin(t *testing.T) {
	for _, suffix := range []byte{'+', '>', '<'} {
		if _, err := aggregateColumn(suffix, []any{"foo", "bar"}); err == nil {
			t.Errorf("expected error for suffix %q on non-numeric values", suffix)
		}
	}
}

func TestAggregateColumn_EmptyValues_FailsForSumMaxMin(t *testing.T) {
	for _, suffix := range []byte{'+', '>', '<'} {
		if _, err := aggregateColumn(suffix, []any{}); err == nil {
			t.Errorf("expected error for suffix %q on empty slice", suffix)
		}
	}
}

func TestAggregateColumn_Count_EmptyValues_ReturnsZero(t *testing.T) {
	got, err := aggregateColumn('#', []any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(int) != 0 {
		t.Errorf("got %v, want 0", got)
	}
}

func TestAggregateColumn_UnknownSuffix_Fails(t *testing.T) {
	if _, err := aggregateColumn('?', []any{1.0}); err == nil {
		t.Errorf("expected error for unknown aggregator suffix")
	}
}

// ─── buildColumnLists ────────────────────────────────────────────────────────

func TestBuildColumnLists_PreservesDocumentOrder(t *testing.T) {
	evaluated := []map[string]any{
		{"v": 10.0},
		{"v": 5.0},
		{"v": 8.0},
	}
	got := buildColumnLists(evaluated)
	vs, ok := got["v"].([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got["v"])
	}
	if len(vs) != 3 || vs[0] != 10.0 || vs[1] != 5.0 || vs[2] != 8.0 {
		t.Errorf("expected [10 5 8] in document order, got %v", vs)
	}
}

func TestBuildColumnLists_MissingColumn_ContributesNil(t *testing.T) {
	evaluated := []map[string]any{
		{"code": "X", "amt": 10.0},
		{"code": "Y"}, // omits "amt"
		{"amt": 8.0},  // omits "code"
	}
	got := buildColumnLists(evaluated)
	codes := got["code"].([]any)
	amts := got["amt"].([]any)
	if codes[0] != "X" || codes[1] != "Y" || codes[2] != nil {
		t.Errorf("code list: expected [X Y nil], got %v", codes)
	}
	if amts[0] != 10.0 || amts[1] != nil || amts[2] != 8.0 {
		t.Errorf("amt list: expected [10 nil 8], got %v", amts)
	}
}

func TestBuildColumnLists_Empty_ReturnsEmptyMap(t *testing.T) {
	got := buildColumnLists(nil)
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

// ─── ruleMatches ─────────────────────────────────────────────────────────────

func TestRuleMatches_AllCellsTrue_ReturnsTrue(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		When: map[string]string{
			"a": "x >= 10",
			"b": "y == true",
		},
	}
	ok, err := ruleMatches(rule, map[string]any{"x": 20.0, "y": true}, "step", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected match")
	}
}

func TestRuleMatches_OneCellFalse_ReturnsFalse(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		When: map[string]string{
			"a": "x >= 10",
			"b": "x < 5", // false
		},
	}
	ok, err := ruleMatches(rule, map[string]any{"x": 20.0}, "step", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected no match when one cell is false")
	}
}

func TestRuleMatches_WildcardCellsAreSkipped(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		When: map[string]string{
			"a": "   ", // whitespace wildcard
			"b": "x >= 10",
		},
	}
	ok, err := ruleMatches(rule, map[string]any{"x": 20.0}, "step", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected match: whitespace cell must be treated as wildcard")
	}
}

func TestRuleMatches_EmptyWhenMap_ReturnsTrue(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{When: map[string]string{}}
	ok, err := ruleMatches(rule, map[string]any{}, "step", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected empty When map to act as catch-all")
	}
}

func TestRuleMatches_NonBoolResult_ReturnsCellError(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		When: map[string]string{"a": "x + 1"}, // returns float64
	}
	_, err := ruleMatches(rule, map[string]any{"x": 1.0}, "step", 3)
	if err == nil {
		t.Fatalf("expected error for non-bool cell result")
	}
	if !strings.HasPrefix(err.Error(), DecisionTableCellError) {
		t.Errorf("expected error prefix %q, got %q", DecisionTableCellError, err.Error())
	}
	if !strings.Contains(err.Error(), "ruleIndex=3") {
		t.Errorf("expected error to carry ruleIndex=3, got %q", err.Error())
	}
}

func TestRuleMatches_EvaluatorError_ReturnsCellError(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		When: map[string]string{"a": "undefined_var > 0"},
	}
	_, err := ruleMatches(rule, map[string]any{}, "step", 0)
	if err == nil {
		t.Fatalf("expected error for undefined variable")
	}
	if !strings.HasPrefix(err.Error(), DecisionTableCellError) {
		t.Errorf("expected error prefix %q, got %q", DecisionTableCellError, err.Error())
	}
}

// ─── evaluateRuleOutputs ─────────────────────────────────────────────────────

func TestEvaluateRuleOutputs_EmptyOutputs_ReturnsNil(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{}
	got, err := evaluateRuleOutputs(rule, map[string]any{}, "step", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty Outputs, got %v", got)
	}
}

func TestEvaluateRuleOutputs_LiteralValues_PassThrough(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		Outputs: map[string]json.RawMessage{
			"tier": json.RawMessage(`"GOLD"`),
			"fee":  json.RawMessage(`0.5`),
			"flag": json.RawMessage(`true`),
		},
	}
	got, err := evaluateRuleOutputs(rule, map[string]any{}, "step", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["tier"] != "GOLD" {
		t.Errorf("tier: got %v, want GOLD", got["tier"])
	}
	if got["fee"].(float64) != 0.5 {
		t.Errorf("fee: got %v, want 0.5", got["fee"])
	}
	if got["flag"].(bool) != true {
		t.Errorf("flag: got %v, want true", got["flag"])
	}
}

func TestEvaluateRuleOutputs_ExpressionString_Evaluates(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		Outputs: map[string]json.RawMessage{
			"doubled": json.RawMessage(`"${score * 2}"`),
		},
	}
	got, err := evaluateRuleOutputs(rule, map[string]any{"score": 50.0}, "step", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["doubled"] != 100.0 {
		t.Errorf("doubled: got %v, want 100", got["doubled"])
	}
}

func TestEvaluateRuleOutputs_NowExpression_ReturnsRFC3339(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		Outputs: map[string]json.RawMessage{
			"stampedAt": json.RawMessage(`"${now()}"`),
		},
	}
	got, err := evaluateRuleOutputs(rule, map[string]any{}, "step", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := got["stampedAt"].(string); !ok {
		t.Errorf("expected stampedAt to be a string (RFC3339), got %T", got["stampedAt"])
	}
}

func TestEvaluateRuleOutputs_BadExpression_ReturnsOutputError(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		Outputs: map[string]json.RawMessage{
			"v": json.RawMessage(`"${undefined_var * 2}"`),
		},
	}
	_, err := evaluateRuleOutputs(rule, map[string]any{}, "step", 4)
	if err == nil {
		t.Fatalf("expected error for undefined variable in ${expr}")
	}
	if !strings.HasPrefix(err.Error(), DecisionTableOutputError) {
		t.Errorf("expected error prefix %q, got %q", DecisionTableOutputError, err.Error())
	}
	if !strings.Contains(err.Error(), "ruleIndex=4") {
		t.Errorf("expected error to carry ruleIndex=4, got %q", err.Error())
	}
}

func TestEvaluateRuleOutputs_NonExprStringIsLiteral(t *testing.T) {
	// A string output that doesn't look like ${...} must be returned verbatim.
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		Outputs: map[string]json.RawMessage{
			"tag": json.RawMessage(`"hello ${name}"`),
		},
	}
	got, err := evaluateRuleOutputs(rule, map[string]any{"name": "world"}, "step", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["tag"] != "hello ${name}" {
		t.Errorf("expected literal pass-through for strings not fully wrapped in ${...}, got %v", got["tag"])
	}
}
