package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunDecisionNoMatch uploads the decision-no-match workflow for prefix, starts an instance,
// and asserts it reaches FAILED when no DECISION branch condition is satisfied.
func RunDecisionNoMatch(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "decision-no-match.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload decision-no-match definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-decision-no-match", nil)
	if err != nil {
		t.Errorf("start decision-no-match instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/decision-no-match] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-decision-no-match", instanceID, nil)

	inst, err := PollUntilTerminal(ctx, client, instanceID, 15*time.Second)
	if err != nil {
		t.Errorf("[%s/decision-no-match] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "FAILED" {
		t.Errorf("[%s/decision-no-match] want FAILED, got %s", prefix, inst.Status)
		return
	}
	if !strings.Contains(inst.FailureReason, "DecisionNoBranchMatched") {
		t.Errorf("[%s/decision-no-match] want failureReason containing %q, got %q", prefix, "DecisionNoBranchMatched", inst.FailureReason)
		return
	}
	t.Logf("[%s/decision-no-match] FAILED with DecisionNoBranchMatched ✓", prefix)
}
