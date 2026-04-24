package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunTimer uploads the timer boundary workflow for prefix, starts an instance,
// and asserts it reaches COMPLETED within 30 seconds (timer fires after ~2s).
func RunTimer(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "timer.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload timer definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-timer", nil)
	if err != nil {
		t.Errorf("start timer instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/timer] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-timer", instanceID, nil)

	// Timer fires after PT2S; worker picks up the timer-fired job; total should be well under 30s
	inst, err := PollUntilTerminal(ctx, client, instanceID, 30*time.Second)
	if err != nil {
		t.Errorf("[%s/timer] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/timer] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
	} else {
		t.Logf("[%s/timer] COMPLETED ✓", prefix)
	}
}
