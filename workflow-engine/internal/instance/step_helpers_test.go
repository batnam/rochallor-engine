package instance

import (
	"slices"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

func TestFindStep_ReturnsMatchingStep(t *testing.T) {
	def := &definition.WorkflowDefinition{
		Steps: []definition.WorkflowStep{
			{ID: "a", Type: definition.StepTypeServiceTask},
			{ID: "b", Type: definition.StepTypeEnd},
		},
	}
	got := findStep(def, "b")
	if got == nil {
		t.Fatalf("expected to find step %q, got nil", "b")
	}
	if got.ID != "b" || got.Type != definition.StepTypeEnd {
		t.Errorf("returned step has wrong identity: %+v", got)
	}
}

func TestFindStep_NotFound_ReturnsNil(t *testing.T) {
	def := &definition.WorkflowDefinition{
		Steps: []definition.WorkflowStep{
			{ID: "a", Type: definition.StepTypeServiceTask},
		},
	}
	if got := findStep(def, "unknown"); got != nil {
		t.Errorf("expected nil for missing step, got %+v", got)
	}
}

func TestFindStep_ReturnsPointerIntoSliceBacking(t *testing.T) {
	def := &definition.WorkflowDefinition{
		Steps: []definition.WorkflowStep{
			{ID: "a", Type: definition.StepTypeServiceTask},
		},
	}
	got := findStep(def, "a")
	if got != &def.Steps[0] {
		t.Errorf("expected findStep to return a pointer into def.Steps (not a copy)")
	}
}

func TestFindParallelGatewayFor_MatchesByJoinStep(t *testing.T) {
	def := &definition.WorkflowDefinition{
		Steps: []definition.WorkflowStep{
			{ID: "pg", Type: definition.StepTypeParallelGateway, JoinStep: "j", ParallelNextSteps: []string{"a", "b"}},
			{ID: "a", Type: definition.StepTypeServiceTask, NextStep: "j"},
			{ID: "b", Type: definition.StepTypeServiceTask, NextStep: "j"},
			{ID: "j", Type: definition.StepTypeJoinGateway, NextStep: "end"},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	got := findParallelGatewayFor(def, "j")
	if got == nil || got.ID != "pg" {
		t.Errorf("expected to find PARALLEL_GATEWAY %q for join %q, got %+v", "pg", "j", got)
	}
}

func TestFindParallelGatewayFor_NoMatch_ReturnsNil(t *testing.T) {
	def := &definition.WorkflowDefinition{
		Steps: []definition.WorkflowStep{
			{ID: "a", Type: definition.StepTypeServiceTask},
			{ID: "j", Type: definition.StepTypeJoinGateway},
		},
	}
	if got := findParallelGatewayFor(def, "j"); got != nil {
		t.Errorf("expected nil when no PG references the join, got %+v", got)
	}
}

func TestFindParallelGatewayFor_IgnoresNonPGSteps(t *testing.T) {
	// A different step type happens to carry a JoinStep value — must be ignored.
	def := &definition.WorkflowDefinition{
		Steps: []definition.WorkflowStep{
			{ID: "decoy", Type: definition.StepTypeServiceTask, JoinStep: "j"},
			{ID: "j", Type: definition.StepTypeJoinGateway},
		},
	}
	if got := findParallelGatewayFor(def, "j"); got != nil {
		t.Errorf("findParallelGatewayFor must only match PARALLEL_GATEWAY steps, got %+v", got)
	}
}

func TestBranchLeafsFor_ReturnsParallelNextSteps(t *testing.T) {
	pg := &definition.WorkflowStep{
		ID:                "pg",
		Type:              definition.StepTypeParallelGateway,
		ParallelNextSteps: []string{"a", "b", "c"},
	}
	got := branchLeafsFor(pg)
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("got %v, want [a b c]", got)
	}
}
