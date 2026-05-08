package scenarios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RunDecisionTableWithDecision exercises the canonical DECISION_TABLE +
// DECISION pairing pattern
//
//	seed (TRANSFORMATION)
//	   → classify-tier (DECISION_TABLE, hitPolicy=F, nextStep=route-by-tier)
//	   → route-by-tier (DECISION on `tier`)
//	   → {gold-end | silver-end | bronze-end}
//
// It uploads e2e/scenarios/shared/decision-table-with-decision.json once and
// starts three instances seeded with creditScore=760 / 720 / 650 (the
// fixture's seed step's transformations are deliberately overwritten by
// instance variables so each instance lands in a different tier branch).
//
// For each band the scenario asserts:
//   - the instance reaches COMPLETED,
//   - the variable map carries the expected `tier` and `feePercent`.
//
// The scenario does not depend on any SDK worker, so it runs identically in
// every dispatch mode. The prefix parameter is accepted for signature
// compatibility with the runner suite but is not used to locate the
// fixture — the fixture lives under `shared/`.
func RunDecisionTableWithDecision(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, "shared", "decision-table-with-decision.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload decision-table-with-decision definition: %v", err)
		return
	}

	type band struct {
		creditScore int
		tier        string
		feePercent  float64
	}
	bands := []band{
		{creditScore: 760, tier: "GOLD", feePercent: 0.5},
		{creditScore: 720, tier: "SILVER", feePercent: 0.7},
		{creditScore: 650, tier: "BRONZE", feePercent: 1.0},
	}

	for _, b := range bands {
		vars := map[string]any{
			"creditScore": b.creditScore,
			"loanAmount":  150000,
		}
		instanceID, err := client.StartInstance(ctx, "LOS::loan-tier-routing-007", vars)
		if err != nil {
			t.Errorf("[shared/decision-table-with-decision/%s] start: %v", b.tier, err)
			continue
		}
		LogInstanceStarted("LOS::loan-tier-routing-007", instanceID, vars)
		t.Logf("[shared/decision-table-with-decision/%s] instance %s started", b.tier, instanceID)

		inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
		if err != nil {
			t.Errorf("[shared/decision-table-with-decision/%s] poll timeout: %v", b.tier, err)
			continue
		}
		if inst.Status != "COMPLETED" {
			t.Errorf("[shared/decision-table-with-decision/%s] want COMPLETED, got %s (failureReason: %q)", b.tier, inst.Status, inst.FailureReason)
			continue
		}
		gotTier, _ := inst.Variables["tier"].(string)
		if gotTier != b.tier {
			t.Errorf("[shared/decision-table-with-decision/%s] want tier=%s, got %v (vars=%v)", b.tier, b.tier, inst.Variables["tier"], inst.Variables)
			continue
		}
		gotFee, ok := inst.Variables["feePercent"].(float64)
		if !ok || gotFee != b.feePercent {
			t.Errorf("[shared/decision-table-with-decision/%s] want feePercent=%v, got %v", b.tier, b.feePercent, inst.Variables["feePercent"])
			continue
		}
		t.Logf("[shared/decision-table-with-decision/%s] %s ✓", b.tier, fmt.Sprintf("tier=%s feePercent=%v", gotTier, gotFee))
	}
}
