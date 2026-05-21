package instance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
				ID:                   "decide",
				Type:                 definition.StepTypeDecision,
				ConditionalNextSteps: definition.NewConditionalBranches("x > 100", "neverReached"),
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
				ConditionalNextSteps: definition.NewConditionalBranches("x >= 10", "go"),
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

// ─── DECISION_TABLE handler tests (U/F) ──────────────────────────────────────

// dtDef builds a minimal workflow with one DECISION_TABLE step routing to
// `end`. The caller sets the rules and hit policy.
func dtDef(rules []definition.DecisionTableRule, hitPolicy string) *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		ID: "test::dt", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:        "dt",
				Type:      definition.StepTypeDecisionTable,
				HitPolicy: hitPolicy,
				NextStep:  "end",
				DecisionTable: &definition.DecisionTable{Rules: rules},
			},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
}

// TestHandleDecisionTable_F_SingleMatch_AdvancesWithOutputs verifies that
// hit policy F merges the first matching rule's outputs into the variable
// map and dispatches step.NextStep.
func TestHandleDecisionTable_F_SingleMatch_AdvancesWithOutputs(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When: map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{
				"tier": json.RawMessage(`"SILVER"`),
				"fee":  json.RawMessage(`0.7`),
			},
		},
	}, "F")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 720.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected instance to complete via END, got %d completions", len(store.completedInstances))
	}
	if got := len(store.updatedVariablesPartial); got != 1 {
		t.Fatalf("expected 1 variables-partial update from the DT outputs, got %d (%v)", got, store.updatedVariablesPartial)
	}
	patch := store.updatedVariablesPartial[0]
	if patch["tier"] != "SILVER" || patch["fee"].(float64) != 0.7 {
		t.Errorf("merged outputs unexpected: %v", patch)
	}
}

// TestHandleDecisionTable_F_MultipleMatches_OnlyFirstApplies verifies that
// under hit policy F, only the first matching rule's outputs are applied.
func TestHandleDecisionTable_F_MultipleMatches_OnlyFirstApplies(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"SILVER"`)},
		},
		{
			When:    map[string]string{"score": "score >= 600"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"BRONZE"`)},
		},
	}, "F")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 720.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected instance to complete via END")
	}
	if got := len(store.updatedVariablesPartial); got != 1 {
		t.Fatalf("expected exactly one DT outputs update (first match only), got %d", got)
	}
	if store.updatedVariablesPartial[0]["tier"] != "SILVER" {
		t.Errorf("first match's tier should win under F, got %v", store.updatedVariablesPartial[0])
	}
}

// TestHandleDecisionTable_U_SingleMatch_Advances verifies hit policy U with
// exactly one match merges outputs and advances.
func TestHandleDecisionTable_U_SingleMatch_Advances(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"SILVER"`)},
		},
	}, "U")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 720.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected instance to complete via END")
	}
}

// TestHandleDecisionTable_U_NoMatch_FailsNoRuleMatched verifies that zero
// matches under any policy fail with DecisionTableNoRuleMatched.
func TestHandleDecisionTable_U_NoMatch_FailsNoRuleMatched(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{When: map[string]string{"score": "score >= 700"}},
	}, "U")
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 500.0}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected the instance to fail under no-match; got %d failed", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.HasPrefix(*got, DecisionTableNoRuleMatched) {
		t.Errorf("expected failure prefix %q, got %v", DecisionTableNoRuleMatched, got)
	}
}

// TestHandleDecisionTable_U_MultipleMatches_UniqueViolation verifies that
// hit policy U with two or more matches fails with DecisionTableUniqueViolation
// naming the matching indices.
func TestHandleDecisionTable_U_MultipleMatches_UniqueViolation(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"SILVER"`)},
		},
		{
			When:    map[string]string{"score": "score >= 600"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"BRONZE"`)},
		},
	}, "U")
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 720.0}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected the instance to fail under unique-violation; got %d failed", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.HasPrefix(*got, DecisionTableUniqueViolation) {
		t.Fatalf("expected failure prefix %q, got %v", DecisionTableUniqueViolation, got)
	}
	if !strings.Contains(*got, "[0 1]") {
		t.Errorf("expected the failure message to name the matching indices [0 1], got %q", *got)
	}
}

// ─── DECISION_TABLE handler tests (A / R / C / aggregators) ─────────────────

// TestHandleDecisionTable_A_Agreement_Succeeds verifies that hit policy A
// with matching rules whose outputs agree on every column merges one set of
// outputs into the variable map.
func TestHandleDecisionTable_A_Agreement_Succeeds(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"GOLD"`), "fee": json.RawMessage(`0.5`)},
		},
		{
			When:    map[string]string{"deposit": "deposit >= 1000"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"GOLD"`), "fee": json.RawMessage(`0.5`)},
		},
	}, "A")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 720.0, "deposit": 5000.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected the instance to complete, got %d completions and %d failures", len(store.completedInstances), len(store.failedInstances))
	}
	if got := len(store.updatedVariablesPartial); got != 1 {
		t.Fatalf("expected one outputs merge under A, got %d", got)
	}
	patch := store.updatedVariablesPartial[0]
	if patch["tier"] != "GOLD" || patch["fee"].(float64) != 0.5 {
		t.Errorf("merged outputs unexpected: %v", patch)
	}
}

// TestHandleDecisionTable_A_Disagreement_FailsAnyConflict verifies that hit
// policy A with matching rules whose outputs disagree on at least one column
// fails with DecisionTableAnyConflict.
func TestHandleDecisionTable_A_Disagreement_FailsAnyConflict(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"GOLD"`)},
		},
		{
			When:    map[string]string{"deposit": "deposit >= 1000"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"SILVER"`)},
		},
	}, "A")
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 720.0, "deposit": 5000.0}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected failure under A-disagreement, got %d failures", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.HasPrefix(*got, DecisionTableAnyConflict) {
		t.Fatalf("expected failure prefix %q, got %v", DecisionTableAnyConflict, got)
	}
	if !strings.Contains(*got, "tier") {
		t.Errorf("expected failure message to name the disagreeing column \"tier\", got %q", *got)
	}
}

// TestHandleDecisionTable_R_BuildsPerColumnLists verifies that hit policy R
// across three matching rules with two output columns produces 3-element
// lists per column in document order.
func TestHandleDecisionTable_R_BuildsPerColumnLists(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"a": "a == true"},
			Outputs: map[string]json.RawMessage{"code": json.RawMessage(`"X"`), "amt": json.RawMessage(`10`)},
		},
		{
			When:    map[string]string{"b": "b == true"},
			Outputs: map[string]json.RawMessage{"code": json.RawMessage(`"Y"`), "amt": json.RawMessage(`5`)},
		},
		{
			When:    map[string]string{"c": "c == true"},
			Outputs: map[string]json.RawMessage{"code": json.RawMessage(`"Z"`), "amt": json.RawMessage(`8`)},
		},
	}, "R")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": true, "b": true, "c": true}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected the instance to complete, got %d completions", len(store.completedInstances))
	}
	patch := store.updatedVariablesPartial[0]
	codes, ok := patch["code"].([]any)
	if !ok || len(codes) != 3 || codes[0] != "X" || codes[1] != "Y" || codes[2] != "Z" {
		t.Errorf("expected code = [X Y Z] in document order, got %v", patch["code"])
	}
	amts, ok := patch["amt"].([]any)
	if !ok || len(amts) != 3 || amts[0].(float64) != 10 || amts[1].(float64) != 5 || amts[2].(float64) != 8 {
		t.Errorf("expected amt = [10 5 8] in document order, got %v", patch["amt"])
	}
}

// TestHandleDecisionTable_C_NoAggregator_MatchesR verifies that hit policy C
// (no aggregator) produces the same per-column-list output shape as R.
func TestHandleDecisionTable_C_NoAggregator_MatchesR(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{When: map[string]string{"a": "a == true"}, Outputs: map[string]json.RawMessage{"v": json.RawMessage(`1`)}},
		{When: map[string]string{"b": "b == true"}, Outputs: map[string]json.RawMessage{"v": json.RawMessage(`2`)}},
	}, "C")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": true, "b": true}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	patch := store.updatedVariablesPartial[0]
	vs, ok := patch["v"].([]any)
	if !ok || len(vs) != 2 || vs[0].(float64) != 1 || vs[1].(float64) != 2 {
		t.Errorf("expected v = [1 2], got %v", patch["v"])
	}
}

// TestHandleDecisionTable_R_NoMatch_FailsNoRuleMatched verifies that R/C
// with zero matches surfaces DecisionTableNoRuleMatched (no implicit empty
// list fallback).
func TestHandleDecisionTable_R_NoMatch_FailsNoRuleMatched(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{When: map[string]string{"a": "a == true"}, Outputs: map[string]json.RawMessage{"v": json.RawMessage(`1`)}},
	}, "R")
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": false}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected failure under R-no-match, got %d failures", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.HasPrefix(*got, DecisionTableNoRuleMatched) {
		t.Errorf("expected failure prefix %q, got %v", DecisionTableNoRuleMatched, got)
	}
}

// TestHandleDecisionTable_CPlus_SumsPerColumn verifies that C+ sums numeric
// values per column across all matched rules.
func TestHandleDecisionTable_CPlus_SumsPerColumn(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{When: map[string]string{"a": "a == true"}, Outputs: map[string]json.RawMessage{"fee": json.RawMessage(`25`)}},
		{When: map[string]string{"b": "b == true"}, Outputs: map[string]json.RawMessage{"fee": json.RawMessage(`15`)}},
		{When: map[string]string{"c": "c == true"}, Outputs: map[string]json.RawMessage{"fee": json.RawMessage(`10`)}},
	}, "C+")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": true, "b": true, "c": true}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	patch := store.updatedVariablesPartial[0]
	if fee, ok := patch["fee"].(float64); !ok || fee != 50 {
		t.Errorf("expected fee = 50 under C+, got %v", patch["fee"])
	}
}

// TestHandleDecisionTable_CHash_CountsMatches verifies C# returns the count
// of matching rules for every output column.
func TestHandleDecisionTable_CHash_CountsMatches(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{When: map[string]string{"a": "a == true"}, Outputs: map[string]json.RawMessage{"x": json.RawMessage(`"foo"`)}},
		{When: map[string]string{"b": "b == true"}, Outputs: map[string]json.RawMessage{"x": json.RawMessage(`"bar"`)}},
		{When: map[string]string{"c": "c == true"}, Outputs: map[string]json.RawMessage{"x": json.RawMessage(`"baz"`)}},
	}, "C#")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": true, "b": true, "c": true}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	patch := store.updatedVariablesPartial[0]
	if count, ok := patch["x"].(int); !ok || count != 3 {
		t.Errorf("expected x = 3 (count) under C#, got %v (%T)", patch["x"], patch["x"])
	}
}

// TestHandleDecisionTable_CMaxMin_AcrossColumns verifies C> returns max and
// C< returns min per column.
func TestHandleDecisionTable_CMaxMin_AcrossColumns(t *testing.T) {
	rules := []definition.DecisionTableRule{
		{When: map[string]string{"a": "a == true"}, Outputs: map[string]json.RawMessage{"v": json.RawMessage(`25`)}},
		{When: map[string]string{"b": "b == true"}, Outputs: map[string]json.RawMessage{"v": json.RawMessage(`15`)}},
		{When: map[string]string{"c": "c == true"}, Outputs: map[string]json.RawMessage{"v": json.RawMessage(`10`)}},
	}
	def := dtDef(rules, "C>")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": true, "b": true, "c": true}, ""); err != nil {
		t.Fatalf("Start (C>): %v", err)
	}
	if v, ok := store.updatedVariablesPartial[0]["v"].(float64); !ok || v != 25 {
		t.Errorf("expected v = 25 under C>, got %v", store.updatedVariablesPartial[0]["v"])
	}

	def = dtDef(rules, "C<")
	svc, store, _, _ = newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": true, "b": true, "c": true}, ""); err != nil {
		t.Fatalf("Start (C<): %v", err)
	}
	if v, ok := store.updatedVariablesPartial[0]["v"].(float64); !ok || v != 10 {
		t.Errorf("expected v = 10 under C<, got %v", store.updatedVariablesPartial[0]["v"])
	}
}

// TestHandleDecisionTable_CPlus_NonNumeric_FailsAggregatorTypeError verifies
// that applying + to a non-numeric column fails with the expected prefix.
func TestHandleDecisionTable_CPlus_NonNumeric_FailsAggregatorTypeError(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{When: map[string]string{"a": "a == true"}, Outputs: map[string]json.RawMessage{"tag": json.RawMessage(`"foo"`)}},
		{When: map[string]string{"b": "b == true"}, Outputs: map[string]json.RawMessage{"tag": json.RawMessage(`"bar"`)}},
	}, "C+")
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": true, "b": true}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected failure under C+ on non-numeric column, got %d failures", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.HasPrefix(*got, DecisionTableAggregatorTypeError) {
		t.Fatalf("expected failure prefix %q, got %v", DecisionTableAggregatorTypeError, got)
	}
	if !strings.Contains(*got, "tag") {
		t.Errorf("expected failure message to name the column \"tag\", got %q", *got)
	}
}

// TestHandleDecisionTable_CHash_NonNumeric_Succeeds verifies that # (count)
// is type-agnostic and never raises DecisionTableAggregatorTypeError.
func TestHandleDecisionTable_CHash_NonNumeric_Succeeds(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{When: map[string]string{"a": "a == true"}, Outputs: map[string]json.RawMessage{"tag": json.RawMessage(`"foo"`)}},
		{When: map[string]string{"b": "b == true"}, Outputs: map[string]json.RawMessage{"tag": json.RawMessage(`"bar"`)}},
	}, "C#")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": true, "b": true}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Errorf("expected the instance to complete under C# on non-numeric column, got %d failures", len(store.failedInstances))
	}
}

// TestHandleDecisionTable_OutputsDoNotShadowInputCells verifies that input
// cells must be evaluated against the pre-step variable snapshot, even when
// the matched rule's outputs overwrite an input variable name.
//
// Setup: two rules. Rule 0 matches when score >= 700 and writes
// `score = 0` (overwriting the input). Rule 1 matches when score < 100. Under
// pre-step evaluation, rule 0 matches (score=720) and rule 1 does NOT match
// (score is read as 720, not the overwritten 0). The test exercises hit policy
// R (rule order, multi-match capable) so a leak from rule 0 → rule 1 would
// show up as two matches instead of one.
//
// Because R is stubbed, the assertion checks that the instance fails for the
// "stubbed policy" reason rather than for spurious multi-match behaviour —
// i.e. matching happens against pre-step variables.
func TestHandleDecisionTable_OutputsDoNotShadowInputCells(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When: map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{
				"score": json.RawMessage(`0`),
			},
		},
		{
			When:    map[string]string{"score": "score < 100"},
			Outputs: map[string]json.RawMessage{"tag": json.RawMessage(`"LOW"`)},
		},
	}, "F")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 720.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Under F, only rule 0 matches (against pre-step vars). The post-merge
	// vars carry score=0 but rule 1 was never re-evaluated.
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected the instance to complete; got %d completions and %d failures", len(store.completedInstances), len(store.failedInstances))
	}
	if got := len(store.updatedVariablesPartial); got != 1 {
		t.Fatalf("expected one outputs merge, got %d", got)
	}
	if v := store.updatedVariablesPartial[0]["score"]; v == nil {
		t.Errorf("expected the outputs patch to set score=0, got %v", store.updatedVariablesPartial[0])
	}
}

// ─── DECISION_TABLE handler tests (additional lifecycle coverage) ─────────────

// TestHandleDecisionTable_MissingPayload_FailsInstance verifies that a
// DECISION_TABLE step with a nil DecisionTable payload fails the instance
// rather than panicking or being silently skipped.
func TestHandleDecisionTable_MissingPayload_FailsInstance(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::dt-missing", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:        "dt",
				Type:      definition.StepTypeDecisionTable,
				HitPolicy: "U",
				NextStep:  "end",
				// DecisionTable intentionally nil.
			},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected the instance to fail when DecisionTable payload is missing, got %d failures", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.Contains(*got, "DecisionTable payload missing") {
		t.Errorf("expected failure reason to mention missing payload, got %v", got)
	}
}

// TestHandleDecisionTable_WildcardCell_MatchesAlways verifies that an
// empty/whitespace When cell is treated as a wildcard (does not need to
// evaluate against any variable).
func TestHandleDecisionTable_WildcardCell_MatchesAlways(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			// One real cell + one wildcard cell. The wildcard must not
			// short-circuit the match to false.
			When: map[string]string{
				"score":    "score >= 700",
				"wildcard": "   ",
			},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"GOLD"`)},
		},
	}, "U")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 720.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected instance to complete with wildcard cell matching, got %d completions and %d failures",
			len(store.completedInstances), len(store.failedInstances))
	}
	if store.updatedVariablesPartial[0]["tier"] != "GOLD" {
		t.Errorf("expected tier=GOLD, got %v", store.updatedVariablesPartial[0])
	}
}

// TestHandleDecisionTable_EmptyWhen_CatchAll verifies that a rule with an
// empty When map acts as a catch-all and matches every input.
func TestHandleDecisionTable_EmptyWhen_CatchAll(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"GOLD"`)},
		},
		{
			// Empty When → catch-all. Reached when the prior rule doesn't match.
			When:    map[string]string{},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"DEFAULT"`)},
		},
	}, "F")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 100.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected the catch-all rule to match and the instance to complete; got %d failures",
			len(store.failedInstances))
	}
	if store.updatedVariablesPartial[0]["tier"] != "DEFAULT" {
		t.Errorf("expected catch-all to produce tier=DEFAULT, got %v", store.updatedVariablesPartial[0])
	}
}

// TestHandleDecisionTable_CellError_NonBoolResult_Fails verifies that a When
// cell expression returning a non-bool fails the step with DecisionTableCellError.
func TestHandleDecisionTable_CellError_NonBoolResult_Fails(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			// "score + 1" evaluates to a float64 — not a bool.
			When:    map[string]string{"score": "score + 1"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"X"`)},
		},
	}, "U")
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 1.0}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected the instance to fail when a cell returns non-bool, got %d failures", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.HasPrefix(*got, DecisionTableCellError) {
		t.Errorf("expected failure prefix %q, got %v", DecisionTableCellError, got)
	}
}

// TestHandleDecisionTable_CellError_EvaluatorError_Fails verifies that a When
// cell whose expression raises an evaluator error (e.g. undefined variable)
// fails the step with DecisionTableCellError.
func TestHandleDecisionTable_CellError_EvaluatorError_Fails(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"x": "undefined_var > 0"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"X"`)},
		},
	}, "U")
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected the instance to fail on cell evaluator error, got %d failures", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.HasPrefix(*got, DecisionTableCellError) {
		t.Errorf("expected failure prefix %q, got %v", DecisionTableCellError, got)
	}
}

// TestHandleDecisionTable_OutputExpression_EvaluatesAgainstPreStepVars
// verifies that a "${expr}" output value is evaluated against the pre-step
// variable snapshot — matching TRANSFORMATION encoding.
func TestHandleDecisionTable_OutputExpression_EvaluatesAgainstPreStepVars(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When: map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{
				"doubled": json.RawMessage(`"${score * 2}"`),
			},
		},
	}, "U")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 800.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected the instance to complete, got %d failures", len(store.failedInstances))
	}
	if got := store.updatedVariablesPartial[0]["doubled"]; got != 1600.0 {
		t.Errorf("expected ${score*2} to evaluate to 1600, got %v (%T)", got, got)
	}
}

// TestHandleDecisionTable_OutputError_BadExpression_Fails verifies that a
// bad "${expr}" output expression fails the step with DecisionTableOutputError.
func TestHandleDecisionTable_OutputError_BadExpression_Fails(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When: map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{
				"v": json.RawMessage(`"${undefined_var * 2}"`),
			},
		},
	}, "U")
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 800.0}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected the instance to fail on output expression error, got %d failures", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.HasPrefix(*got, DecisionTableOutputError) {
		t.Errorf("expected failure prefix %q, got %v", DecisionTableOutputError, got)
	}
}

// TestHandleDecisionTable_DefaultPolicy_EmptyHitPolicy_BehavesAsU verifies
// that an empty HitPolicy string defaults to "U" semantics — two matching
// rules trigger DecisionTableUniqueViolation.
func TestHandleDecisionTable_DefaultPolicy_EmptyHitPolicy_BehavesAsU(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"score": "score >= 700"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"GOLD"`)},
		},
		{
			When:    map[string]string{"score": "score >= 600"},
			Outputs: map[string]json.RawMessage{"tier": json.RawMessage(`"SILVER"`)},
		},
	}, "") // empty HitPolicy → should default to "U"
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 720.0}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected default policy to behave as U (unique violation), got %d failures", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.HasPrefix(*got, DecisionTableUniqueViolation) {
		t.Errorf("expected failure prefix %q under default policy, got %v", DecisionTableUniqueViolation, got)
	}
}

// TestHandleDecisionTable_NoOutputs_AdvancesWithoutVariableUpdate verifies
// that a matching rule with no Outputs map still advances the instance to
// step.NextStep, and no variables-partial update is recorded.
func TestHandleDecisionTable_NoOutputs_AdvancesWithoutVariableUpdate(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When: map[string]string{"score": "score >= 700"},
			// No Outputs.
		},
	}, "U")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"score": 800.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected the instance to advance and complete, got %d failures", len(store.failedInstances))
	}
	if got := len(store.updatedVariablesPartial); got != 0 {
		t.Errorf("expected no variables-partial update for a no-outputs rule, got %d (%v)", got, store.updatedVariablesPartial)
	}
}

// ─── SERVICE_TASK handler tests ───────────────────────────────────────────────

// TestHandleServiceTask_CreatesJobAndDispatches verifies the happy path:
// a job row is inserted with the step's jobType and the dispatcher is invoked.
func TestHandleServiceTask_CreatesJobAndDispatches(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::svc", Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "do", Type: definition.StepTypeServiceTask, JobType: "doWork", RetryCount: 3},
		},
	}
	svc, store, _, disp := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := len(store.insertedJobs); got != 1 {
		t.Fatalf("expected 1 job inserted, got %d", got)
	}
	if !strings.HasSuffix(store.insertedJobs[0], "/doWork") {
		t.Errorf("expected job to carry jobType %q, got %q", "doWork", store.insertedJobs[0])
	}
	if got := len(disp.enqueued); got != 1 {
		t.Errorf("expected dispatcher.Enqueue called once, got %d", got)
	}
}

// TestHandleServiceTask_EmptyJobType_FallsBackToStepID verifies that a
// SERVICE_TASK with an empty JobType uses the step ID as the jobType.
func TestHandleServiceTask_EmptyJobType_FallsBackToStepID(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::svc-fallback", Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "doStep", Type: definition.StepTypeServiceTask},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := len(store.insertedJobs); got != 1 {
		t.Fatalf("expected 1 job inserted, got %d", got)
	}
	if !strings.HasSuffix(store.insertedJobs[0], "/doStep") {
		t.Errorf("expected jobType to fall back to step ID %q, got %q", "doStep", store.insertedJobs[0])
	}
}

// TestHandleServiceTask_SchedulesTimerBoundaryEvents verifies that a
// SERVICE_TASK with a TIMER boundary event inserts a boundary_event_schedule
// row alongside the job. Also exercises the shared scheduleBoundaryEvents
// helper used by USER_TASK and WAIT handlers.
func TestHandleServiceTask_SchedulesTimerBoundaryEvents(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::svc-boundary", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID: "do", Type: definition.StepTypeServiceTask, JobType: "work",
				BoundaryEvents: []definition.BoundaryEvent{
					{Type: definition.BoundaryEventTypeTimer, Duration: "PT30S", TargetStepId: "timeout"},
				},
			},
			{ID: "timeout", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := len(store.insertedBoundarySchedules); got != 1 {
		t.Errorf("expected 1 boundary_event_schedule row, got %d", got)
	}
}

// ─── WAIT handler test ───────────────────────────────────────────────────────

// TestHandleWait_SetsWaitingStatus verifies that a WAIT step transitions the
// instance to WAITING and does not advance.
func TestHandleWait_SetsWaitingStatus(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::wait", Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "park", Type: definition.StepTypeWait, NextStep: "end"},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if inst.Status != InstanceStatusWaiting {
		t.Errorf("expected returned status WAITING, got %q", inst.Status)
	}
	if got := store.instances[inst.ID].Status; got != InstanceStatusWaiting {
		t.Errorf("expected stored status WAITING, got %q", got)
	}
	if len(store.completedInstances) != 0 {
		t.Errorf("WAIT step must not auto-advance the instance to completion")
	}
}

// ─── PARALLEL_GATEWAY handler test ───────────────────────────────────────────

// TestHandleParallelGateway_FansOutAllBranches verifies that a parallel
// gateway dispatches every branch in ParallelNextSteps.
func TestHandleParallelGateway_FansOutAllBranches(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::pg", Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "pg", Type: definition.StepTypeParallelGateway, ParallelNextSteps: []string{"a", "b"}, JoinStep: "j"},
			{ID: "a", Type: definition.StepTypeServiceTask, JobType: "aJob", NextStep: "j"},
			{ID: "b", Type: definition.StepTypeServiceTask, JobType: "bJob", NextStep: "j"},
			{ID: "j", Type: definition.StepTypeJoinGateway, NextStep: "end"},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Both branches must have inserted a job.
	if got := len(store.insertedJobs); got != 2 {
		t.Fatalf("expected 2 jobs from the two parallel branches, got %d (%v)", got, store.insertedJobs)
	}
	gotJobTypes := map[string]bool{}
	for _, entry := range store.insertedJobs {
		if i := strings.Index(entry, "/"); i >= 0 {
			gotJobTypes[entry[i+1:]] = true
		}
	}
	if !gotJobTypes["aJob"] || !gotJobTypes["bJob"] {
		t.Errorf("expected jobs for both branches (aJob, bJob), got %v", gotJobTypes)
	}
	// The gateway itself should have been completed.
	foundPGCompletion := false
	for _, key := range store.completedStepExecsByStep {
		if strings.HasSuffix(key, ":pg") {
			foundPGCompletion = true
			break
		}
	}
	if !foundPGCompletion {
		t.Errorf("expected the parallel gateway step_execution to be completed, got %v", store.completedStepExecsByStep)
	}
}

// ─── END handler test ────────────────────────────────────────────────────────

// TestHandleEnd_CompletesInstance verifies that an END step marks the
// instance COMPLETED.
func TestHandleEnd_CompletesInstance(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::end", Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected the END step to complete the instance, got %d completions", len(store.completedInstances))
	}
	if got := store.instances[inst.ID].Status; got != InstanceStatusCompleted {
		t.Errorf("expected stored status COMPLETED, got %q", got)
	}
}

// ─── DECISION handler edge-case tests ────────────────────────────────────────

// TestHandleDecision_NonBoolExpression_FailsInstance verifies that a DECISION
// branch whose expression returns a non-bool value fails the instance.
func TestHandleDecision_NonBoolExpression_FailsInstance(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::decision-nonbool", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:                   "decide",
				Type:                 definition.StepTypeDecision,
				ConditionalNextSteps: definition.NewConditionalBranches("x + 1", "neverReached"),
			},
			{ID: "neverReached", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"x": 1.0}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected the instance to fail on a non-bool DECISION expression, got %d failures", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.Contains(*got, "not bool") {
		t.Errorf("expected failure reason to mention non-bool result, got %v", got)
	}
}

// ─── TRANSFORMATION handler edge-case tests ──────────────────────────────────

// TestHandleTransformation_NowExpression_ProducesRFC3339 verifies that the
// special "${now()}" expression resolves to a timestamp parseable as RFC3339.
func TestHandleTransformation_NowExpression_ProducesRFC3339(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::xform-now", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:       "xform",
				Type:     definition.StepTypeTransformation,
				NextStep: "end",
				Transformations: map[string]json.RawMessage{
					"stampedAt": json.RawMessage(`"${now()}"`),
				},
			},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := len(store.updatedVariablesPartial); got != 1 {
		t.Fatalf("expected one variables-partial update, got %d", got)
	}
	raw, ok := store.updatedVariablesPartial[0]["stampedAt"].(string)
	if !ok {
		t.Fatalf("expected stampedAt to be a string, got %T", store.updatedVariablesPartial[0]["stampedAt"])
	}
	if _, err := time.Parse(time.RFC3339, raw); err != nil {
		t.Errorf("expected ${now()} to produce an RFC3339 timestamp, got %q (parse err: %v)", raw, err)
	}
}

// TestHandleTransformation_BadExpression_FailsInstance verifies that a
// transformation whose ${expr} fails evaluation fails the instance via
// failInstance (not by returning the error up through Start).
func TestHandleTransformation_BadExpression_FailsInstance(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::xform-bad", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:       "xform",
				Type:     definition.StepTypeTransformation,
				NextStep: "end",
				Transformations: map[string]json.RawMessage{
					"v": json.RawMessage(`"${undefined_var * 2}"`),
				},
			},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected the instance to fail on bad transformation expression, got %d failures", len(store.failedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.Contains(*got, "expression eval") {
		t.Errorf("expected failure reason to mention expression eval error, got %v", got)
	}
}

// ─── routeStep default-case test ─────────────────────────────────────────────

// TestRouteStep_UnsupportedStepType_ReturnsError verifies that an unknown
// step type surfaces an "unsupported step type" error out of dispatchStep.
func TestRouteStep_UnsupportedStepType_ReturnsError(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::bad-type", Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "bad", Type: definition.StepType("UNKNOWN_TYPE")},
		},
	}
	svc, _, _, _ := newServiceForTest(def)
	_, err := svc.Start(context.Background(), def.ID, 1, map[string]any{}, "")
	if err == nil {
		t.Fatalf("expected Start to surface an error for an unsupported step type")
	}
	if !strings.Contains(err.Error(), "unsupported step type") {
		t.Errorf("expected error to mention \"unsupported step type\", got %q", err.Error())
	}
}

// ─── spec-verification tests (docs/workflow-format.md) ───────────────────────

// Spec (Error behaviour table, docs/workflow-format.md): "No branch matches in
// DECISION → Step fails with DecisionNoBranchMatched". Verify the failure
// reason carries the documented token, not just that the instance fails.
func TestHandleDecision_NoBranchMatched_FailureCarriesDocumentedToken(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::dec-no-match-token", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:                   "decide",
				Type:                 definition.StepTypeDecision,
				ConditionalNextSteps: definition.NewConditionalBranches("x > 100", "end"),
			},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"x": 1.0}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.Contains(*got, "DecisionNoBranchMatched") {
		t.Errorf("expected failure reason to carry the spec token %q, got %v", "DecisionNoBranchMatched", got)
	}
}

// Spec (TRANSFORMATION, docs/workflow-format.md line 275): "Expressions can
// return any type — numbers, strings, booleans — and the result is assigned
// directly to the variable." Existing tests only cover strings; verify number
// and boolean returns.
func TestHandleTransformation_ExpressionReturnsBoolean(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::xform-bool", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:       "xform",
				Type:     definition.StepTypeTransformation,
				NextStep: "end",
				Transformations: map[string]json.RawMessage{
					"isHighValue": json.RawMessage(`"${loanAmount > 100000}"`),
				},
			},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"loanAmount": 150000.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, ok := store.updatedVariablesPartial[0]["isHighValue"].(bool)
	if !ok {
		t.Fatalf("expected isHighValue to be a bool, got %T", store.updatedVariablesPartial[0]["isHighValue"])
	}
	if !got {
		t.Errorf("expected isHighValue=true for loanAmount=150000, got false")
	}
}

func TestHandleTransformation_ExpressionReturnsNumber(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::xform-num", Version: 1,
		Steps: []definition.WorkflowStep{
			{
				ID:       "xform",
				Type:     definition.StepTypeTransformation,
				NextStep: "end",
				Transformations: map[string]json.RawMessage{
					"totalWithFee": json.RawMessage(`"${loanAmount + 50}"`),
				},
			},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"loanAmount": 1000.0}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, ok := store.updatedVariablesPartial[0]["totalWithFee"].(float64)
	if !ok {
		t.Fatalf("expected totalWithFee to be a float64, got %T", store.updatedVariablesPartial[0]["totalWithFee"])
	}
	if got != 1050.0 {
		t.Errorf("expected totalWithFee=1050, got %v", got)
	}
}

// Spec (DECISION_TABLE runtime behaviour, docs/workflow-format.md line 217):
// non-boolean cell result fails with a DecisionTableCellError "naming the rule
// index and column". Existing TestRuleMatches_NonBoolResult_ReturnsCellError
// verifies the rule index; this one verifies the column name is present.
func TestRuleMatches_NonBoolError_NamesColumn(t *testing.T) {
	SetExpressionEvaluator(expression.Evaluate)
	rule := &definition.DecisionTableRule{
		When: map[string]string{"myColumn": "x + 1"}, // returns float64
	}
	_, err := ruleMatches(rule, map[string]any{"x": 1.0}, "step", 0)
	if err == nil {
		t.Fatalf("expected error for non-bool cell result")
	}
	if !strings.Contains(err.Error(), `column="myColumn"`) {
		t.Errorf("expected error to name column \"myColumn\", got %q", err.Error())
	}
}

// Spec (DECISION_TABLE §3, docs/workflow-format.md line 210): "A rule that
// omits a column declared by other matched rules contributes null to that
// column's list. Under +/>/< this surfaces as a DecisionTableAggregatorTypeError."
func TestHandleDecisionTable_CPlus_OmittedColumn_FailsAggregatorTypeError(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"a": "a == true"},
			Outputs: map[string]json.RawMessage{"fee": json.RawMessage(`25`)},
		},
		{
			When: map[string]string{"b": "b == true"},
			// Omits "fee" → contributes nil to fee's list.
			Outputs: map[string]json.RawMessage{"otherCol": json.RawMessage(`"x"`)},
		},
	}, "C+")
	svc, store, _, _ := newServiceForTest(def)
	inst, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": true, "b": true}, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.failedInstances) != 1 {
		t.Fatalf("expected failure when C+ sees nil from omitted column, got %d failures and %d completions",
			len(store.failedInstances), len(store.completedInstances))
	}
	got := store.instances[inst.ID].FailureReason
	if got == nil || !strings.HasPrefix(*got, DecisionTableAggregatorTypeError) {
		t.Errorf("expected failure prefix %q, got %v", DecisionTableAggregatorTypeError, got)
	}
}

// Spec (Expression Reference, docs/workflow-format.md): "Expressions are
// evaluated in the order they appear in the JSON object." Build a workflow
// via Parse() (the canonical REST upload path) where multiple branches
// match, and verify the *first-in-document-order* branch is the one
// dispatched. With a buggy map-iteration implementation the chosen branch
// would be non-deterministic — 1/5 chance of being correct per run.
func TestHandleDecision_EvaluatesInDocumentOrder(t *testing.T) {
	const src = `{
		"id": "test::decision-order",
		"name": "Decision Order",
		"steps": [
			{
				"id": "decide",
				"name": "Decide",
				"type": "DECISION",
				"conditionalNextSteps": {
					"x > 0":   "first",
					"x > 10":  "second",
					"x > 50":  "third",
					"x > 100": "fourth",
					"x > 200": "fifth"
				}
			},
			{"id": "first",  "name": "First",  "type": "END"},
			{"id": "second", "name": "Second", "type": "END"},
			{"id": "third",  "name": "Third",  "type": "END"},
			{"id": "fourth", "name": "Fourth", "type": "END"},
			{"id": "fifth",  "name": "Fifth",  "type": "END"}
		]
	}`
	def, err := definition.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Repeat the run to make the test deterministic even if order were
	// random — with the fix in place, every run must pick "first".
	for i := 0; i < 20; i++ {
		svc, store, _, _ := newServiceForTest(def)
		// x = 500 makes EVERY branch match. The expression at insertion
		// position 0 ("x > 0", target "first") must win.
		if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"x": 500.0}, ""); err != nil {
			t.Fatalf("iteration %d: Start: %v", i, err)
		}
		// Inspect which END step received a step_execution row. The DECISION
		// dispatches exactly one target; if order is preserved, it's "first".
		dispatched := map[string]bool{}
		for _, key := range store.stepExecsByID {
			if idx := strings.LastIndex(key, ":"); idx >= 0 {
				dispatched[key[idx+1:]] = true
			}
		}
		if !dispatched["first"] {
			t.Fatalf("iteration %d: expected branch %q (document-first) to be dispatched, dispatched steps=%v",
				i, "first", dispatched)
		}
		for _, late := range []string{"second", "third", "fourth", "fifth"} {
			if dispatched[late] {
				t.Errorf("iteration %d: branch %q was dispatched but %q (document-first) should win",
					i, late, "first")
			}
		}
	}
}

// Spec (DECISION_TABLE §3, docs/workflow-format.md line 210): "under # the
// count is unaffected" by rules omitting columns.
func TestHandleDecisionTable_CHash_OmittedColumns_StillCountsAllMatches(t *testing.T) {
	def := dtDef([]definition.DecisionTableRule{
		{
			When:    map[string]string{"a": "a == true"},
			Outputs: map[string]json.RawMessage{"colA": json.RawMessage(`"x"`)},
		},
		{
			When:    map[string]string{"b": "b == true"},
			Outputs: map[string]json.RawMessage{"colB": json.RawMessage(`"y"`)},
		},
		{
			When:    map[string]string{"c": "c == true"},
			Outputs: map[string]json.RawMessage{"colC": json.RawMessage(`"z"`)},
		},
	}, "C#")
	svc, store, _, _ := newServiceForTest(def)
	if _, err := svc.Start(context.Background(), def.ID, 1, map[string]any{"a": true, "b": true, "c": true}, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(store.completedInstances) != 1 {
		t.Fatalf("expected the instance to complete under C#, got %d failures", len(store.failedInstances))
	}
	patch := store.updatedVariablesPartial[0]
	// Under C#, every output column receives the count of matched rules (3),
	// regardless of whether that column was actually produced by every rule.
	for _, col := range []string{"colA", "colB", "colC"} {
		got, ok := patch[col].(int)
		if !ok {
			t.Errorf("%s: expected int count, got %T (%v)", col, patch[col], patch[col])
			continue
		}
		if got != 3 {
			t.Errorf("%s: expected count=3 (all matches), got %d", col, got)
		}
	}
}
