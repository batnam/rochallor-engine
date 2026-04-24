package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunRetryFail uploads the retry-fail workflow for prefix, starts an instance,
// and asserts it reaches COMPLETED within 20 seconds.
// The flaky handler fails on the first attempt and succeeds on the second.
func RunRetryFail(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "retry-fail.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload retry-fail definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-retry-fail", nil)
	if err != nil {
		t.Errorf("start retry-fail instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/retry-fail] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-retry-fail", instanceID, nil)

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/retry-fail] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/retry-fail] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
	} else {
		t.Logf("[%s/retry-fail] COMPLETED ✓", prefix)
	}
}
