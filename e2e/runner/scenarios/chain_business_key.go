package scenarios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RunChainBusinessKey verifies that when a chained workflow auto-starts
// (autoStartNextWorkflow=true), the parent's businessKey is propagated to the
// child instance, so callers can locate the child by GET
// /v1/instances?definitionId=<child>&businessKey=<bk>. Without propagation,
// the child stores business_key=NULL and is unreachable by correlation.
func RunChainBusinessKey(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	appPath := filepath.Join(scenariosDir, prefix, "chain-workflow-a.json")
	disbPath := filepath.Join(scenariosDir, prefix, "chain-workflow-b.json")

	appDef, err := os.ReadFile(appPath)
	if err != nil {
		t.Errorf("[%s/chain-business-key] read %s: %v", prefix, appPath, err)
		return
	}
	disbDef, err := os.ReadFile(disbPath)
	if err != nil {
		t.Errorf("[%s/chain-business-key] read %s: %v", prefix, disbPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, disbDef); err != nil {
		t.Errorf("[%s/chain-business-key] upload workflow-b: %v", prefix, err)
		return
	}
	if err := client.UploadDefinition(ctx, appDef); err != nil {
		t.Errorf("[%s/chain-business-key] upload workflow-a: %v", prefix, err)
		return
	}

	workflowA := "e2e-" + prefix + "-chain-workflow-a"
	workflowB := "e2e-" + prefix + "-chain-workflow-b"
	bk := fmt.Sprintf("BK-chain-%s-%d", prefix, time.Now().UnixNano())

	parentID, err := client.StartInstanceWithBusinessKey(ctx, workflowA, map[string]any{"applicantId": "123"}, bk)
	if err != nil {
		t.Errorf("[%s/chain-business-key] start workflow-a with bk=%q: %v", prefix, bk, err)
		return
	}
	LogInstanceStarted(workflowA, parentID, map[string]any{"businessKey": bk})

	parent, err := PollUntilTerminal(ctx, client, parentID, 20*time.Second)
	if err != nil {
		t.Errorf("[%s/chain-business-key] workflow-a poll timeout: %v", prefix, err)
		return
	}
	if parent.Status != "COMPLETED" {
		t.Errorf("[%s/chain-business-key] workflow-a want COMPLETED, got %s (failure: %q)", prefix, parent.Status, parent.FailureReason)
		return
	}

	// The chain start runs in a goroutine after END, so poll the list endpoint
	// for the child instance keyed by (workflowB, bk).
	deadline := time.Now().Add(15 * time.Second)
	var children []Instance
	for time.Now().Before(deadline) {
		children, err = client.ListInstancesByDefAndBusinessKey(ctx, workflowB, bk)
		if err != nil {
			t.Errorf("[%s/chain-business-key] list instances of %s by bk=%q: %v", prefix, workflowB, bk, err)
			return
		}
		if len(children) > 0 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	if len(children) == 0 {
		t.Errorf("[%s/chain-business-key] no child instance of %s found with businessKey=%q — chain did not propagate businessKey", prefix, workflowB, bk)
		return
	}
	if len(children) > 1 {
		t.Errorf("[%s/chain-business-key] expected exactly 1 child instance of %s with bk=%q, got %d", prefix, workflowB, bk, len(children))
		return
	}
	child := children[0]
	if child.BusinessKey != bk {
		t.Errorf("[%s/chain-business-key] child.businessKey = %q, want %q", prefix, child.BusinessKey, bk)
		return
	}
	t.Logf("[%s/chain-business-key] chained child %s carries businessKey %q ✓", prefix, child.ID, bk)

	// Let the child reach a terminal state so the (bk, defID) pair frees up
	// for any subsequent runs of the suite.
	if _, err := PollUntilTerminal(ctx, client, child.ID, 20*time.Second); err != nil {
		t.Logf("[%s/chain-business-key] child %s did not terminate cleanly: %v (non-fatal)", prefix, child.ID, err)
	}
}
