package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunUserTask uploads the user-task workflow for prefix, starts an instance,
// waits for it to reach WAITING, resumes it via the instance resume endpoint,
// and asserts it reaches COMPLETED within 20 seconds.
func RunUserTask(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "user-task.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload user-task definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-user-task", nil)
	if err != nil {
		t.Errorf("start user-task instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/user-task] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-user-task", instanceID, nil)

	// Poll until WAITING (USER_TASK reached) or terminal
	userTaskStepID := prefix + "-user-task"

	deadline := time.Now().Add(15 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Errorf("[%s/user-task] timed out waiting for WAITING state", prefix)
			return
		}
		inst, err := client.GetInstance(ctx, instanceID)
		if err != nil {
			t.Errorf("[%s/user-task] get instance: %v", prefix, err)
			return
		}

		// Log status/step changes automatically
		AuditInstance(inst)

		if inst.Status == "WAITING" {
			break
		}
		if inst.Status == "COMPLETED" || inst.Status == "FAILED" || inst.Status == "CANCELLED" {
			t.Errorf("[%s/user-task] reached terminal state %s before user task was active", prefix, inst.Status)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Complete the USER_TASK step via the stable-id route
	if err := client.CompleteUserTaskByStableID(ctx, instanceID, userTaskStepID, map[string]any{"approved": true}); err != nil {
		t.Errorf("[%s/user-task] complete user task: %v", prefix, err)
		return
	}

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/user-task] poll timeout after resume: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/user-task] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
	} else {
		t.Logf("[%s/user-task] COMPLETED ✓", prefix)
	}
}
