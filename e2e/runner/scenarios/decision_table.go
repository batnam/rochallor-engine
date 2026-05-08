package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunDecisionTable uploads the per-language decision-table fixture for
// prefix, starts an instance (no input variables — the fixture's first
// TRANSFORMATION step seeds them deterministically), and asserts that the
// instance reaches COMPLETED at the GOLD branch with tier="GOLD" and
// feePercent set. Under the 007 wire format the DECISION_TABLE's per-rule
// `outputs` populate the variable map and the linear `nextStep` advances to
// a downstream DECISION step that routes by `tier`. The scenario does not
// depend on any SDK worker — both the seeding and the routing happen
// entirely inside the engine, so the scenario runs identically in every
// dispatch mode.
func RunDecisionTable(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "decision-table.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload decision-table definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-decision-table", nil)
	if err != nil {
		t.Errorf("start decision-table instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/decision-table] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-decision-table", instanceID, nil)

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/decision-table] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/decision-table] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
		return
	}

	// Assert the matched rule's outputs landed on the instance variable map.
	// The fixture seeds score=720 and amount=30000000, which falls into the
	// GOLD rule (score >= 700, score < 750). That rule sets tier="GOLD".
	gotTier, _ := inst.Variables["tier"].(string)
	if gotTier != "GOLD" {
		t.Errorf("[%s/decision-table] want tier=GOLD, got %v (vars=%v)", prefix, inst.Variables["tier"], inst.Variables)
		return
	}
	// feePercent should be a number; JSON unmarshal makes it float64.
	if _, ok := inst.Variables["feePercent"].(float64); !ok {
		t.Errorf("[%s/decision-table] feePercent missing or not a number: %v (vars=%v)", prefix, inst.Variables["feePercent"], inst.Variables)
		return
	}

	t.Logf("[%s/decision-table] COMPLETED tier=GOLD feePercent=%v ✓", prefix, inst.Variables["feePercent"])
}
