package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunParallel uploads the parallel gateway workflow for prefix, starts an instance,
// and asserts it reaches COMPLETED within 30 seconds.
func RunParallel(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "parallel.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload parallel definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-parallel", nil)
	if err != nil {
		t.Errorf("start parallel instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/parallel] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-parallel", instanceID, nil)

	inst, err := PollUntilTerminal(ctx, client, instanceID, 30*time.Second)
	if err != nil {
		t.Errorf("[%s/parallel] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/parallel] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
	} else {
		t.Logf("[%s/parallel] COMPLETED ✓", prefix)
	}
}
