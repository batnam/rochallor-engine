package definition_test

// Spec-first property tests for the workflow definition validator.
//
// Source of truth: docs/workflow-format.md §Validation Rules
// Written from the specification ONLY — not the implementation.
// A failing test means the validator is not enforcing a documented rule.

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// minimalValidDef returns the smallest definition that satisfies all spec rules:
// one SERVICE_TASK pointing to an END step.
func minimalValidDef(id string) *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		ID:   id,
		Name: "Test workflow",
		Steps: []definition.WorkflowStep{
			{ID: "task", Name: "Task", Type: definition.StepTypeServiceTask, JobType: "job", NextStep: "end"},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
}

// ── Property 1 ────────────────────────────────────────────────────────────────
// Spec: "id — Pattern: ^[A-Za-z0-9_:\-]+$ (letters, digits, _, :, -)"
// Any character outside this set in the definition ID must be rejected.
func TestValidate_IDWithForbiddenCharsAlwaysFails(t *testing.T) {
	// Characters that are explicitly outside ^[A-Za-z0-9_:\-]+$
	forbidden := []string{
		"my workflow",  // space
		"order@v2",    // @
		"a/b",         // slash
		"def.name",    // dot
		"id+extra",    // plus
		"id=1",        // equals
		"id!",         // exclamation
	}

	for _, id := range forbidden {
		id := id
		t.Run("id="+id, func(t *testing.T) {
			def := minimalValidDef(id)
			if err := definition.Validate(def); err == nil {
				t.Fatalf("expected validation failure for id=%q, got nil", id)
			}
		})
	}
}

// ── Property 2 ────────────────────────────────────────────────────────────────
// Spec: "PARALLEL_GATEWAY — parallelNextSteps: Minimum 2 entries"
// A PARALLEL_GATEWAY with only one branch must be rejected.
func TestValidate_ParallelGatewayRequiresAtLeastTwoBranches(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		def := &definition.WorkflowDefinition{
			ID:   "test-parallel",
			Name: "Test",
			Steps: []definition.WorkflowStep{
				{
					ID:                "split",
					Name:              "Split",
					Type:              definition.StepTypeParallelGateway,
					ParallelNextSteps: []string{"branch-a"}, // only ONE branch — violates spec
					JoinStep:          "join",
				},
				{ID: "branch-a", Name: "Branch A", Type: definition.StepTypeServiceTask, JobType: "j", NextStep: "join"},
				{ID: "join", Name: "Join", Type: definition.StepTypeJoinGateway, NextStep: "end"},
				{ID: "end", Name: "End", Type: definition.StepTypeEnd},
			},
		}
		if err := definition.Validate(def); err == nil {
			t.Fatal("expected validation failure for PARALLEL_GATEWAY with only 1 branch, got nil")
		}
	})
}

// ── Property 3 ────────────────────────────────────────────────────────────────
// Spec: "PARALLEL_GATEWAY — joinStep: Required; target must exist"
// A PARALLEL_GATEWAY whose joinStep points to a non-existent step must fail.
func TestValidate_ParallelGatewayJoinStepMustExist(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		def := &definition.WorkflowDefinition{
			ID:   "test-parallel",
			Name: "Test",
			Steps: []definition.WorkflowStep{
				{
					ID:                "split",
					Name:              "Split",
					Type:              definition.StepTypeParallelGateway,
					ParallelNextSteps: []string{"branch-a", "branch-b"},
					JoinStep:          "nonexistent-join", // dangling reference
				},
				{ID: "branch-a", Name: "Branch A", Type: definition.StepTypeServiceTask, JobType: "j", NextStep: "real-join"},
				{ID: "branch-b", Name: "Branch B", Type: definition.StepTypeServiceTask, JobType: "j", NextStep: "real-join"},
				{ID: "real-join", Name: "Join", Type: definition.StepTypeJoinGateway, NextStep: "end"},
				{ID: "end", Name: "End", Type: definition.StepTypeEnd},
			},
		}
		if err := definition.Validate(def); err == nil {
			t.Fatal("expected validation failure for PARALLEL_GATEWAY with non-existent joinStep, got nil")
		}
	})
}

// ── Property 4 ────────────────────────────────────────────────────────────────
// Spec: "nextWorkflowId required (and must be non-empty) when autoStartNextWorkflow is true"
// Setting autoStartNextWorkflow without nextWorkflowId must be rejected.
func TestValidate_AutoStartWithoutNextWorkflowIDFails(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		def := minimalValidDef("test-chain")
		def.AutoStartNextWorkflow = true
		def.NextWorkflowId = "" // missing — violates spec

		if err := definition.Validate(def); err == nil {
			t.Fatal("expected validation failure for autoStartNextWorkflow=true without nextWorkflowId, got nil")
		}
	})
}

// ── Property 5 ────────────────────────────────────────────────────────────────
// Spec: "All steps are reachable — Graph walk from the first step must reach every step"
// Any step that cannot be reached from the first step must be rejected.
// This catches dead code in workflows — steps the engine would never execute.
func TestValidate_UnreachableStepAlwaysFails(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// orphan is defined but no other step references it → unreachable.
		def := &definition.WorkflowDefinition{
			ID:   "test-unreachable",
			Name: "Test",
			Steps: []definition.WorkflowStep{
				{ID: "task", Name: "Task", Type: definition.StepTypeServiceTask, JobType: "j", NextStep: "end"},
				{ID: "end", Name: "End", Type: definition.StepTypeEnd},
				{ID: "orphan", Name: "Orphan", Type: definition.StepTypeServiceTask, JobType: "j", NextStep: "end"}, // unreachable
			},
		}
		if err := definition.Validate(def); err == nil {
			t.Fatal("expected validation failure for unreachable step, got nil")
		}
	})
}

// ── Property 6 ────────────────────────────────────────────────────────────────
// Spec: "Boundary event targetStepId — Must point to an existing step ID"
// A boundary event pointing to a non-existent step must be rejected.
func TestValidate_BoundaryEventTargetMustExist(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		def := &definition.WorkflowDefinition{
			ID:   "test-boundary",
			Name: "Test",
			Steps: []definition.WorkflowStep{
				{
					ID:      "task",
					Name:    "Task",
					Type:    definition.StepTypeServiceTask,
					JobType: "job",
					NextStep: "end",
					BoundaryEvents: []definition.BoundaryEvent{
						{
							Type:         definition.BoundaryEventTypeTimer,
							Duration:     "PT30S",
							Interrupting: false,
							TargetStepId: "nonexistent-escalation", // dangling
						},
					},
				},
				{ID: "end", Name: "End", Type: definition.StepTypeEnd},
			},
		}
		if err := definition.Validate(def); err == nil {
			t.Fatal("expected validation failure for boundary event with non-existent targetStepId, got nil")
		}
	})
}

// ── Property 7 ────────────────────────────────────────────────────────────────
// Spec: "At least one END step is reachable — The workflow must have a terminal state"
// A workflow that only loops (no reachable END) must be rejected.
// This is the cycle-detection proxy: A → B → A has no reachable END → fails.
func TestValidate_CycleWithNoEndFails(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// A → B → A — no END reachable, loop forever.
		def := &definition.WorkflowDefinition{
			ID:   "test-cycle",
			Name: "Test",
			Steps: []definition.WorkflowStep{
				{ID: "a", Name: "A", Type: definition.StepTypeServiceTask, JobType: "j", NextStep: "b"},
				{ID: "b", Name: "B", Type: definition.StepTypeServiceTask, JobType: "j", NextStep: "a"},
			},
		}
		if err := definition.Validate(def); err == nil {
			t.Fatal("expected validation failure for cycle with no reachable END, got nil")
		}
	})
}

// ── Property 8 ────────────────────────────────────────────────────────────────
// Spec: "DECISION — conditionalNextSteps: Must have at least one entry"
// The spec also implies all target IDs must exist. A decision step with a
// target that doesn't exist must be rejected.
func TestValidate_DecisionTargetMustExist(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		def := &definition.WorkflowDefinition{
			ID:   "test-decision",
			Name: "Test",
			Steps: []definition.WorkflowStep{
				{
					ID:   "decide",
					Name: "Decide",
					Type: definition.StepTypeDecision,
					ConditionalNextSteps: map[string]string{
						"status == 'ok'": "nonexistent-step", // dangling
					},
				},
				{ID: "end", Name: "End", Type: definition.StepTypeEnd},
			},
		}
		if err := definition.Validate(def); err == nil {
			t.Fatal("expected validation failure for DECISION with non-existent target, got nil")
		}
	})
}

// ── Property 9 ────────────────────────────────────────────────────────────────
// Spec: "autoStartNextWorkflow + nextWorkflowId both set → valid"
// Setting both fields together on an otherwise valid definition must pass.
func TestValidate_AutoStartWithNextWorkflowIDPasses(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		def := minimalValidDef("test-chain")
		def.AutoStartNextWorkflow = true
		def.NextWorkflowId = "other-workflow"

		if err := definition.Validate(def); err != nil {
			t.Fatalf("expected valid definition with autoStart to pass, got: %v", err)
		}
	})
}
