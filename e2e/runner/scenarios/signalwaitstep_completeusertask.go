package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// RunSignalWaitStepCompleteUserTask exercises the combined signal and user task flow.
// It generates a per-instance audit log in audit-{instanceId}.log.
func RunSignalWaitStepCompleteUserTask(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "signalwaitstep-completeusertask.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err = client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload signalwaitstep-completeusertask definition (%s): %v", prefix, err)
		return
	}

	var instanceID string
	instanceID, err = client.StartInstance(ctx, "e2e-"+prefix+"-signalwaitstep-completeusertask", nil)
	if err != nil {
		t.Errorf("start signalwaitstep-completeusertask instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/signalwaitstep-completeusertask] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-signalwaitstep-completeusertask", instanceID, nil)

	var inst Instance

	// Poll until WAITING (WAIT step reached)
	waitStepID := prefix + "-wait-signal"

	deadline := time.Now().Add(15 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Errorf("[%s/signalwaitstep-completeusertask] timed out waiting for WAIT state", prefix)
			return
		}
		inst, err = client.GetInstance(ctx, instanceID)
		if err != nil {
			t.Errorf("[%s/signalwaitstep-completeusertask] get instance: %v", prefix, err)
			return
		}

		// Log status/step changes automatically
		AuditInstance(inst)

		if inst.Status == "WAITING" && contains(inst.CurrentStepIds, waitStepID) {
			break
		}
		if inst.Status == "COMPLETED" || inst.Status == "FAILED" || inst.Status == "CANCELLED" {
			t.Errorf("[%s/signalwaitstep-completeusertask] reached terminal state %s before WAIT step", prefix, inst.Status)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Signal the WAIT step
	vars := map[string]any{"signaled": true, "signalTime": time.Now().String()}
	startSignal := time.Now()
	if err = client.SignalWait(ctx, instanceID, waitStepID, vars); err != nil {
		t.Errorf("[%s/signalwaitstep-completeusertask] signal wait: %v", prefix, err)
		return
	}
	t.Logf("[%s/signalwaitstep-completeusertask] signal wait took %v", prefix, time.Since(startSignal))

	// Poll until WAITING (USER_TASK reached)
	userTaskStepID := prefix + "-user-task"

	deadline = time.Now().Add(15 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Errorf("[%s/signalwaitstep-completeusertask] timed out waiting for USER_TASK state", prefix)
			return
		}
		inst, err = client.GetInstance(ctx, instanceID)
		if err != nil {
			t.Errorf("[%s/signalwaitstep-completeusertask] get instance: %v", prefix, err)
			return
		}

		// Log status/step changes automatically
		AuditInstance(inst)

		if inst.Status == "WAITING" && contains(inst.CurrentStepIds, userTaskStepID) {
			break
		}
		if inst.Status == "COMPLETED" || inst.Status == "FAILED" || inst.Status == "CANCELLED" {
			t.Errorf("[%s/signalwaitstep-completeusertask] reached terminal state %s before USER_TASK", prefix, inst.Status)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Complete the USER_TASK
	startComplete := time.Now()
	if err = client.CompleteUserTaskByStableID(ctx, instanceID, userTaskStepID, map[string]any{"approved": true}); err != nil {
		t.Errorf("[%s/signalwaitstep-completeusertask] complete user task: %v", prefix, err)
		return
	}
	t.Logf("[%s/signalwaitstep-completeusertask] complete user task took %v", prefix, time.Since(startComplete))

	// Poll until terminal
	inst, err = PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/signalwaitstep-completeusertask] poll timeout after completion: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/signalwaitstep-completeusertask] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
	} else {
		t.Logf("[%s/signalwaitstep-completeusertask] COMPLETED ✓", prefix)
	}

	// US2: Error Handling
	t.Logf("[%s/signalwaitstep-completeusertask] US2: Testing error handling", prefix)

	// T018: Signal invalid step ID
	if err = client.SignalWait(ctx, instanceID, "invalid-step", nil); err == nil {
		t.Errorf("[%s/signalwaitstep-completeusertask] [US2] expected error for invalid step ID, got nil", prefix)
	} else {
		t.Logf("[%s/signalwaitstep-completeusertask] [US2] correctly rejected invalid step ID signal", prefix)
	}

	// T020: Signal non-existent instance
	if err = client.SignalWait(ctx, "non-existent-id", waitStepID, nil); err == nil {
		t.Errorf("[%s/signalwaitstep-completeusertask] [US2] expected error for non-existent instance ID, got nil", prefix)
	} else {
		t.Logf("[%s/signalwaitstep-completeusertask] [US2] correctly rejected non-existent instance ID signal", prefix)
	}

	// T019: Complete user task in invalid state (instance already COMPLETED)
	if err = client.CompleteUserTaskByStableID(ctx, instanceID, userTaskStepID, nil); err == nil {
		t.Errorf("[%s/signalwaitstep-completeusertask] [US2] expected error for completion in COMPLETED state, got nil", prefix)
	} else {
		t.Logf("[%s/signalwaitstep-completeusertask] [US2] correctly rejected task completion in invalid state", prefix)
	}
}
