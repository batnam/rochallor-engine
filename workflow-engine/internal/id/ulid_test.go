package id_test

import (
	"sort"
	"sync"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/id"
)

// TestNewProducesNonEmptyString asserts New() returns a non-empty string.
func TestNewProducesNonEmptyString(t *testing.T) {
	got := id.New()
	if got == "" {
		t.Fatal("New() returned empty string")
	}
	// ULID strings are 26 characters long.
	if len(got) != 26 {
		t.Errorf("New() length: want 26, got %d (value=%q)", len(got), got)
	}
}

// TestUniquenessUnder10kConcurrentGenerations asserts that 10 000 concurrent
// calls to New() produce 10 000 distinct values (per T039 spec requirement).
func TestUniquenessUnder10kConcurrentGenerations(t *testing.T) {
	const n = 10_000
	ids := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			ids[i] = id.New()
		}()
	}
	wg.Wait()

	seen := make(map[string]struct{}, n)
	for _, v := range ids {
		if _, dup := seen[v]; dup {
			t.Fatalf("duplicate ULID: %q", v)
		}
		seen[v] = struct{}{}
	}
}

// TestLexicographicSortOrder asserts that ULIDs generated sequentially
// (in the same millisecond clock tick, where the monotonic counter must
// ensure ordering) sort in the order they were generated.
//
// The oklog/ulid monotonic source increments the random part when
// multiple ULIDs are generated within the same millisecond, guaranteeing
// lexicographic order for the same timestamp.
func TestLexicographicSortOrder(t *testing.T) {
	const n = 1000
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = id.New()
	}

	sorted := make([]string, n)
	copy(sorted, ids)
	sort.Strings(sorted)

	for i := range ids {
		if ids[i] != sorted[i] {
			t.Errorf("sort order violated at index %d: original=%q sorted=%q", i, ids[i], sorted[i])
			break
		}
	}
}

// TestSemanticAliasesReturnNonEmpty ensures that all semantic alias functions
// delegate correctly to New() and return non-empty ULIDs.
func TestSemanticAliasesReturnNonEmpty(t *testing.T) {
	funcs := []struct {
		name string
		fn   func() string
	}{
		{"NewInstance", id.NewInstance},
		{"NewStepExecution", id.NewStepExecution},
		{"NewJob", id.NewJob},
		{"NewUserTask", id.NewUserTask},
		{"NewBoundaryEvent", id.NewBoundaryEvent},
	}
	for _, f := range funcs {
		v := f.fn()
		if v == "" {
			t.Errorf("%s() returned empty string", f.name)
		}
		if len(v) != 26 {
			t.Errorf("%s() length: want 26, got %d", f.name, len(v))
		}
	}
}
