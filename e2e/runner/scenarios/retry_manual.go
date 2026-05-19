package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunRetryManual exercises the manual retry path. The workflow has a single
// SERVICE_TASK with retryCount=1, so two auto attempts both fail and the
// instance settles in FAILED. The scenario then calls RetryStep to re-run
// the failed step; the worker's per-instance counter lets the third attempt
// succeed, and the instance reaches COMPLETED.
func RunRetryManual(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "retry-manual.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload retry-manual definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-retry-manual", nil)
	if err != nil {
		t.Errorf("start retry-manual instance (%s): %v", prefix, err)
		return
	}
	t.Logf("[%s/retry-manual] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-retry-manual", instanceID, nil)

	// 1. Wait for the auto-retry budget to exhaust → instance FAILED.
	inst, err := PollUntilTerminal(ctx, client, instanceID, 30*time.Second)
	if err != nil {
		t.Errorf("[%s/retry-manual] poll for FAILED: %v", prefix, err)
		return
	}
	if inst.Status != "FAILED" {
		t.Errorf("[%s/retry-manual] want FAILED before retry, got %s", prefix, inst.Status)
		return
	}
	if inst.FailureReason == "" {
		t.Errorf("[%s/retry-manual] want non-empty failureReason on initial FAILED")
		return
	}
	t.Logf("[%s/retry-manual] FAILED as expected with reason %q — issuing manual retry", prefix, inst.FailureReason)

	// 2. Manual retry of the failed step.
	stepID := prefix + "-manual-retry-step"
	retried, err := client.RetryStep(ctx, instanceID, stepID, nil)
	if err != nil {
		t.Errorf("[%s/retry-manual] RetryStep: %v", prefix, err)
		return
	}
	if retried.Status != "ACTIVE" {
		t.Errorf("[%s/retry-manual] post-retry status = %s, want ACTIVE", prefix, retried.Status)
		return
	}
	if retried.FailureReason != "" {
		t.Errorf("[%s/retry-manual] post-retry failureReason should be cleared, got %q", prefix, retried.FailureReason)
	}

	// 3. Workflow should resume and complete.
	final, err := PollUntilTerminal(ctx, client, instanceID, 30*time.Second)
	if err != nil {
		t.Errorf("[%s/retry-manual] poll after retry: %v", prefix, err)
		return
	}
	if final.Status != "COMPLETED" {
		t.Errorf("[%s/retry-manual] want COMPLETED after retry, got %s (failureReason: %q)",
			prefix, final.Status, final.FailureReason)
		return
	}

	// 4. History should show two attempts for the retried step: one FAILED
	//    (the original auto-retry-exhausted attempt — auto retries reuse the
	//    same step_execution row, only the terminal failure marks it FAILED)
	//    plus one COMPLETED (the manual retry attempt, a fresh
	//    step_execution row with attempt_number incremented).
	hist, err := client.GetHistory(ctx, instanceID)
	if err != nil {
		t.Errorf("[%s/retry-manual] GetHistory: %v", prefix, err)
		return
	}
	failed, completed := 0, 0
	for _, se := range hist {
		if se.StepID != stepID {
			continue
		}
		switch se.Status {
		case "FAILED":
			failed++
		case "COMPLETED":
			completed++
		}
	}
	if failed != 1 {
		t.Errorf("[%s/retry-manual] want 1 FAILED step_execution for retried step, got %d", prefix, failed)
	}
	if completed != 1 {
		t.Errorf("[%s/retry-manual] want 1 COMPLETED step_execution for retried step, got %d", prefix, completed)
	}
	t.Logf("[%s/retry-manual] COMPLETED after manual retry ✓ (history: %d failed + %d completed)", prefix, failed, completed)
}
