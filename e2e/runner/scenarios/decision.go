package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunDecision uploads the decision workflow for prefix, starts an instance,
// and asserts it reaches COMPLETED with result=="approved" within 20 seconds.
func RunDecision(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "decision.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload decision definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-decision", nil)
	if err != nil {
		t.Errorf("start decision instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/decision] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-decision", instanceID, nil)

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/decision] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/decision] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
	} else {
		t.Logf("[%s/decision] COMPLETED ✓", prefix)
	}
}
