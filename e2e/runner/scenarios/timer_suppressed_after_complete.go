package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunTimerSuppressedAfterComplete validates the fix for the boundary-event
// firing-after-step-complete bug.
//
// Shape: fast SERVICE_TASK (with non-interrupting TIMER PT2S → should-not-fire)
// → WAIT (parked until signal) → END.
//
// Expected: the SERVICE_TASK completes immediately, the boundary timer's
// fire_at elapses while the instance is parked in WAIT, but DispatchBoundaryStep
// observes that the parent step_execution has left RUNNING and suppresses the
// fire. After the signal, the workflow completes without ever creating a
// step_execution for should-not-fire.
//
// Only runs against the python SDK to avoid duplicating JSON + handler wiring
// in every worker.
func RunTimerSuppressedAfterComplete(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	if prefix != "python" {
		t.Logf("[%s/timer-suppressed-after-complete] skipped (python-only scenario)", prefix)
		return
	}

	defPath := filepath.Join(scenariosDir, prefix, "timer-suppressed-after-complete.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload timer-suppressed-after-complete definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-timer-suppressed-after-complete", nil)
	if err != nil {
		t.Errorf("start timer-suppressed-after-complete instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/timer-suppressed-after-complete] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-timer-suppressed-after-complete", instanceID, nil)

	// Wait for the workflow to advance past the fast-noop and into WAIT.
	deadline := time.Now().Add(15 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Errorf("[%s/timer-suppressed-after-complete] timed out waiting for WAITING state", prefix)
			return
		}
		inst, err := client.GetInstance(ctx, instanceID)
		if err != nil {
			t.Errorf("[%s/timer-suppressed-after-complete] get instance: %v", prefix, err)
			return
		}
		AuditInstance(inst)
		if inst.Status == "WAITING" {
			break
		}
		if inst.Status == "COMPLETED" || inst.Status == "FAILED" || inst.Status == "CANCELLED" {
			t.Errorf("[%s/timer-suppressed-after-complete] reached terminal state %s before WAIT was entered", prefix, inst.Status)
			return
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Timer duration is PT2S and the sweeper polls every 5s — wait long enough
	// that any firing path would have executed. Then signal and assert.
	t.Logf("[%s/timer-suppressed-after-complete] parked in WAIT — sleeping past timer + sweeper window", prefix)
	time.Sleep(10 * time.Second)

	if err := client.SignalWait(ctx, instanceID, prefix+"-park", map[string]any{}); err != nil {
		t.Errorf("[%s/timer-suppressed-after-complete] signal wait: %v", prefix, err)
		return
	}

	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/timer-suppressed-after-complete] poll timeout after signal: %v", prefix, err)
		return
	}
	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/timer-suppressed-after-complete] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
		return
	}

	history, err := client.GetHistory(ctx, instanceID)
	if err != nil {
		t.Errorf("[%s/timer-suppressed-after-complete] get history: %v", prefix, err)
		return
	}

	shouldNotFireStepID := prefix + "-should-not-fire"
	for _, se := range history {
		if se.StepID == shouldNotFireStepID {
			t.Errorf("[%s/timer-suppressed-after-complete] boundary timer fired after parent step COMPLETED — found step_execution for %s (status=%s)", prefix, shouldNotFireStepID, se.Status)
			return
		}
	}
	t.Logf("[%s/timer-suppressed-after-complete] boundary timer suppressed after step COMPLETED ✓", prefix)
}
