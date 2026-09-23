package scenarios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RunChaining exercises the automatic workflow chaining feature.
func RunChaining(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	appPath := filepath.Join(scenariosDir, prefix, "chain-workflow-a.json")
	disbPath := filepath.Join(scenariosDir, prefix, "chain-workflow-b.json")

	appDef, err := os.ReadFile(appPath)
	if err != nil {
		t.Errorf("[%s/chaining] read %s: %v", prefix, appPath, err)
		return
	}
	disbDef, err := os.ReadFile(disbPath)
	if err != nil {
		t.Errorf("[%s/chaining] read %s: %v", prefix, disbPath, err)
		return
	}

	ctx := context.Background()

	// 1. Upload both definitions
	if err := client.UploadDefinition(ctx, disbDef); err != nil {
		t.Errorf("[%s/chaining] upload chain-workflow-b definition: %v", prefix, err)
		return
	}
	if err := client.UploadDefinition(ctx, appDef); err != nil {
		t.Errorf("[%s/chaining] upload chain-workflow-a definition: %v", prefix, err)
		return
	}

	// 2. Start the primary instance
	initialVars := map[string]any{"applicantId": "123", "amount": float64(100)}
	workflowA := "e2e-" + prefix + "-chain-workflow-a"
	businessKey := fmt.Sprintf("chain-%s-%d", prefix, time.Now().UnixNano())
	instanceID, err := client.StartInstanceWithBusinessKey(ctx, workflowA, initialVars, businessKey)
	if err != nil {
		t.Errorf("[%s/chaining] start workflow-a instance: %v", prefix, err)
		return
	}
	LogInstanceStarted(workflowA, instanceID, initialVars)

	// 3. Poll until terminal
	instA, err := PollUntilTerminal(ctx, client, instanceID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/chaining] workflow-a poll timeout: %v", prefix, err)
		return
	}

	if instA.Status != "COMPLETED" {
		t.Errorf("[%s/chaining] workflow-a want COMPLETED, got %s (failure: %q)", prefix, instA.Status, instA.FailureReason)
		return
	}

	// Assert that the worker's output variables are present on the completed instance.
	assertVar(t, prefix, "chaining", instA.Variables, "applicantId", "123")
	assertVar(t, prefix, "chaining", instA.Variables, "amount", float64(100))

	// Verify that the SDK processed the automatically created child.
	child, err := waitForChainedChild(ctx, client, "e2e-"+prefix+"-chain-workflow-b", businessKey)
	if err != nil {
		t.Errorf("[%s/chaining] child: %v", prefix, err)
		return
	}
	assertVar(t, prefix, "chaining-child", child.Variables, "applicantId", "123")
	assertVar(t, prefix, "chaining-child", child.Variables, "amount", float64(100))
}
