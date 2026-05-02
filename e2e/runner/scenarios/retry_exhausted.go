package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunRetryExhausted uploads the retry-exhausted workflow for prefix, starts an instance,
// and asserts it reaches FAILED after all retry attempts are consumed.
func RunRetryExhausted(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "retry-exhausted.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload retry-exhausted definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-retry-exhausted", nil)
	if err != nil {
		t.Errorf("start retry-exhausted instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/retry-exhausted] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-retry-exhausted", instanceID, nil)

	inst, err := PollUntilTerminal(ctx, client, instanceID, 30*time.Second)
	if err != nil {
		t.Errorf("[%s/retry-exhausted] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "FAILED" {
		t.Errorf("[%s/retry-exhausted] want FAILED, got %s", prefix, inst.Status)
		return
	}
	if inst.FailureReason == "" {
		t.Errorf("[%s/retry-exhausted] want non-empty failureReason, got empty string")
		return
	}
	t.Logf("[%s/retry-exhausted] FAILED with reason %q ✓", prefix, inst.FailureReason)
}
