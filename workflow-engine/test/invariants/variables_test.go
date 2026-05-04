//go:build integration

package invariants_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// corruptVariables overwrites an instance's variables column with a JSON value
// that is valid JSONB (so the DB accepts it) but is NOT a JSON object, causing
// json.Unmarshal into map[string]any to fail. We use a JSON array.
func corruptVariables(ctx context.Context, t *testing.T, instanceID string) {
	t.Helper()
	if _, err := gPool.Exec(ctx,
		`UPDATE workflow_instance SET variables = '[1,2,3]'::jsonb WHERE id = $1`,
		instanceID,
	); err != nil {
		t.Fatalf("corrupt variables: %v", err)
	}
}

// TestDecision_NonObjectVariablesFailWithDiagnosticMessage verifies that when
// instance variables contain valid JSON that is not an object (e.g. an array),
// a DECISION step fails the instance with a message that mentions the variable
// problem — not the misleading "no conditionalNextSteps branch matched".
//
// With the old code: variablesToMap swallows the unmarshal error, returns {},
// the expression evaluates against an empty env, and the engine reports
// "DecisionNoBranchMatched" — hiding the real cause.
//
// With the fix: variablesToMap returns the error; handleDecision calls
// failInstance with a message that includes "variable" or "unmarshal",
// making the root cause visible.
func TestDecision_NonObjectVariablesFailWithDiagnosticMessage(t *testing.T) {
	ctx := context.Background()

	def := &definition.WorkflowDefinition{
		ID:   "vars-corrupt-decision",
		Name: "Corrupt Vars Decision",
		Steps: []definition.WorkflowStep{
			{
				ID:       "task",
				Name:     "Task",
				Type:     definition.StepTypeServiceTask,
				JobType:  "vars-corrupt-task",
				NextStep: "decide",
			},
			{
				ID:   "decide",
				Name: "Decide",
				Type: definition.StepTypeDecision,
				ConditionalNextSteps: map[string]string{
					"#result == 'ok'": "end",
				},
			},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// Seed with valid variables so the instance starts fine.
	inst, err := gInstSvc.Start(ctx, def.ID, 0, map[string]any{"result": "ok"}, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	j := pollJob(ctx, t, "vars-corrupt-task")

	// Replace variables with a JSON array — valid JSONB, but not an object.
	// json.Unmarshal into map[string]any will fail on this.
	corruptVariables(ctx, t, inst.ID)

	// Worker completes the job. The engine will then dispatch the DECISION step
	// and attempt to evaluate expressions against the now-corrupt variables.
	if err := gInstSvc.CompleteJobAndAdvance(ctx, j.ID, "test-worker", nil); err != nil {
		t.Fatalf("CompleteJobAndAdvance: %v", err)
	}

	// Wait for the instance to reach a terminal state.
	terminal, err := awaitTerminal(ctx, inst.ID, 5*time.Second)
	if err != nil {
		t.Fatalf("await terminal: %v", err)
	}
	if terminal.Status != instance.InstanceStatusFailed {
		t.Fatalf("expected FAILED, got %s", terminal.Status)
	}

	// The failure reason must mention the variable problem.
	// Old code: "no conditionalNextSteps branch matched (DecisionNoBranchMatched)"
	// Fixed code: "corrupt instance variables: ..."
	reason := ""
	if terminal.FailureReason != nil {
		reason = *terminal.FailureReason
	}
	if strings.Contains(reason, "DecisionNoBranchMatched") {
		t.Fatalf("failure reason hides variable corruption: %q\nExpected a message mentioning the variable problem", reason)
	}
	if !strings.Contains(strings.ToLower(reason), "variable") {
		t.Fatalf("failure reason does not mention 'variable': %q", reason)
	}
}

// TestTransformation_NonObjectVariablesReturnError verifies that a
// TRANSFORMATION step with corrupt (non-object) variables returns an error
// rather than silently applying transformations against an empty variable set.
func TestTransformation_NonObjectVariablesReturnError(t *testing.T) {
	ctx := context.Background()

	def := &definition.WorkflowDefinition{
		ID:   "vars-corrupt-transform",
		Name: "Corrupt Vars Transform",
		Steps: []definition.WorkflowStep{
			{
				ID:       "task",
				Name:     "Task",
				Type:     definition.StepTypeServiceTask,
				JobType:  "vars-transform-task",
				NextStep: "transform",
			},
			{
				ID:   "transform",
				Name: "Transform",
				Type: definition.StepTypeTransformation,
				Transformations: map[string]json.RawMessage{
					"processed": json.RawMessage(`true`),
				},
				NextStep: "end",
			},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}

	inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	j := pollJob(ctx, t, "vars-transform-task")
	corruptVariables(ctx, t, inst.ID)

	// CompleteJobAndAdvance triggers the TRANSFORMATION step.
	// With old code: variablesToMap returns {}, transformation silently applies
	// to an empty map, instance proceeds to END (no error — wrong behaviour).
	// With the fix: an error is returned and the transaction rolls back.
	err = gInstSvc.CompleteJobAndAdvance(ctx, j.ID, "test-worker", nil)
	if err == nil {
		// If no error is returned, check whether the instance ended correctly
		// (it should have failed, not completed, with corrupt vars).
		got, getErr := gInstSvc.Get(ctx, inst.ID)
		if getErr != nil {
			t.Fatalf("get instance: %v", getErr)
		}
		if got.Status == instance.InstanceStatusCompleted {
			t.Fatal("instance completed despite corrupt variables — transformation silently used empty map")
		}
	}
}
