package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunLinear uploads the linear workflow definition for prefix, starts an instance,
// and asserts it reaches COMPLETED within 15 seconds.
func RunLinear(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "linear.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload linear definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-linear", nil)
	if err != nil {
		t.Errorf("start linear instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/linear] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-linear", instanceID, nil)

	inst, err := PollUntilTerminal(ctx, client, instanceID, 15*time.Second)
	if err != nil {
		t.Errorf("[%s/linear] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/linear] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
		return
	}
	t.Logf("[%s/linear] COMPLETED ✓", prefix)

	// Verify GetHistory returns COMPLETED entries for all expected steps.
	history, err := client.GetHistory(ctx, instanceID)
	if err != nil {
		t.Errorf("[%s/linear] get history: %v", prefix, err)
		return
	}
	wantSteps := map[string]bool{
		"step-a": false,
		"step-b": false,
		"step-c": false,
		"end":    false,
	}
	for _, se := range history {
		if _, ok := wantSteps[se.StepID]; ok && se.Status == "COMPLETED" {
			wantSteps[se.StepID] = true
		}
	}
	for stepID, seen := range wantSteps {
		if !seen {
			t.Errorf("[%s/linear] history missing COMPLETED entry for step %q", prefix, stepID)
		}
	}
	t.Logf("[%s/linear] history verified ✓", prefix)
}
