package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// restartEngine uses only the isolated Compose stack created by e2e/run.sh.
// SIGKILL tests recovery without graceful engine shutdown or database edits.
func restartEngine(ctx context.Context, client ClientIface, scenariosDir, probeInstance string, offline time.Duration) error {
	composeFile, service := "docker-compose-polling.yml", "engine"
	if os.Getenv("WE_DISPATCH_MODE") == "kafka_outbox" {
		composeFile, service = "docker-compose-kafka-outbox.yml", "e2e-engine"
	}
	composePath := filepath.Join(scenariosDir, "..", composeFile)
	command := func(commandCtx context.Context, args ...string) error {
		args = append([]string{"compose", "-f", composePath}, args...)
		args = append(args, service)
		output, err := exec.CommandContext(commandCtx, "docker", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("engine %v: %w: %s", args, err, output)
		}
		return nil
	}
	if err := command(ctx, "kill", "--signal", "SIGKILL"); err != nil {
		return err
	}
	restarted := false
	defer func() {
		if !restarted {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = command(cleanupCtx, "up", "--no-deps", "--no-recreate", "--no-build", "-d")
		}
	}()
	// Disable Compose restart policy during the intentional outage.
	if err := command(ctx, "stop", "--timeout", "0"); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(offline):
	}
	if err := command(ctx, "up", "--no-deps", "--no-recreate", "--no-build", "-d"); err != nil {
		return err
	}
	restarted = true
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, err := client.GetInstance(probeCtx, probeInstance)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("engine did not become reachable after restart")
}

// RunTimerRecovery covers both timer modes, suppressed timers and replay after
// restart. Jobs run through the real SDK workers and the selected dispatch mode.
func RunTimerRecovery(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	type timerCase struct {
		name, id     string
		interrupting bool
	}
	cases := []timerCase{{name: "non-interrupting"}, {name: "interrupting", interrupting: true}, {name: "completed", interrupting: true}, {name: "cancelled", interrupting: true}}
	for i := range cases {
		c := &cases[i]
		defID := fmt.Sprintf("%s-durable-timer-%s-%d", prefix, c.name, time.Now().UnixNano())
		def := fmt.Sprintf(`{"id":%q,"name":"Durable timer","steps":[{"id":"work","name":"Wait","type":"WAIT","nextStep":"end","boundaryEvents":[{"type":"TIMER","duration":"PT3S","interrupting":%t,"targetStepId":"timeout"}]},{"id":"timeout","name":"Timeout","type":"SERVICE_TASK","jobType":%q,"nextStep":"end"},{"id":"end","name":"End","type":"END"}]}`, defID, c.interrupting, prefix+"-timer-fired")
		if err := client.UploadDefinition(ctx, []byte(def)); err != nil {
			t.Errorf("upload: %v", err)
			return
		}
		var err error
		c.id, err = client.StartInstance(ctx, defID, nil)
		if err != nil {
			t.Errorf("start: %v", err)
			return
		}
		switch c.name {
		case "completed":
			err = client.SignalWait(ctx, c.id, "work", nil)
		case "cancelled":
			_, err = client.CancelInstance(ctx, c.id, "operator cancelled")
		}
		if err != nil {
			t.Errorf("%s setup: %v", c.name, err)
			return
		}
	}
	// Leave the engine down past fire_at; PostgreSQL/Kafka and workers remain up.
	if err := restartEngine(ctx, client, scenariosDir, cases[0].id, 4*time.Second); err != nil {
		t.Errorf("crash/restart: %v", err)
		return
	}
	snapshots := make(map[string]string)
	for _, c := range cases {
		inst, err := PollUntilTerminal(ctx, client, c.id, 20*time.Second)
		if err != nil {
			t.Errorf("%s recovery: %v", c.name, err)
			return
		}
		expectedStatus := "COMPLETED"
		if c.name == "cancelled" {
			expectedStatus = "CANCELLED"
		}
		if inst.Status != expectedStatus {
			t.Errorf("%s status=%s, want %s", c.name, inst.Status, expectedStatus)
			return
		}
		history, err := client.GetHistory(ctx, c.id)
		if err != nil {
			t.Errorf("history: %v", err)
			return
		}
		targets := 0
		for _, step := range history {
			if step.StepID == "timeout" {
				targets++
				if step.Status != "COMPLETED" {
					t.Errorf("timeout did not complete: %+v", step)
					return
				}
			}
			if step.StepID == "work" && c.name == "interrupting" && step.Status != "FAILED" {
				t.Errorf("work not interrupted: %+v", step)
				return
			}
			if step.StepID == "work" && c.name == "non-interrupting" && step.Status != "RUNNING" {
				t.Errorf("non-interrupting timer changed work: %+v", step)
				return
			}
		}
		wantTargets := 1
		if c.name == "completed" || c.name == "cancelled" {
			wantTargets = 0
		}
		if targets != wantTargets {
			t.Errorf("%s dispatched %d timer targets, want %d", c.name, targets, wantTargets)
			return
		}
		snapshots[c.id], err = callbackSnapshot(ctx, client, c.id)
		if err != nil {
			t.Errorf("snapshot: %v", err)
			return
		}
	}
	if err := restartEngine(ctx, client, scenariosDir, cases[0].id, 0); err != nil {
		t.Errorf("second restart: %v", err)
		return
	}
	// Wait for a full timer sweep after restart to detect duplicate dispatch.
	select {
	case <-ctx.Done():
		t.Errorf("replay wait: %v", ctx.Err())
		return
	case <-time.After(6 * time.Second):
	}
	for _, c := range cases {
		got, err := callbackSnapshot(ctx, client, c.id)
		if err != nil || got != snapshots[c.id] {
			t.Errorf("%s replay changed persisted state: %v", c.name, err)
			return
		}
	}
}

func waitForChainedChild(ctx context.Context, client ClientIface, defID, businessKey string) (Instance, error) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		children, err := client.ListInstancesByDefAndBusinessKey(ctx, defID, businessKey)
		if err != nil {
			return Instance{}, err
		}
		if len(children) > 1 {
			return Instance{}, fmt.Errorf("created %d children for %s", len(children), defID)
		}
		if len(children) == 1 {
			child, err := PollUntilTerminal(ctx, client, children[0].ID, 20*time.Second)
			if err != nil {
				return Instance{}, err
			}
			if child.Status != "COMPLETED" {
				return Instance{}, fmt.Errorf("child status=%s: %s", child.Status, child.FailureReason)
			}
			return child, nil
		}
		select {
		case <-ctx.Done():
			return Instance{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return Instance{}, fmt.Errorf("no child created for %s", defID)
}

func RunChainRecovery(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	parentID := fmt.Sprintf("%s-durable-chain-%d", prefix, time.Now().UnixNano())
	childID, grandchildID, bk := parentID+"-child", parentID+"-grandchild", parentID+"-key"
	parentDef := fmt.Sprintf(`{"id":%q,"name":"Parent","autoStartNextWorkflow":true,"nextWorkflowId":%q,"steps":[{"id":"end","name":"End","type":"END"}]}`, parentID, childID)
	if err := client.UploadDefinition(ctx, []byte(parentDef)); err != nil {
		t.Errorf("parent upload: %v", err)
		return
	}
	vars := map[string]any{"amount": float64(42), "correlation": bk, "nested": map[string]any{"approved": true}}
	parent, err := client.StartInstanceWithBusinessKey(ctx, parentID, vars, bk)
	if err != nil {
		t.Errorf("parent start: %v", err)
		return
	}
	completed, err := PollUntilTerminal(ctx, client, parent, 10*time.Second)
	if err != nil || completed.Status != "COMPLETED" {
		t.Errorf("parent did not complete: %+v %v", completed, err)
		return
	}
	// The target is intentionally absent when the durable worker first retries.
	select {
	case <-ctx.Done():
		t.Errorf("wait: %v", ctx.Err())
		return
	case <-time.After(1500 * time.Millisecond):
	}
	if err := restartEngine(ctx, client, scenariosDir, parent, 0); err != nil {
		t.Errorf("crash/restart: %v", err)
		return
	}
	grandchildDef := fmt.Sprintf(`{"id":%q,"name":"Grandchild","steps":[{"id":"end","name":"End","type":"END"}]}`, grandchildID)
	childDef := fmt.Sprintf(`{"id":%q,"name":"Child","autoStartNextWorkflow":true,"nextWorkflowId":%q,"steps":[{"id":"task","name":"Finalize","type":"SERVICE_TASK","jobType":%q,"nextStep":"end"},{"id":"end","name":"End","type":"END"}]}`, childID, grandchildID, prefix+"-chain-finalize")
	for _, def := range []string{grandchildDef, childDef} {
		if err := client.UploadDefinition(ctx, []byte(def)); err != nil {
			t.Errorf("target upload: %v", err)
			return
		}
	}
	children := make(map[string]Instance)
	snapshots := make(map[string]string)
	for _, defID := range []string{childID, grandchildID} {
		child, err := waitForChainedChild(ctx, client, defID, bk)
		if err != nil {
			t.Errorf("recovery: %v", err)
			return
		}
		// Worker output may add fields; the completion snapshot must survive every hop.
		for key, value := range vars {
			want, _ := json.Marshal(value)
			got, _ := json.Marshal(child.Variables[key])
			if string(got) != string(want) {
				t.Errorf("%s lost variable %s: %s", defID, key, got)
				return
			}
		}
		if child.BusinessKey != bk {
			t.Errorf("%s lost business key", defID)
			return
		}
		children[defID] = child
		snapshots[defID], err = callbackSnapshot(ctx, client, child.ID)
		if err != nil {
			t.Errorf("snapshot: %v", err)
			return
		}
	}
	if err := restartEngine(ctx, client, scenariosDir, parent, 0); err != nil {
		t.Errorf("second restart: %v", err)
		return
	}
	select {
	case <-ctx.Done():
		t.Errorf("replay wait: %v", ctx.Err())
		return
	case <-time.After(2 * time.Second):
	}
	for defID, original := range children {
		listed, err := client.ListInstancesByDefAndBusinessKey(ctx, defID, bk)
		if err != nil || len(listed) != 1 || listed[0].ID != original.ID {
			t.Errorf("restart duplicated %s: %+v %v", defID, listed, err)
			return
		}
		snapshot, err := callbackSnapshot(ctx, client, original.ID)
		if err != nil || snapshot != snapshots[defID] {
			t.Errorf("restart changed child history: %v", err)
			return
		}
	}
}
