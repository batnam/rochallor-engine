package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunParallelUserTask uploads the parallel-user-task workflow for prefix.
// One branch is a SERVICE_TASK; the other is a USER_TASK.
// The scenario completes the USER_TASK to unblock the JOIN_GATEWAY and asserts COMPLETED.
func RunParallelUserTask(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "parallel-user-task.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload parallel-user-task definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-parallel-user-task", nil)
	if err != nil {
		t.Errorf("start parallel-user-task instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/parallel-user-task] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-parallel-user-task", instanceID, nil)

	// Poll until the USER_TASK branch parks the instance in WAITING.
	userTaskStepID := prefix + "-put-user-task"
	deadline := time.Now().Add(15 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Errorf("[%s/parallel-user-task] timed out waiting for WAITING state", prefix)
			return
		}
		inst, err := client.GetInstance(ctx, instanceID)
		if err != nil {
			t.Errorf("[%s/parallel-user-task] get instance: %v", prefix, err)
			return
		}
		AuditInstance(inst)
		if inst.Status == "WAITING" && contains(inst.CurrentStepIds, userTaskStepID) {
			break
		}
		if inst.Status == "FAILED" || inst.Status == "CANCELLED" {
			t.Errorf("[%s/parallel-user-task] unexpected terminal state %s (reason: %q)", prefix, inst.Status, inst.FailureReason)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	if err := client.CompleteUserTaskByStableID(ctx, instanceID, userTaskStepID, map[string]any{"userTaskDone": true}); err != nil {
		t.Errorf("[%s/parallel-user-task] complete user task: %v", prefix, err)
		return
	}

	inst, err := PollUntilTerminal(ctx, client, instanceID, 15*time.Second)
	if err != nil {
		t.Errorf("[%s/parallel-user-task] poll timeout after user task completion: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/parallel-user-task] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
	} else {
		t.Logf("[%s/parallel-user-task] COMPLETED ✓", prefix)
	}
}
