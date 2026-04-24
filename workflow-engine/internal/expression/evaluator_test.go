package expression_test

import (
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/expression"
)

// TestBoolExpressions covers boolean expressions used in DECISION steps (R-003).
func TestBoolExpressions(t *testing.T) {
	tests := []struct {
		name string
		expr string
		vars map[string]any
		want bool
	}{
		// loan-application-workflow.json
		{
			"customer valid + high score → true",
			"#customerValid == true && #customerScore >= 700",
			map[string]any{"customerValid": true, "customerScore": float64(720)},
			true,
		},
		{
			"customer valid + low score → false",
			"#customerValid == true && #customerScore >= 700",
			map[string]any{"customerValid": true, "customerScore": float64(650)},
			false,
		},
		{
			"customer invalid → true for the false branch",
			"#customerValid == false",
			map[string]any{"customerValid": false},
			true,
		},
		{
			"customer valid + low score matches branch",
			"#customerValid == true && #customerScore < 700",
			map[string]any{"customerValid": true, "customerScore": float64(650)},
			true,
		},

		// document-verification-workflow.json
		{
			"doc authentic + high score",
			"#documentAuthentic == true && #authenticityScore >= 0.9",
			map[string]any{"documentAuthentic": true, "authenticityScore": float64(0.95)},
			true,
		},
		{
			"review decision APPROVED",
			"#reviewDecision == 'APPROVED'",
			map[string]any{"reviewDecision": "APPROVED"},
			true,
		},
		{
			"review decision REJECTED",
			"#reviewDecision == 'REJECTED'",
			map[string]any{"reviewDecision": "REJECTED"},
			true,
		},
		{
			"review decision PENDING (matches PENDING branch)",
			"#reviewDecision == 'PENDING'",
			map[string]any{"reviewDecision": "PENDING"},
			true,
		},
		{
			"doc not authentic or low score",
			"#documentAuthentic == false || #authenticityScore < 0.7",
			map[string]any{"documentAuthentic": false, "authenticityScore": float64(0.5)},
			true,
		},
		{
			"doc not authentic or low score — first OR branch triggers",
			"#documentAuthentic == false || #authenticityScore < 0.7",
			map[string]any{"documentAuthentic": false, "authenticityScore": float64(0.95)},
			true,
		},

		// pre-approve.json style
		{
			"is_bic_staff == true",
			"${is_bic_staff == true}",
			map[string]any{"is_bic_staff": true},
			true,
		},
		{
			"is_bic_staff == false",
			"${is_bic_staff == false}",
			map[string]any{"is_bic_staff": false},
			true,
		},
		{
			"nested field access: eligibleApprove == true",
			"${decisionResult.eligibleApprove == true}",
			map[string]any{"decisionResult": map[string]any{"eligibleApprove": true}},
			true,
		},

		// loan_registration.json style
		{
			"is_auto == 1",
			"#is_auto == 1",
			map[string]any{"is_auto": float64(1)},
			true,
		},
		{
			"is_auto == 0",
			"#is_auto == 0",
			map[string]any{"is_auto": float64(0)},
			true,
		},

		// Parenthesised expressions
		{
			"parenthesised OR + AND",
			"(#a == true || #b == true) && #c == true",
			map[string]any{"a": false, "b": true, "c": true},
			true,
		},

		// Arithmetic comparison
		{
			"arithmetic: price * qty > threshold",
			"price * qty > 1000",
			map[string]any{"price": 50.0, "qty": 25.0},
			true,
		},
		{
			"arithmetic: total - discount < budget",
			"total - discount < budget",
			map[string]any{"total": 900.0, "discount": 100.0, "budget": 850.0},
			true,
		},

		// Built-in: contains — rewrites to expr's native "in" operator
		{
			"contains: user has ADMIN role",
			`contains(roles, "ADMIN")`,
			map[string]any{"roles": []any{"USER", "ADMIN"}},
			true,
		},
		{
			"contains: user does not have SUPERUSER role",
			`contains(roles, "SUPERUSER")`,
			map[string]any{"roles": []any{"USER", "ADMIN"}},
			false,
		},
		{
			"in operator: direct membership check",
			`"ADMIN" in roles`,
			map[string]any{"roles": []any{"USER", "ADMIN"}},
			true,
		},

		// Built-in: len
		{
			"len: items list non-empty",
			"len(items) > 0",
			map[string]any{"items": []any{"a", "b"}},
			true,
		},
		{
			"len: items list empty",
			"len(items) == 0",
			map[string]any{"items": []any{}},
			true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := expression.Evaluate(tc.expr, tc.vars)
			if err != nil {
				t.Fatalf("Evaluate returned error: %v", err)
			}
			b, ok := got.(bool)
			if !ok {
				t.Fatalf("expected bool result, got %T (%v)", got, got)
			}
			if b != tc.want {
				t.Errorf("want %v, got %v", tc.want, b)
			}
		})
	}
}

// TestValueExpressions covers expressions that return non-bool values (arithmetic, string).
func TestValueExpressions(t *testing.T) {
	tests := []struct {
		name string
		expr string
		vars map[string]any
		want any
	}{
		{
			"arithmetic: addition",
			"price + tax",
			map[string]any{"price": 100.0, "tax": 20.0},
			120.0,
		},
		{
			"arithmetic: subtraction",
			"total - discount",
			map[string]any{"total": 500.0, "discount": 50.0},
			450.0,
		},
		{
			"arithmetic: multiplication",
			"price * qty",
			map[string]any{"price": 25.0, "qty": 4.0},
			100.0,
		},
		{
			"arithmetic: division",
			"total / divisor",
			map[string]any{"total": 100.0, "divisor": 4.0},
			25.0,
		},
		{
			"len: returns count",
			"len(items)",
			map[string]any{"items": []any{"a", "b", "c"}},
			3,
		},
		{
			"variable passthrough: string",
			"name",
			map[string]any{"name": "Alice"},
			"Alice",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := expression.Evaluate(tc.expr, tc.vars)
			if err != nil {
				t.Fatalf("Evaluate returned error: %v", err)
			}
			if got != tc.want {
				t.Errorf("want %v (%T), got %v (%T)", tc.want, tc.want, got, got)
			}
		})
	}
}

func TestEvaluateErrorCases(t *testing.T) {
	t.Run("malformed expression — unclosed string", func(t *testing.T) {
		_, err := expression.Evaluate("#a == 'hello", map[string]any{"a": "hello"})
		if err == nil {
			t.Fatal("expected error for malformed expression")
		}
	})

	t.Run("malformed expression — unclosed ${", func(t *testing.T) {
		_, err := expression.Evaluate("${missingBrace == true", map[string]any{"missingBrace": true})
		if err == nil {
			t.Fatal("expected error for unclosed ${")
		}
	})
}
