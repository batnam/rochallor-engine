package main

import (
	"context"
	"fmt"
	"time"

	"github.com/batnam/e2e/runner/scenarios"
)

// PollUntilTerminal polls GET /v1/instances/{id} every 500 ms until the instance
// reaches a terminal status (COMPLETED, FAILED, or CANCELLED), or until timeout.
func PollUntilTerminal(ctx context.Context, client *Client, instanceID string, timeout time.Duration) (scenarios.Instance, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return scenarios.Instance{}, fmt.Errorf("timeout after %s waiting for instance %s", timeout, instanceID)
		}
		inst, err := client.GetInstance(ctx, instanceID)
		if err != nil {
			return scenarios.Instance{}, fmt.Errorf("poll %s: %w", instanceID, err)
		}
		switch inst.Status {
		case "COMPLETED", "FAILED", "CANCELLED":
			return inst, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}
