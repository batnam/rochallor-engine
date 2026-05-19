package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunRetryManualWithVars exercises the variable-patch path of manual retry.
// The workflow's single SERVICE_TASK fails until variables.corrected == true.
// The initial run has retryCount=0, so the first failure is terminal — the
// instance is FAILED. The scenario then retries the step with a
// {"corrected": true} variable patch; the engine merges that into the
// instance variables before re-dispatch, the worker observes the corrected
// flag in its job payload, succeeds, and the workflow completes.
func RunRetryManualWithVars(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "retry-manual-with-vars.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload retry-manual-with-vars definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-retry-manual-with-vars", nil)
	if err != nil {
		t.Errorf("start retry-manual-with-vars instance (%s): %v", prefix, err)
		return
	}
	t.Logf("[%s/retry-manual-with-vars] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-retry-manual-with-vars", instanceID, nil)

	// 1. First run terminates immediately as FAILED (retryCount=0).
	inst, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/retry-manual-with-vars] poll for FAILED: %v", prefix, err)
		return
	}
	if inst.Status != "FAILED" {
		t.Errorf("[%s/retry-manual-with-vars] want FAILED before retry, got %s", prefix, inst.Status)
		return
	}
	t.Logf("[%s/retry-manual-with-vars] FAILED with reason %q — retrying with corrected=true",
		prefix, inst.FailureReason)

	// 2. Retry with the variable patch that fixes the bad data.
	retried, err := client.RetryStep(ctx, instanceID, prefix+"-needs-fix-step",
		map[string]any{"corrected": true})
	if err != nil {
		t.Errorf("[%s/retry-manual-with-vars] RetryStep: %v", prefix, err)
		return
	}
	if retried.Status != "ACTIVE" {
		t.Errorf("[%s/retry-manual-with-vars] post-retry status = %s, want ACTIVE",
			prefix, retried.Status)
		return
	}

	// 3. Workflow should resume and complete this time.
	final, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/retry-manual-with-vars] poll after retry: %v", prefix, err)
		return
	}
	if final.Status != "COMPLETED" {
		t.Errorf("[%s/retry-manual-with-vars] want COMPLETED after retry, got %s (failureReason: %q)",
			prefix, final.Status, final.FailureReason)
		return
	}

	// 4. The patched variable should be visible on the completed instance,
	//    alongside the variable the handler set on success.
	if got, _ := final.Variables["corrected"].(bool); !got {
		t.Errorf("[%s/retry-manual-with-vars] expected corrected=true in final variables, got %v",
			prefix, final.Variables)
	}
	if got, _ := final.Variables["fixed"].(bool); !got {
		t.Errorf("[%s/retry-manual-with-vars] expected fixed=true in final variables, got %v",
			prefix, final.Variables)
	}
	t.Logf("[%s/retry-manual-with-vars] COMPLETED after variable patch ✓", prefix)
}
