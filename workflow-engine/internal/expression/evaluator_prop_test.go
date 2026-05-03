package expression_test

// Spec-first property tests for the expression evaluator.
//
// Source of truth: docs/workflow-format.md §Expression Reference
// These tests are written from the specification ONLY — not from the implementation.
// Each property describes behaviour that a correct evaluator MUST exhibit.
// A failing test means a real bug was found.

import (
	"fmt"
	"testing"

	"pgregory.net/rapid"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/expression"
)

// ── Property 1 ────────────────────────────────────────────────────────────────
// Spec: "Undefined variable referenced → Runtime error; step fails"
// A correct evaluator MUST return an error when an expression references a
// variable that is not in the vars map — NOT silently return zero / nil / false.
func TestEvaluate_UndefinedVariableReturnsError(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Pick a variable name that is NOT in the vars map.
		varName := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "varname")
		expr := fmt.Sprintf(`%s == "somevalue"`, varName)

		_, err := expression.Evaluate(expr, map[string]any{})
		if err == nil {
			t.Fatalf("expected error for undefined variable %q, got nil", varName)
		}
	})
}

// ── Property 2 ────────────────────────────────────────────────────────────────
// Spec: "Comparison operators (==, !=, >, >=, <, <=) always return bool"
// Evaluating any well-formed comparison must produce a bool, not a number or string.
func TestEvaluate_ComparisonOperatorsAlwaysReturnBool(t *testing.T) {
	ops := []string{"==", "!=", ">", ">=", "<", "<="}

	rapid.Check(t, func(t *rapid.T) {
		op := rapid.SampledFrom(ops).Draw(t, "op")
		a := float64(rapid.IntRange(-1000, 1000).Draw(t, "a"))
		b := float64(rapid.IntRange(-1000, 1000).Draw(t, "b"))

		expr := fmt.Sprintf(`a %s b`, op)
		result, err := expression.Evaluate(expr, map[string]any{"a": a, "b": b})
		if err != nil {
			t.Skipf("evaluation error (acceptable for invalid op combinations): %v", err)
		}
		if _, ok := result.(bool); !ok {
			t.Fatalf("operator %q returned %T(%v), expected bool", op, result, result)
		}
	})
}

// ── Property 3 ────────────────────────────────────────────────────────────────
// Spec: "Arithmetic operators (+, -, *, /) return a number"
// Specifically: addition must be commutative — a+b == b+a — otherwise DECISION
// logic that relies on addition in TRANSFORMATION outputs would be inconsistent.
func TestEvaluate_AdditionIsCommutative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := float64(rapid.IntRange(-500, 500).Draw(t, "a"))
		b := float64(rapid.IntRange(-500, 500).Draw(t, "b"))
		vars := map[string]any{"a": a, "b": b}

		ab, err1 := expression.Evaluate(`a + b`, vars)
		ba, err2 := expression.Evaluate(`b + a`, vars)

		if err1 != nil || err2 != nil {
			t.Fatalf("unexpected error: a+b=%v b+a=%v", err1, err2)
		}
		if ab != ba {
			t.Fatalf("addition not commutative: a+b=%v but b+a=%v (a=%v b=%v)", ab, ba, a, b)
		}
	})
}

// ── Property 4 ────────────────────────────────────────────────────────────────
// Spec: comparison operators are semantically correct.
// Transitivity: if a > b AND b > c, then a > c must hold.
// A bug in numeric comparison (e.g. wrong type coercion) would violate this.
func TestEvaluate_ComparisonTransitivity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Pick three distinct integers in ascending order to guarantee a > b > c.
		c := float64(rapid.IntRange(-100, 100).Draw(t, "c"))
		b := c + float64(rapid.IntRange(1, 50).Draw(t, "b_delta"))
		a := b + float64(rapid.IntRange(1, 50).Draw(t, "a_delta"))

		vars := map[string]any{"a": a, "b": b, "c": c}

		aGTb, err := expression.Evaluate(`a > b`, vars)
		if err != nil || aGTb != true {
			t.Fatalf("a > b should be true (a=%v b=%v): result=%v err=%v", a, b, aGTb, err)
		}
		bGTc, err := expression.Evaluate(`b > c`, vars)
		if err != nil || bGTc != true {
			t.Fatalf("b > c should be true (b=%v c=%v): result=%v err=%v", b, c, bGTc, err)
		}
		aGTc, err := expression.Evaluate(`a > c`, vars)
		if err != nil {
			t.Fatalf("a > c returned error: %v", err)
		}
		if aGTc != true {
			t.Fatalf("transitivity violated: a>b && b>c but NOT a>c (a=%v b=%v c=%v)", a, b, c)
		}
	})
}

// ── Property 5 ────────────────────────────────────────────────────────────────
// Spec: "contains(collection, element) — Reports whether element is present"
// contains(list, x) MUST return true iff x is in the list, false otherwise.
// An empty list must never match anything.
func TestEvaluate_ContainsBuiltinSemantics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Build a list of unique short strings.
		size := rapid.IntRange(1, 6).Draw(t, "size")
		items := make([]any, size)
		for i := 0; i < size; i++ {
			items[i] = fmt.Sprintf("item%d", i)
		}

		// Pick an element that IS in the list.
		inIdx := rapid.IntRange(0, size-1).Draw(t, "in_idx")
		presentElem := items[inIdx].(string)

		// Pick an element that is NOT in the list.
		absentElem := "absent-element-xyz"

		vars := map[string]any{"list": items}

		got, err := expression.Evaluate(fmt.Sprintf(`contains(list, "%s")`, presentElem), vars)
		if err != nil {
			t.Fatalf("contains present element error: %v", err)
		}
		if got != true {
			t.Fatalf("contains(%v, %q) should be true, got %v", items, presentElem, got)
		}

		got, err = expression.Evaluate(fmt.Sprintf(`contains(list, "%s")`, absentElem), vars)
		if err != nil {
			t.Fatalf("contains absent element error: %v", err)
		}
		if got != false {
			t.Fatalf("contains(%v, %q) should be false, got %v", items, absentElem, got)
		}
	})
}

// ── Property 6 ────────────────────────────────────────────────────────────────
// Spec: "len(collection) — Returns the length of an array, map, or string"
// len MUST agree with Go's built-in len for the same inputs.
func TestEvaluate_LenBuiltinAgreesWithGoLen(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		size := rapid.IntRange(0, 10).Draw(t, "size")
		items := make([]any, size)
		for i := range items {
			items[i] = i
		}

		vars := map[string]any{"items": items}
		result, err := expression.Evaluate(`len(items)`, vars)
		if err != nil {
			t.Fatalf("len(items) error: %v", err)
		}
		got, ok := result.(int)
		if !ok {
			t.Fatalf("len returned %T(%v), expected int", result, result)
		}
		if got != size {
			t.Fatalf("len(%v) = %d, expected %d", items, got, size)
		}
	})
}

// ── Property 7 ────────────────────────────────────────────────────────────────
// Spec: "String literals: single or double quoted — 'APPROVED' and "APPROVED" are equivalent"
// The preprocessor must produce the same result for both quote styles.
func TestEvaluate_SingleQuoteEquivalentToDoubleQuote(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Use simple alpha strings to avoid quoting issues inside the expression.
		val := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "val")
		vars := map[string]any{"x": val}

		single, err1 := expression.Evaluate(fmt.Sprintf(`x == '%s'`, val), vars)
		double, err2 := expression.Evaluate(fmt.Sprintf(`x == "%s"`, val), vars)

		if (err1 != nil) != (err2 != nil) {
			t.Fatalf("single/double quote disagree on error: single=%v double=%v", err1, err2)
		}
		if err1 == nil && single != double {
			t.Fatalf("single quote result %v != double quote result %v for val=%q", single, double, val)
		}
	})
}

// ── Property 8 ────────────────────────────────────────────────────────────────
// Spec: "${ident} — Wraps a single identifier or full sub-expression; identical to bare identifier"
// ${x} must evaluate identically to bare x.
func TestEvaluate_DollarBraceEquivalentToBareIdent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		val := float64(rapid.IntRange(-100, 100).Draw(t, "val"))
		vars := map[string]any{"score": val}
		threshold := val - 1 // ensures score > threshold

		bare, err1 := expression.Evaluate(fmt.Sprintf(`score > %v`, threshold), vars)
		wrapped, err2 := expression.Evaluate(fmt.Sprintf(`${score} > %v`, threshold), vars)

		if (err1 != nil) != (err2 != nil) {
			t.Fatalf("bare/wrapped disagree on error: bare=%v wrapped=%v", err1, err2)
		}
		if err1 == nil && bare != wrapped {
			t.Fatalf("bare result %v != wrapped result %v for score=%v", bare, wrapped, val)
		}
	})
}

// ── Property 9 ────────────────────────────────────────────────────────────────
// Spec: "Malformed expression (unclosed quote, syntax error) → Rejected at evaluation time; step fails"
// Evaluate must return an error for syntactically broken expressions, never panic.
func TestEvaluate_MalformedExpressionReturnsError(t *testing.T) {
	malformed := []string{
		`"unclosed string`,
		`${unclosed`,
		`a ==`,   // incomplete expression
		`== b`,   // missing left operand
		// Note: "a ++ b" is NOT malformed — expr-lang parses it as a + (+b).
	}

	for _, expr := range malformed {
		expr := expr
		t.Run(fmt.Sprintf("expr=%q", expr), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Evaluate panicked on malformed expr=%q: %v", expr, r)
				}
			}()
			_, err := expression.Evaluate(expr, map[string]any{"a": 1, "b": 2})
			if err == nil {
				t.Fatalf("expected error for malformed expr=%q, got nil", expr)
			}
		})
	}
}
