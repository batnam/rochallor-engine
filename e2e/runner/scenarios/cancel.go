package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunCancel uploads a WAIT-only workflow, parks it in WAITING, cancels it,
// and asserts the instance reaches CANCELLED with the expected failure reason.
func RunCancel(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "cancel.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload cancel definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-cancel", nil)
	if err != nil {
		t.Errorf("start cancel instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/cancel] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-cancel", instanceID, nil)

	// Wait for instance to park in WAITING before cancelling.
	deadline := time.Now().Add(15 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Errorf("[%s/cancel] timed out waiting for WAITING state", prefix)
			return
		}
		inst, err := client.GetInstance(ctx, instanceID)
		if err != nil {
			t.Errorf("[%s/cancel] get instance: %v", prefix, err)
			return
		}
		AuditInstance(inst)
		if inst.Status == "WAITING" {
			break
		}
		if inst.Status == "COMPLETED" || inst.Status == "FAILED" || inst.Status == "CANCELLED" {
			t.Errorf("[%s/cancel] reached terminal state %s before cancelling", prefix, inst.Status)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	const cancelReason = "e2e-test-cancel"
	cancelled, err := client.CancelInstance(ctx, instanceID, cancelReason)
	if err != nil {
		t.Errorf("[%s/cancel] cancel instance: %v", prefix, err)
		return
	}

	if cancelled.Status != "CANCELLED" {
		t.Errorf("[%s/cancel] cancel response: want CANCELLED, got %s", prefix, cancelled.Status)
	}
	if cancelled.FailureReason != cancelReason {
		t.Errorf("[%s/cancel] cancel response: want failureReason %q, got %q", prefix, cancelReason, cancelled.FailureReason)
	}

	// Re-fetch to confirm the CANCELLED state is persisted.
	refetched, err := client.GetInstance(ctx, instanceID)
	if err != nil {
		t.Errorf("[%s/cancel] re-fetch after cancel: %v", prefix, err)
		return
	}
	if refetched.Status != "CANCELLED" {
		t.Errorf("[%s/cancel] re-fetch: want CANCELLED, got %s", prefix, refetched.Status)
	} else {
		t.Logf("[%s/cancel] CANCELLED ✓", prefix)
	}

	// Cancelling an already-cancelled instance must return an error.
	_, err = client.CancelInstance(ctx, instanceID, "should-fail")
	if err == nil {
		t.Errorf("[%s/cancel] expected error cancelling terminal instance, got nil", prefix)
	} else {
		t.Logf("[%s/cancel] correctly rejected cancel on terminal instance ✓", prefix)
	}
}
