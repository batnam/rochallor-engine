package definition_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// validDecisionTableDef returns a minimal valid workflow under the 007 wire
// format: a DECISION_TABLE with a step-level nextStep, advancing linearly to
// an END step. The rule has an empty When map (catch-all).
func validDecisionTableDef() *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		ID:   "LOS::dt-test",
		Name: "Decision Table Test",
		Steps: []definition.WorkflowStep{
			{
				ID:        "start",
				Name:      "Start",
				Type:      definition.StepTypeDecisionTable,
				HitPolicy: "U",
				NextStep:  "end",
				DecisionTable: &definition.DecisionTable{
					Rules: []definition.DecisionTableRule{
						{When: map[string]string{}},
					},
				},
			},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
}

func TestValidateDecisionTable_HappyPath(t *testing.T) {
	if err := definition.Validate(validDecisionTableDef()); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateDecisionTable_DefaultHitPolicy(t *testing.T) {
	// Omitting hitPolicy must be accepted; the engine defaults to "U" at runtime.
	d := validDecisionTableDef()
	d.Steps[0].HitPolicy = ""
	if err := definition.Validate(d); err != nil {
		t.Errorf("expected no error when hitPolicy is omitted, got: %v", err)
	}
}

func TestValidateDecisionTable_EmptyRules(t *testing.T) {
	d := validDecisionTableDef()
	d.Steps[0].DecisionTable.Rules = nil
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for empty rules")
	}
	if !strings.Contains(err.Error(), "rules must not be empty") {
		t.Errorf("error must name the empty-rules condition, got: %v", err)
	}
	if !strings.Contains(err.Error(), `"start"`) {
		t.Errorf("error must name the offending step id, got: %v", err)
	}
}

func TestValidateDecisionTable_MissingPayload(t *testing.T) {
	d := validDecisionTableDef()
	d.Steps[0].DecisionTable = nil
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for missing payload")
	}
	if !strings.Contains(err.Error(), "decisionTable payload is required") {
		t.Errorf("error must name the missing-payload condition, got: %v", err)
	}
}

// V3 — nextStep is required on DECISION_TABLE.
func TestValidateDecisionTable_MissingNextStep(t *testing.T) {
	d := validDecisionTableDef()
	d.Steps[0].NextStep = ""
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for missing nextStep")
	}
	if !strings.Contains(err.Error(), "nextStep is required") {
		t.Errorf("error must name the missing nextStep, got: %v", err)
	}
}

// V4 — nextStep must resolve to an existing step.
func TestValidateDecisionTable_DanglingNextStep(t *testing.T) {
	d := validDecisionTableDef()
	d.Steps[0].NextStep = "no-such-step"
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for dangling nextStep")
	}
	if !strings.Contains(err.Error(), "unknown step") || !strings.Contains(err.Error(), "no-such-step") {
		t.Errorf("error must name the dangling target, got: %v", err)
	}
}

// V5 — hitPolicy must be one of the recognised values.
func TestValidateDecisionTable_UnrecognisedHitPolicy(t *testing.T) {
	d := validDecisionTableDef()
	d.Steps[0].HitPolicy = "X"
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for unrecognised hitPolicy")
	}
	if !strings.Contains(err.Error(), `hitPolicy "X"`) || !strings.Contains(err.Error(), "not recognised") {
		t.Errorf("error must name the unrecognised hitPolicy, got: %v", err)
	}
}

// V5 — all nine canonical hit-policy values are accepted.
func TestValidateDecisionTable_AllCanonicalHitPolicies(t *testing.T) {
	for _, hp := range []string{"U", "F", "A", "R", "C", "C+", "C#", "C>", "C<"} {
		hp := hp
		t.Run(hp, func(t *testing.T) {
			d := validDecisionTableDef()
			d.Steps[0].HitPolicy = hp
			if err := definition.Validate(d); err != nil {
				t.Errorf("expected hitPolicy %q to be accepted, got: %v", hp, err)
			}
		})
	}
}

// V6 — aggregator suffix is only valid on hitPolicy "C".
func TestValidateDecisionTable_AggregatorOnlyOnCollect(t *testing.T) {
	d := validDecisionTableDef()
	d.Steps[0].HitPolicy = "F+"
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for aggregator on non-Collect policy")
	}
	if !strings.Contains(err.Error(), "F+") {
		t.Errorf("error must name the offending hitPolicy, got: %v", err)
	}
}

// V8 — presence of legacy `then` on a rule is rejected with a migration
// message. The struct itself no longer has a Then field, so this is enforced
// at parse time via the strict decoder.
func TestValidateDecisionTable_LegacyThenRejected(t *testing.T) {
	raw := []byte(`{
		"id":"LOS::dt-legacy-then",
		"name":"DT legacy then",
		"steps":[
			{"id":"start","name":"Start","type":"DECISION_TABLE","hitPolicy":"U","nextStep":"end",
			 "decisionTable":{"rules":[{"when":{},"then":"end"}]}},
			{"id":"end","name":"End","type":"END"}
		]
	}`)
	_, err := definition.ParseBytes(raw)
	if err == nil {
		t.Fatal("expected parse error for legacy `then` field")
	}
	msg := err.Error()
	if !strings.Contains(msg, "then") || !strings.Contains(msg, "no longer supported") {
		t.Errorf("error must point to the 005→007 migration for `then`, got: %v", err)
	}
}

// V9 — presence of legacy `defaultNextStep` on the decisionTable is rejected
// with a migration message. Enforced at parse time.
func TestValidateDecisionTable_LegacyDefaultNextStepRejected(t *testing.T) {
	raw := []byte(`{
		"id":"LOS::dt-legacy-dns",
		"name":"DT legacy default",
		"steps":[
			{"id":"start","name":"Start","type":"DECISION_TABLE","hitPolicy":"U","nextStep":"end",
			 "decisionTable":{"rules":[{"when":{}}],"defaultNextStep":"end"}},
			{"id":"end","name":"End","type":"END"}
		]
	}`)
	_, err := definition.ParseBytes(raw)
	if err == nil {
		t.Fatal("expected parse error for legacy `defaultNextStep` field")
	}
	msg := err.Error()
	if !strings.Contains(msg, "defaultNextStep") || !strings.Contains(msg, "no longer supported") {
		t.Errorf("error must point to the 005→007 migration for `defaultNextStep`, got: %v", err)
	}
}

func TestValidateDecisionTable_ForbiddenForeignField(t *testing.T) {
	d := validDecisionTableDef()
	// A DECISION_TABLE step carrying a TRANSFORMATION-style field — must be rejected.
	d.Steps[0].Transformations = map[string]json.RawMessage{"x": json.RawMessage(`1`)}
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for foreign transformations field on DECISION_TABLE")
	}
	if !strings.Contains(err.Error(), "transformations is not valid on this step type") {
		t.Errorf("error must name the forbidden field, got: %v", err)
	}
}

func TestValidateDecisionTable_DecisionTableOnNonDecisionTableStep(t *testing.T) {
	// Symmetric guard: a SERVICE_TASK step carrying a DecisionTable payload is rejected.
	d := validLoanDef()
	d.Steps[0].DecisionTable = &definition.DecisionTable{
		Rules: []definition.DecisionTableRule{{When: nil}},
	}
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for DecisionTable on non-DECISION_TABLE step")
	}
	if !strings.Contains(err.Error(), "decisionTable is not valid on this step type") {
		t.Errorf("error must name the foreign decisionTable, got: %v", err)
	}
}

func TestValidateDecisionTable_BadOutputJSON(t *testing.T) {
	d := validDecisionTableDef()
	d.Steps[0].DecisionTable.Rules[0].Outputs = map[string]json.RawMessage{
		"k": json.RawMessage(`{this is not json`),
	}
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for malformed output JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON value") {
		t.Errorf("error must name the malformed output, got: %v", err)
	}
}

// V11 — reachability: the table's single outbound edge (step.NextStep) must
// participate in the graph walk so steps reachable only via the table aren't
// flagged as orphans.
func TestValidateDecisionTable_NextStepReachable(t *testing.T) {
	d := &definition.WorkflowDefinition{
		ID:   "LOS::dt-reach",
		Name: "DT Reachability",
		Steps: []definition.WorkflowStep{
			{
				ID:        "start",
				Name:      "Start",
				Type:      definition.StepTypeDecisionTable,
				HitPolicy: "U",
				NextStep:  "tail",
				DecisionTable: &definition.DecisionTable{
					Rules: []definition.DecisionTableRule{{When: nil}},
				},
			},
			{ID: "tail", Name: "Tail", Type: definition.StepTypeEnd},
		},
	}
	if err := definition.Validate(d); err != nil {
		t.Errorf("expected no error — DT nextStep should be reachable, got: %v", err)
	}
}
