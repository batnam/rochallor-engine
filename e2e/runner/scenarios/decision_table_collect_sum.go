package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunDecisionTableCollectSum exercises hit policy C+ end-to-end. It uploads
// e2e/scenarios/shared/decision-table-collect-sum.json (a TRANSFORMATION
// seeding wireTransfer=true, internationalTx=true, amount=15000,
// expedited=false; the DECISION_TABLE matches three rules out of four and
// sums their `fee` outputs to 50.0). The scenario asserts the instance
// completes and the final variable map carries fee=50.0. No SDK worker is
// involved, so the scenario runs identically in every dispatch mode.
func RunDecisionTableCollectSum(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, "shared", "decision-table-collect-sum.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload decision-table-collect-sum definition: %v", err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "LOS::applicable-fees-total-007", nil)
	if err != nil {
		t.Errorf("[shared/decision-table-collect-sum] start: %v", err)
		return
	}
	LogInstanceStarted("LOS::applicable-fees-total-007", instanceID, nil)
	t.Logf("[shared/decision-table-collect-sum] instance %s started", instanceID)

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[shared/decision-table-collect-sum] poll timeout: %v", err)
		return
	}
	if inst.Status != "COMPLETED" {
		t.Errorf("[shared/decision-table-collect-sum] want COMPLETED, got %s (failureReason: %q)", inst.Status, inst.FailureReason)
		return
	}
	fee, ok := inst.Variables["fee"].(float64)
	if !ok || fee != 50.0 {
		t.Errorf("[shared/decision-table-collect-sum] want fee=50.0, got %v (vars=%v)", inst.Variables["fee"], inst.Variables)
		return
	}
	t.Logf("[shared/decision-table-collect-sum] fee=50.0 ✓")
}
