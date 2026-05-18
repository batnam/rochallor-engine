package scenarios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunBusinessKeyDuplicate verifies that the engine rejects a second
// StartInstance call carrying the same (definitionId, businessKey) while the
// first instance is still in-flight (ACTIVE or WAITING). Enforced by the
// partial UNIQUE index added in migration 0010.
//
// Strategy: use the existing user-task definition. The first call lands at
// USER_TASK → WAITING (non-terminal), so the second call must be rejected by
// the engine. After cancelling instance 1, a third call with the same
// businessKey is expected to succeed (re-run after terminal state).
func RunBusinessKeyDuplicate(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "user-task.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("[%s/business-key-duplicate] read %s: %v", prefix, defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("[%s/business-key-duplicate] upload user-task definition: %v", prefix, err)
		return
	}

	defID := "e2e-" + prefix + "-user-task"
	bk := fmt.Sprintf("BK-dup-%s-%d", prefix, time.Now().UnixNano())

	// 1. First start: should succeed.
	inst1ID, err := client.StartInstanceWithBusinessKey(ctx, defID, nil, bk)
	if err != nil {
		t.Errorf("[%s/business-key-duplicate] first start with bk=%q: unexpected error: %v", prefix, bk, err)
		return
	}
	LogInstanceStarted(defID, inst1ID, map[string]any{"businessKey": bk})
	t.Logf("[%s/business-key-duplicate] first start ok: instance %s", prefix, inst1ID)

	// Give the worker a moment to drain the pre-user-task SERVICE_TASK so the
	// instance reaches WAITING. The duplicate check applies in BOTH ACTIVE and
	// WAITING states, so this isn't strictly required, but it makes the test
	// exercise the WAITING state explicitly (the chain-workflow use case).
	if err := waitForStatusIn(ctx, client, inst1ID, []string{"WAITING", "ACTIVE"}, 10*time.Second); err != nil {
		t.Errorf("[%s/business-key-duplicate] instance 1 did not reach WAITING/ACTIVE: %v", prefix, err)
		return
	}

	// 2. Second start with the same (defID, bk) while inst1 is in-flight: expect rejection.
	_, err = client.StartInstanceWithBusinessKey(ctx, defID, nil, bk)
	if err == nil {
		t.Errorf("[%s/business-key-duplicate] duplicate start with bk=%q unexpectedly succeeded — engine did not enforce uniqueness", prefix, bk)
		return
	}
	if !strings.Contains(err.Error(), "business key already in use") {
		t.Errorf("[%s/business-key-duplicate] duplicate start error did not surface business-key conflict: %v", prefix, err)
		return
	}
	t.Logf("[%s/business-key-duplicate] duplicate start rejected as expected: %v ✓", prefix, err)

	// 3. Cancel inst1, then retry the same bk: expect success (re-run allowed
	// after terminal state).
	if _, err := client.CancelInstance(ctx, inst1ID, "e2e cleanup"); err != nil {
		t.Errorf("[%s/business-key-duplicate] cancel instance 1: %v", prefix, err)
		return
	}
	if _, err := PollUntilTerminal(ctx, client, inst1ID, 10*time.Second); err != nil {
		t.Errorf("[%s/business-key-duplicate] instance 1 did not reach terminal after cancel: %v", prefix, err)
		return
	}

	inst3ID, err := client.StartInstanceWithBusinessKey(ctx, defID, nil, bk)
	if err != nil {
		t.Errorf("[%s/business-key-duplicate] re-run with bk=%q after cancel: unexpected error: %v", prefix, bk, err)
		return
	}
	t.Logf("[%s/business-key-duplicate] re-run after cancel ok: instance %s ✓", prefix, inst3ID)

	// Cleanup the re-run as well so the suite stays idempotent.
	if _, err := client.CancelInstance(ctx, inst3ID, "e2e cleanup"); err != nil {
		t.Logf("[%s/business-key-duplicate] cancel instance 3: %v (non-fatal)", prefix, err)
	}
}

// waitForStatusIn polls until the instance status matches one of want, or
// timeout elapses.
func waitForStatusIn(ctx context.Context, client ClientIface, instanceID string, want []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inst, err := client.GetInstance(ctx, instanceID)
		if err != nil {
			return err
		}
		for _, w := range want {
			if inst.Status == w {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("instance %s did not reach %v within %s", instanceID, want, timeout)
}
