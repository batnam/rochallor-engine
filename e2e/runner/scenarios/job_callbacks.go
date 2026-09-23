package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// These scenarios drive worker callbacks themselves to control ordering. The
// reserved job types are not registered by SDK workers. All calls use the
// existing REST/gRPC contract; Kafka jobs are received from the real broker.
func RunJobCallbacks(t TestReporter, client ClientIface, _ string, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, scenario := range []string{"completed", "cancelled", "manual-retry", "automatic-retry"} {
		if err := checkJobCallbacks(ctx, client, prefix, scenario); err != nil {
			t.Errorf("%s: %v", scenario, err)
			return
		}
	}
}

func callbackSnapshot(ctx context.Context, client ClientIface, instanceID string) (string, error) {
	inst, err := client.GetInstance(ctx, instanceID)
	if err != nil {
		return "", err
	}
	history, err := client.GetHistory(ctx, instanceID)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Instance Instance
		History  []StepExecution
	}{inst, history})
	return string(data), err
}

func checkJobCallbacks(ctx context.Context, client ClientIface, prefix, scenario string) error {
	delivery, old, err := callbackJob(ctx, client, prefix)
	if err != nil {
		return err
	}
	defer delivery.close()
	instanceID := delivery.instanceID
	const worker = "callback-worker"
	expected := "ACTIVE"
	switch scenario {
	case "completed":
		err = client.CompleteJob(ctx, old.ID, worker, map[string]any{"result": "original"})
		expected = "COMPLETED"
	case "cancelled":
		_, err = client.CancelInstance(ctx, instanceID, "operator cancelled")
		expected = "CANCELLED"
	case "manual-retry":
		if err = client.FailJob(ctx, old.ID, worker, "original failure", false); err != nil {
			return err
		}
		// A late completion before manual retry must not change the failed step.
		before, err := callbackSnapshot(ctx, client, instanceID)
		if err != nil {
			return err
		}
		if err = client.CompleteJob(ctx, old.ID, worker, map[string]any{"result": "stale"}); err != nil {
			return err
		}
		after, err := callbackSnapshot(ctx, client, instanceID)
		if err != nil {
			return err
		}
		if before != after {
			return fmt.Errorf("late completion rewrote failed history")
		}
		_, err = client.RetryStep(ctx, instanceID, "task", nil)
	case "automatic-retry":
		err = client.FailJob(ctx, old.ID, worker, "transient", true)
	}
	if err != nil {
		return err
	}
	before, err := callbackSnapshot(ctx, client, instanceID)
	if err != nil {
		return err
	}
	if err = client.CompleteJob(ctx, old.ID, worker, map[string]any{"result": "stale"}); err != nil {
		return err
	}
	for _, retryable := range []bool{true, false} {
		if err = client.FailJob(ctx, old.ID, worker, "duplicate failure", retryable); err != nil {
			return err
		}
	}
	after, err := callbackSnapshot(ctx, client, instanceID)
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("late callbacks changed instance/history")
	}
	inst, err := client.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	if inst.Status != expected {
		return fmt.Errorf("status=%s, want %s", inst.Status, expected)
	}
	wait := 10 * time.Second
	if expected != "ACTIVE" {
		wait = time.Second
	}
	jobs, err := delivery.next(ctx, wait)
	if err != nil {
		return err
	}
	if expected != "ACTIVE" {
		if len(jobs) != 0 {
			return fmt.Errorf("terminal callback resurrected %d jobs", len(jobs))
		}
		return nil
	}
	if len(jobs) != 1 || jobs[0].ID == old.ID {
		return fmt.Errorf("retry did not create exactly one new job: %+v", jobs)
	}
	fresh := jobs[0]
	if scenario == "automatic-retry" && (fresh.RetriesRemaining != 1 || fresh.StepExecutionID != old.StepExecutionID) {
		return fmt.Errorf("retry changed step identity or consumed budget twice: %+v", fresh)
	}
	if scenario == "manual-retry" && fresh.StepExecutionID == old.StepExecutionID {
		return fmt.Errorf("manual retry reused step execution")
	}
	before, err = callbackSnapshot(ctx, client, instanceID)
	if err != nil {
		return err
	}
	if err = client.CompleteJob(ctx, old.ID, worker, map[string]any{"result": "stale-after-claim"}); err != nil {
		return err
	}
	after, err = callbackSnapshot(ctx, client, instanceID)
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("old job advanced new attempt")
	}
	if err = client.CompleteJob(ctx, fresh.ID, worker, map[string]any{"result": "fresh"}); err != nil {
		return err
	}
	if err = client.CompleteJob(ctx, fresh.ID, worker, map[string]any{"result": "duplicate"}); err != nil {
		return err
	}
	inst, err = client.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	if inst.Status != "COMPLETED" || inst.Variables["result"] != "fresh" {
		return fmt.Errorf("current completion not preserved: %+v", inst)
	}
	return nil
}

func RunJobLeaseRecovery(t TestReporter, client ClientIface, _ string, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	delivery, old, err := callbackJob(ctx, client, prefix)
	if err != nil {
		t.Errorf("setup: %v", err)
		return
	}
	defer delivery.close()
	instanceID := delivery.instanceID
	// Wait for the real lease and sweeper; no database edits or test-only API.
	var fresh Job
	for fresh.ID == "" {
		select {
		case <-ctx.Done():
			t.Errorf("lease recovery: %v", ctx.Err())
			return
		case <-time.After(250 * time.Millisecond):
		}
		jobs, err := delivery.next(ctx, time.Second)
		if err != nil {
			t.Errorf("poll: %v", err)
			return
		}
		if len(jobs) > 0 {
			fresh = jobs[0]
		}
	}
	if fresh.ID == old.ID || fresh.StepExecutionID != old.StepExecutionID || fresh.RetriesRemaining != old.RetriesRemaining {
		t.Errorf("lease recovery reused job ID or spent a retry: old=%+v fresh=%+v", old, fresh)
		return
	}
	before, err := callbackSnapshot(ctx, client, instanceID)
	if err != nil {
		t.Errorf("snapshot: %v", err)
		return
	}
	if err = client.CompleteJob(ctx, old.ID, "callback-worker", map[string]any{"result": "stale"}); err != nil {
		t.Errorf("stale complete: %v", err)
		return
	}
	if err = client.FailJob(ctx, old.ID, "callback-worker", "stale failure", false); err != nil {
		t.Errorf("stale fail: %v", err)
		return
	}
	after, err := callbackSnapshot(ctx, client, instanceID)
	if err != nil || before != after {
		t.Errorf("stale callback mutated new lease: %v", err)
		return
	}
	if err = client.CompleteJob(ctx, fresh.ID, "callback-worker", map[string]any{"result": "fresh"}); err != nil {
		t.Errorf("fresh complete: %v", err)
		return
	}
	inst, err := client.GetInstance(ctx, instanceID)
	if err != nil || inst.Status != "COMPLETED" || inst.Variables["result"] != "fresh" {
		t.Errorf("fresh attempt did not finish: %+v, %v", inst, err)
	}
}
