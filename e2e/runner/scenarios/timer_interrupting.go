package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunTimerInterrupting tests an interrupting boundary timer on a SERVICE_TASK.
// The slow-task handler intentionally holds the job without completing; the timer
// fires after PT2S, cancels the main step, and routes to the timeout handler.
//
// NOTE: This scenario requires the engine's timer_sweeper to support the
// interrupting=true path (InterruptStepAndDispatchBoundary). Gate with
// E2E_TIMER_INTERRUPTING=1 until the engine fix is merged.
func RunTimerInterrupting(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	if os.Getenv("E2E_TIMER_INTERRUPTING") != "1" {
		t.Logf("[%s/timer-interrupting] skipped (set E2E_TIMER_INTERRUPTING=1 to enable)", prefix)
		return
	}

	defPath := filepath.Join(scenariosDir, prefix, "timer-interrupting.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload timer-interrupting definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-timer-interrupting", nil)
	if err != nil {
		t.Errorf("start timer-interrupting instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/timer-interrupting] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-timer-interrupting", instanceID, nil)

	// Timer fires after PT2S; the slow-task handler never completes voluntarily.
	inst, err := PollUntilTerminal(ctx, client, instanceID, 30*time.Second)
	if err != nil {
		t.Errorf("[%s/timer-interrupting] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/timer-interrupting] want COMPLETED (via timeout handler), got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
		return
	}

	// Verify history: slow-task must be interrupted (FAILED/CANCELLED), timeout-handler must be COMPLETED.
	history, err := client.GetHistory(ctx, instanceID)
	if err != nil {
		t.Errorf("[%s/timer-interrupting] get history: %v", prefix, err)
		return
	}

	slowTaskInterrupted := false
	timeoutHandlerCompleted := false
	for _, se := range history {
		switch se.StepID {
		case prefix + "-slow-task":
			if se.Status == "FAILED" || se.Status == "CANCELLED" {
				slowTaskInterrupted = true
			}
		case prefix + "-timeout-handler":
			if se.Status == "COMPLETED" {
				timeoutHandlerCompleted = true
			}
		}
	}

	if !slowTaskInterrupted {
		t.Errorf("[%s/timer-interrupting] expected slow-task to be interrupted (FAILED/CANCELLED) in history", prefix)
	}
	if !timeoutHandlerCompleted {
		t.Errorf("[%s/timer-interrupting] expected timeout-handler to be COMPLETED in history", prefix)
	}
	if slowTaskInterrupted && timeoutHandlerCompleted {
		t.Logf("[%s/timer-interrupting] interrupting timer fired correctly ✓", prefix)
	}
}
