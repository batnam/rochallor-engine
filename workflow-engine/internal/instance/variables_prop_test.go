package instance

// Spec-first property tests for variable merging.
//
// Source of truth: docs/workflow-format.md §Variables and data flow
// "Merged at each step completion: a worker returns {"key": value, ...} and the
//  engine merges it into the instance variables. Existing keys are overwritten;
//  keys not returned are preserved."
//
// These tests are written from the specification ONLY — not the implementation.
// A failing test means mergeVariables violates a documented contract.

import (
	"encoding/json"
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// scalarVar generates a JSON-serialisable scalar: string, float64, bool, or null.
// Integer-valued floats are used to avoid floating-point precision noise in comparisons.
func scalarVar(t *rapid.T, label string) any {
	switch rapid.IntRange(0, 3).Draw(t, label+"_type") {
	case 0:
		return rapid.StringMatching(`[a-zA-Z0-9._-]{0,15}`).Draw(t, label+"_str")
	case 1:
		return float64(rapid.IntRange(-1000, 1000).Draw(t, label+"_num"))
	case 2:
		return rapid.Bool().Draw(t, label+"_bool")
	default:
		return nil
	}
}

// genVars generates a map[string]any with deterministic string keys.
func genVars(t *rapid.T, prefix string) map[string]any {
	n := rapid.IntRange(0, 8).Draw(t, prefix+"_n")
	m := make(map[string]any, n)
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("%s_k%d", prefix, i)] = scalarVar(t, fmt.Sprintf("%s_%d", prefix, i))
	}
	return m
}

func mustMarshal(m map[string]any) json.RawMessage {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func mustUnmarshal(raw json.RawMessage) map[string]any {
	m := make(map[string]any)
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	return m
}

func jsonEqual(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// ── Property 1 ────────────────────────────────────────────────────────────────
// Spec: "Existing keys are overwritten" (delta wins on conflict).
// For every key in delta, the merged result must contain that key with delta's value.
// This is the fundamental contract of step output application.
func TestMergeVariables_DeltaAlwaysWins(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		existing := genVars(t, "e")
		delta := genVars(t, "d")

		result, err := mergeVariables(mustMarshal(existing), delta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		merged := mustUnmarshal(result)

		for k, want := range delta {
			got, ok := merged[k]
			if !ok {
				t.Fatalf("delta key %q missing from result", k)
			}
			if !jsonEqual(want, got) {
				t.Fatalf("key %q: delta value %v was not applied, got %v", k, want, got)
			}
		}
	})
}

// ── Property 2 ────────────────────────────────────────────────────────────────
// Spec: "keys not returned are preserved"
// For every key in existing that delta does NOT touch, the result must preserve
// the original value unchanged.
func TestMergeVariables_UnmodifiedExistingKeysPreserved(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		existing := genVars(t, "e")
		delta := genVars(t, "d")

		result, err := mergeVariables(mustMarshal(existing), delta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		merged := mustUnmarshal(result)

		for k, want := range existing {
			if _, overwritten := delta[k]; overwritten {
				continue
			}
			got, ok := merged[k]
			if !ok {
				t.Fatalf("existing key %q disappeared after merge", k)
			}
			if !jsonEqual(want, got) {
				t.Fatalf("existing key %q was mutated: before=%v after=%v", k, want, got)
			}
		}
	})
}

// ── Property 3 ────────────────────────────────────────────────────────────────
// Spec: result contains exactly keys(existing) ∪ keys(delta) — no keys silently dropped,
// no phantom keys introduced.
func TestMergeVariables_ResultKeysAreUnionOfInputs(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		existing := genVars(t, "e")
		delta := genVars(t, "d")

		result, err := mergeVariables(mustMarshal(existing), delta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		merged := mustUnmarshal(result)

		// Build expected key set.
		want := make(map[string]struct{}, len(existing)+len(delta))
		for k := range existing {
			want[k] = struct{}{}
		}
		for k := range delta {
			want[k] = struct{}{}
		}

		if len(merged) != len(want) {
			t.Fatalf("result has %d keys, expected %d (union of existing+delta)", len(merged), len(want))
		}
		for k := range want {
			if _, ok := merged[k]; !ok {
				t.Fatalf("expected key %q in result (from union) but it was missing", k)
			}
		}
	})
}

// ── Property 4 ────────────────────────────────────────────────────────────────
// Spec: result is always a valid JSON object (not array, not primitive).
// Workers and downstream DECISION expressions read from it — corrupted JSON would break everything.
func TestMergeVariables_ResultIsAlwaysValidJSONObject(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		existing := genVars(t, "e")
		delta := genVars(t, "d")

		result, err := mergeVariables(mustMarshal(existing), delta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !json.Valid(result) {
			t.Fatalf("result is not valid JSON: %s", result)
		}
		// Must be an object, not an array or scalar.
		var obj map[string]any
		if err := json.Unmarshal(result, &obj); err != nil {
			t.Fatalf("result is not a JSON object: %s (unmarshal error: %v)", result, err)
		}
	})
}

// ── Property 5 ────────────────────────────────────────────────────────────────
// Spec: variables are typed — a key can change type between merges.
// Example: existing["score"]=700 (number), delta["score"]="excellent" (string).
// The result must hold "excellent" — no type locking.
func TestMergeVariables_TypeChangeIsAllowed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		numVal := float64(rapid.IntRange(0, 1000).Draw(t, "num"))
		strVal := rapid.StringMatching(`[a-z]{3,10}`).Draw(t, "str")

		existing := map[string]any{"key": numVal}
		delta := map[string]any{"key": strVal}

		result, err := mergeVariables(mustMarshal(existing), delta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		merged := mustUnmarshal(result)
		got, ok := merged["key"]
		if !ok {
			t.Fatal("key missing from result")
		}
		if got != strVal {
			t.Fatalf("after type change: want %q (string), got %v (%T)", strVal, got, got)
		}
	})
}

// ── Property 6 ────────────────────────────────────────────────────────────────
// Spec: "keys not returned are preserved" — if delta is empty, the result must
// be identical to the existing variables. No data loss on no-op merges.
func TestMergeVariables_EmptyDeltaPreservesAllExisting(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		existing := genVars(t, "e")
		raw := mustMarshal(existing)

		result, err := mergeVariables(raw, map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		merged := mustUnmarshal(result)

		if len(merged) != len(existing) {
			t.Fatalf("empty delta changed key count: %d → %d", len(existing), len(merged))
		}
		for k, want := range existing {
			if !jsonEqual(want, merged[k]) {
				t.Fatalf("empty delta mutated key %q: %v → %v", k, want, merged[k])
			}
		}
	})
}

// ── Property 7 ────────────────────────────────────────────────────────────────
// Spec: instances start with variables passed at creation; a null-valued delta key
// should set the variable to null — not silently drop the key.
// (Dropping a null-valued key would make it impossible to explicitly clear a variable.)
func TestMergeVariables_NullDeltaValueSetsKeyToNull(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Start with a non-null value.
		existing := map[string]any{"status": "active"}
		// Worker explicitly clears it by returning null.
		delta := map[string]any{"status": nil}

		result, err := mergeVariables(mustMarshal(existing), delta)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		merged := mustUnmarshal(result)

		val, ok := merged["status"]
		if !ok {
			t.Fatal("key 'status' was deleted instead of set to null")
		}
		if val != nil {
			t.Fatalf("expected null, got %v (%T)", val, val)
		}
	})
}

// ── Property 8 ────────────────────────────────────────────────────────────────
// Spec: invalid JSON in existing is a data-integrity failure — must return an error.
// Silently ignoring corrupted stored variables would cause silent data loss.
func TestMergeVariables_CorruptExistingJSONReturnsError(t *testing.T) {
	corrupted := []json.RawMessage{
		json.RawMessage(`not-json`),
		json.RawMessage(`{broken`),
		json.RawMessage(`[1,2,3]`), // array, not an object — contract violation
	}

	for _, bad := range corrupted {
		bad := bad
		t.Run(string(bad), func(t *testing.T) {
			_, err := mergeVariables(bad, map[string]any{"k": "v"})
			if err == nil {
				t.Fatalf("expected error for corrupt existing=%q, got nil", bad)
			}
		})
	}
}
