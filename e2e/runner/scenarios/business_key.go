package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// RunBusinessKey uploads a simple workflow, starts it with a businessKey,
// and asserts the business key is persisted on the completed instance.
func RunBusinessKey(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "business-key.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload business-key definition (%s): %v", prefix, err)
		return
	}

	const wantKey = "BK-e2e-001"
	instanceID, err := client.StartInstanceWithBusinessKey(ctx, "e2e-"+prefix+"-business-key", nil, wantKey)
	if err != nil {
		t.Errorf("start business-key instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/business-key] instance %s started with businessKey %q", prefix, instanceID, wantKey)
	LogInstanceStarted("e2e-"+prefix+"-business-key", instanceID, nil)

	inst, err := PollUntilTerminal(ctx, client, instanceID, 15*time.Second)
	if err != nil {
		t.Errorf("[%s/business-key] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/business-key] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
		return
	}

	// Re-fetch to verify businessKey is persisted on the stored instance.
	refetched, err := client.GetInstance(ctx, instanceID)
	if err != nil {
		t.Errorf("[%s/business-key] re-fetch: %v", prefix, err)
		return
	}
	if refetched.BusinessKey != wantKey {
		t.Errorf("[%s/business-key] want businessKey %q, got %q", prefix, wantKey, refetched.BusinessKey)
	} else {
		t.Logf("[%s/business-key] businessKey %q persisted ✓", prefix, refetched.BusinessKey)
	}
}
