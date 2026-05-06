package instance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/expression"
)

// newServiceForTest wires a Service backed by mockDB + mockStore.
func newServiceForTest(def *definition.WorkflowDefinition) (*Service, *mockStore, *mockDB, *noopDispatcher) {
	store := newMockStore()
	dbm := &mockDB{}
	disp := &noopDispatcher{}
	SetExpressionEvaluator(expression.Evaluate)
	svc := NewService(context.Background(), dbm, store, fakeDefRepo{def: def}, disp)
	return svc, store, dbm, disp
}

func TestStart_DispatchesFirstStep_UserTask(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID:      "test::user-task",
		Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "ut", Type: definition.StepTypeUserTask},
		},
	}
	svc, store, dbm, _ := newServiceForTest(def)

	inst, err := svc.Start(context.Background(), def.ID, def.Version, map[string]any{"k": "v"}, "biz1")
	if err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	// handleUserTask transitions the instance to WAITING within the same tx,
	// so the returned instance should already reflect WAITING.
	if inst.Status != InstanceStatusWaiting {
		t.Errorf("after Start with USER_TASK first step, instance status = %q, want WAITING", inst.Status)
	}
	if got := store.instances[inst.ID].Status; got != InstanceStatusWaiting {
		t.Errorf("store status = %q, want WAITING", got)
	}
	if len(store.insertedUserTasks) != 1 {
		t.Errorf("expected 1 user_task insert, got %d", len(store.insertedUserTasks))
	}
	if got, want := dbm.txTypes, []string{"instance.start"}; !equalStrings(got, want) {
		t.Errorf("txTypes = %v, want %v", got, want)
	}
}

func TestHandleDecision_NoBranchMatched_FailsInstance(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::decision", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:   "decide",
				Type: definition.StepTypeDecision,
				ConditionalNextSteps: map[string]string{
					"x > 100": "neverReached",
				},
			},
			{ID: "neverReached", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)

	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"x": 1.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) == 0 {
		t.Errorf("expected handleDecision with no matching branch to fail the instance, but failedInstances is empty")
	}
}

func TestHandleDecision_FirstMatchingBranchDispatched(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::decision", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:                   "decide",
				Type:                 definition.StepTypeDecision,
				ConditionalNextSteps: map[string]string{"x >= 10": "go"},
			},
			{ID: "go", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"x": 42.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Errorf("expected the instance to reach END and be COMPLETED; completedInstances = %v", store.completedInstances)
	}
}

func TestHandleTransformation_AppliesPatchAndDispatches(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::xform", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:       "xform",
				Type:     definition.StepTypeTransformation,
				NextStep: "end",
				Transformations: map[string]json.RawMessage{
					"y": json.RawMessage(`"${x}"`),
				},
			},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"x": "hello"}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// One partial-update should have been recorded (the {y: "hello"} delta).
	if got := len(store.updatedVariablesPartial); got != 1 {
		t.Fatalf("expected one variables-partial update, got %d (%v)", got, store.updatedVariablesPartial)
	}
	if v, ok := store.updatedVariablesPartial[0]["y"]; !ok || v != "hello" {
		t.Errorf("expected partial update {y: \"hello\"}, got %v", store.updatedVariablesPartial[0])
	}
}

func TestHandleJoinGateway_PartialArrival_NoAdvance(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::join", Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "p", Type: definition.StepTypeParallelGateway, ParallelNextSteps: []string{"a", "b"}, JoinStep: "j"},
			{ID: "a", Type: definition.StepTypeServiceTask, NextStep: "j"},
			{ID: "b", Type: definition.StepTypeServiceTask, NextStep: "j"},
			{ID: "j", Type: definition.StepTypeJoinGateway, NextStep: "end"},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	store.branchLeafsCompleted = 1 // only one branch arrived

	inst := &WorkflowInstance{
		ID: "i1", DefinitionID: def.ID, DefinitionVersion: 1,
		Status:    InstanceStatusActive,
		Variables: json.RawMessage(`{}`),
	}
	store.instances[inst.ID] = inst
	tx := fakeTx{}
	if err := svc.handleJoinGateway(context.Background(), tx, inst, def, &def.Steps[3], "se-join"); err != nil {
		t.Fatalf("handleJoinGateway: %v", err)
	}
	if len(store.completedInstances) != 0 {
		t.Errorf("partial-arrival join should NOT complete the instance; got %d", len(store.completedInstances))
	}
}

func TestHandleJoinGateway_AllArrived_AdvancesPastJoin(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::join", Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "p", Type: definition.StepTypeParallelGateway, ParallelNextSteps: []string{"a", "b"}, JoinStep: "j"},
			{ID: "a", Type: definition.StepTypeServiceTask, NextStep: "j"},
			{ID: "b", Type: definition.StepTypeServiceTask, NextStep: "j"},
			{ID: "j", Type: definition.StepTypeJoinGateway, NextStep: "end"},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	store.branchLeafsCompleted = 2 // both branches arrived

	inst := &WorkflowInstance{
		ID: "i1", DefinitionID: def.ID, DefinitionVersion: 1,
		Status: InstanceStatusActive, CurrentStepIDs: []string{"j"},
		Variables: json.RawMessage(`{}`),
	}
	store.instances[inst.ID] = inst
	tx := fakeTx{}
	if err := svc.handleJoinGateway(context.Background(), tx, inst, def, &def.Steps[3], "se-join"); err != nil {
		t.Fatalf("handleJoinGateway: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Errorf("expected the instance to reach END and be COMPLETED; got %v", store.completedInstances)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
