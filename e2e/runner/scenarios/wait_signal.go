package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunWaitSignal exercises the new signal endpoint
// (POST /v1/instances/{instanceId}/signals/{waitStepId}).
//
// It uploads a workflow with a SERVICE_TASK → WAIT → END shape (no boundary
// timer on the WAIT), starts an instance, waits for it to reach WAITING, then
// posts a signal to the WAIT step. The signal body is shallow-merged into
// the instance variables and the workflow advances to END.
func RunWaitSignal(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "wait-signal.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload wait-signal definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-wait-signal", nil)
	if err != nil {
		t.Errorf("start wait-signal instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/wait-signal] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-wait-signal", instanceID, nil)

	waitStepID := prefix + "-wait-signal"

	deadline := time.Now().Add(15 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Errorf("[%s/wait-signal] timed out waiting for WAITING state", prefix)
			return
		}
		inst, err := client.GetInstance(ctx, instanceID)
		if err != nil {
			t.Errorf("[%s/wait-signal] get instance: %v", prefix, err)
			return
		}

		// Log status/step changes automatically
		AuditInstance(inst)

		if inst.Status == "WAITING" {
			break
		}
		if inst.Status == "COMPLETED" || inst.Status == "FAILED" || inst.Status == "CANCELLED" {
			t.Errorf("[%s/wait-signal] reached terminal state %s before WAIT step was entered", prefix, inst.Status)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Signal the WAIT step. The body is the variable map directly (no wrapper).
	vars := map[string]any{"transactionStatus": "SUCCESS", "txnRef": "E2E-" + prefix + "-001"}
	if err := client.SignalWait(ctx, instanceID, waitStepID, vars); err != nil {
		t.Errorf("[%s/wait-signal] signal wait: %v", prefix, err)
		return
	}

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/wait-signal] poll timeout after signal: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/wait-signal] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
	} else {
		t.Logf("[%s/wait-signal] COMPLETED ✓", prefix)
	}
}
