package definition_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// fixtureDir is the path to the copied legacy fixtures.
const fixtureDir = "../../test/fixtures"

func TestParseAllLegacyFixtures(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Skip("no fixtures found at " + fixtureDir)
	}

	for _, path := range fixtures {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer f.Close()

			def, err := definition.Parse(f)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if def.ID == "" {
				t.Errorf("id is empty")
			}
			if len(def.Steps) == 0 {
				t.Errorf("steps is empty")
			}

			// Round-trip: re-serialize and re-parse
			data, err := def.MarshalJSON()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			def2, err := definition.ParseBytes(data)
			if err != nil {
				t.Fatalf("re-parse: %v", err)
			}
			if def2.ID != def.ID {
				t.Errorf("round-trip id mismatch: want %q, got %q", def.ID, def2.ID)
			}
			if len(def2.Steps) != len(def.Steps) {
				t.Errorf("round-trip steps len: want %d, got %d", len(def.Steps), len(def2.Steps))
			}
		})
	}
}

// TestParseDecisionTable007Shape asserts that a definition matching the 007
// wire format (specs/007-decision-table-outputs/contracts/examples/
// first-with-decision.json) round-trips through definition.Parse without
// losing any of the new fields (step-level hitPolicy, step-level nextStep on
// a DECISION_TABLE, per-rule outputs) and that no removed 005 fields appear
// in the marshalled output.
func TestParseDecisionTable007Shape(t *testing.T) {
	src := []byte(`{
  "id": "LOS::loan-tier-routing-007",
  "name": "Loan Tier Routing — 007 canonical pattern",
  "steps": [
    {
      "id": "classify-tier",
      "name": "Classify Tier",
      "type": "DECISION_TABLE",
      "hitPolicy": "F",
      "nextStep": "route-by-tier",
      "decisionTable": {
        "rules": [
          { "when": { "creditScore": "creditScore >= 750" }, "outputs": { "tier": "GOLD",   "feePercent": 0.5 } },
          { "when": { "creditScore": "creditScore >= 700" }, "outputs": { "tier": "SILVER", "feePercent": 0.7 } },
          { "when": {},                                       "outputs": { "tier": "BRONZE", "feePercent": 1.0 } }
        ]
      }
    },
    { "id": "route-by-tier", "name": "Route", "type": "DECISION",
      "conditionalNextSteps": { "tier == \"GOLD\"": "gold-end", "tier == \"SILVER\"": "silver-end", "tier == \"BRONZE\"": "bronze-end" } },
    { "id": "gold-end",   "name": "Gold END",   "type": "END" },
    { "id": "silver-end", "name": "Silver END", "type": "END" },
    { "id": "bronze-end", "name": "Bronze END", "type": "END" }
  ]
}`)

	def, err := definition.ParseBytes(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// ── Step-level new fields on the DECISION_TABLE ─────────────────────
	dt := def.Steps[0]
	if dt.Type != definition.StepTypeDecisionTable {
		t.Fatalf("step[0].type: want DECISION_TABLE, got %q", dt.Type)
	}
	if dt.HitPolicy != "F" {
		t.Errorf("step[0].hitPolicy: want %q, got %q", "F", dt.HitPolicy)
	}
	if dt.NextStep != "route-by-tier" {
		t.Errorf("step[0].nextStep: want %q, got %q", "route-by-tier", dt.NextStep)
	}
	if dt.DecisionTable == nil {
		t.Fatal("step[0].decisionTable: nil")
	}
	if len(dt.DecisionTable.Rules) != 3 {
		t.Fatalf("step[0].decisionTable.rules: want 3, got %d", len(dt.DecisionTable.Rules))
	}
	for i, r := range dt.DecisionTable.Rules {
		if len(r.Outputs) != 2 {
			t.Errorf("rules[%d].outputs: want 2 keys, got %d", i, len(r.Outputs))
		}
	}

	// ── Round-trip: re-marshal and re-parse, expect structural equality ─
	out, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Marshalled form MUST NOT carry the removed 005 fields anywhere.
	for _, banned := range [][]byte{[]byte(`"defaultNextStep"`), []byte(`"then"`)} {
		if bytes.Contains(out, banned) {
			t.Errorf("marshalled JSON unexpectedly contains removed 005 field %s: %s", banned, string(out))
		}
	}

	def2, err := definition.ParseBytes(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if def2.Steps[0].HitPolicy != "F" {
		t.Errorf("round-trip step[0].hitPolicy: want %q, got %q", "F", def2.Steps[0].HitPolicy)
	}
	if def2.Steps[0].NextStep != "route-by-tier" {
		t.Errorf("round-trip step[0].nextStep: want %q, got %q", "route-by-tier", def2.Steps[0].NextStep)
	}
	if len(def2.Steps[0].DecisionTable.Rules) != 3 {
		t.Errorf("round-trip rules len: want 3, got %d", len(def2.Steps[0].DecisionTable.Rules))
	}
}
