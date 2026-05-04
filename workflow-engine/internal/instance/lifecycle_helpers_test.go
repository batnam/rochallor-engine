package instance

import (
	"errors"
	"slices"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

func TestRecomputeInstanceStatus(t *testing.T) {
	def := &definition.WorkflowDefinition{
		Steps: []definition.WorkflowStep{
			{ID: "a", Type: definition.StepTypeServiceTask},
			{ID: "b", Type: definition.StepTypeUserTask},
			{ID: "c", Type: definition.StepTypeWait},
			{ID: "d", Type: definition.StepTypeTransformation},
		},
	}

	cases := []struct {
		name    string
		current []string
		want    InstanceStatus
	}{
		{"no parked steps", []string{"a"}, InstanceStatusActive},
		{"all parked steps", []string{"b", "c"}, InstanceStatusWaiting},
		{"mixed — parked present", []string{"a", "b"}, InstanceStatusWaiting},
		{"mixed — parked absent", []string{"a", "d"}, InstanceStatusActive},
		{"unknown step id is ignored", []string{"unknown"}, InstanceStatusActive},
		{"empty", nil, InstanceStatusActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := &WorkflowInstance{CurrentStepIDs: tc.current}
			got := recomputeInstanceStatus(inst, def)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRemoveFromCurrentSteps(t *testing.T) {
	inst := &WorkflowInstance{CurrentStepIDs: []string{"a", "b", "c"}}
	removeFromCurrentSteps(inst, "b")
	if got, want := len(inst.CurrentStepIDs), 2; got != want {
		t.Fatalf("len = %d, want %d", got, want)
	}
	for _, s := range inst.CurrentStepIDs {
		if s == "b" {
			t.Errorf("b should have been removed; got %v", inst.CurrentStepIDs)
		}
	}
}

func TestWithStep_AddsNewID(t *testing.T) {
	got := withStep([]string{"a", "b"}, "c")
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("got %v, want [a b c]", got)
	}
}

func TestWithStep_IsIdempotentWhenPresent(t *testing.T) {
	got := withStep([]string{"a", "b"}, "b")
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v, want [a b]", got)
	}
}

func TestWithStep_DoesNotMutateInput(t *testing.T) {
	ids := []string{"a", "b"}
	_ = withStep(ids, "c")
	if !slices.Equal(ids, []string{"a", "b"}) {
		t.Fatalf("input was mutated: %v", ids)
	}
}

func TestWithoutStep_RemovesID(t *testing.T) {
	got := withoutStep([]string{"a", "b", "c"}, "b")
	if !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("got %v, want [a c]", got)
	}
}

func TestWithoutStep_NilSafe(t *testing.T) {
	got := withoutStep(nil, "x")
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestWithoutStep_DoesNotMutateInput(t *testing.T) {
	ids := []string{"a", "b", "c"}
	_ = withoutStep(ids, "b")
	if !slices.Equal(ids, []string{"a", "b", "c"}) {
		t.Fatalf("input was mutated: %v", ids)
	}
}

func TestTypedErrorsAreDistinct(t *testing.T) {
	all := []error{
		ErrInstanceNotFound,
		ErrUserTaskNotFound,
		ErrInstanceTerminal,
		ErrWaitStepNotParked,
		ErrStepTypeMismatch,
	}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("sentinel errors %d and %d should be distinct but errors.Is returned true", i, j)
			}
		}
	}
}
