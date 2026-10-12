package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/batnam/e2e/runner/faultproxy"
)

func sdkClaims(ctx context.Context) (map[string]faultproxy.Claim, error) {
	port := os.Getenv("SDK_FAULT_PROXY_PORT")
	if port == "" {
		port = "19095"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:"+port+"/__claims", nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var claims map[string]faultproxy.Claim
	err = json.NewDecoder(response.Body).Decode(&claims)
	return claims, err
}

// RunSDKPollingRecovery exercises the real engine's 30s lease, with heartbeat
// disabled. Workers use capacity 2 so backlog remains visible at the claim API.
func RunSDKPollingRecovery(t TestReporter, client ClientIface, dir, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	defer releaseFault(context.Background())
	for _, test := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"capacity includes pending callback", func(ctx context.Context) error { return sdkPollingCapacity(ctx, client, prefix) }},
		{"lease expiry and stale callback", func(ctx context.Context) error { return sdkPollingLease(ctx, client, prefix, dir, false) }},
		{"shutdown and replacement", func(ctx context.Context) error { return sdkPollingLease(ctx, client, prefix, dir, true) }},
	} {
		if err := test.run(ctx); err != nil {
			t.Errorf("%s: %v", test.name, err)
			return
		}
		t.Logf("%s: passed", test.name)
	}
}

func sdkPollingCapacity(ctx context.Context, client ClientIface, prefix string) error {
	if err := setFault(ctx, prefix, "hold-before", "complete"); err != nil {
		return err
	}
	ids := make([]string, 0, 8)
	for range 8 {
		id, err := startSDKProbe(ctx, client, prefix, false)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if _, _, err := waitSDKCalls(ctx, func(_ string, c faultproxy.Calls) bool { return c.Complete >= 2 }); err != nil {
		return err
	}
	claims, err := sdkClaims(ctx)
	if err != nil {
		return err
	}
	if len(claims) != 2 {
		return fmt.Errorf("capacity=2, but claimed %d jobs while callbacks pending", len(claims))
	}
	for _, claim := range claims {
		if claim.MaxJobs > 2 {
			return fmt.Errorf("over-poll: %+v", claim)
		}
	}
	if err := releaseFault(ctx); err != nil {
		return err
	}
	for _, id := range ids {
		inst, err := PollUntilTerminal(ctx, client, id, 20*time.Second)
		if err != nil {
			return err
		}
		if inst.Status != "COMPLETED" || inst.Variables["handlerCalls"] != float64(1) || inst.Variables["remaining"] != float64(2) {
			return fmt.Errorf("backlog caused duplicate execution or spent retry: %+v", inst)
		}
	}
	return nil
}

func sdkPollingLease(parent context.Context, client ClientIface, prefix, dir string, shutdown bool) error {
	ctx, cancel := context.WithTimeout(parent, 65*time.Second)
	defer cancel()
	if err := setFault(ctx, prefix, "hold-before", "complete"); err != nil {
		return err
	}
	id, err := startSDKProbe(ctx, client, prefix, false)
	if err != nil {
		return err
	}
	oldID, _, err := waitSDKCalls(ctx, func(_ string, c faultproxy.Calls) bool { return strings.Contains(c.Payload, id) })
	if err != nil {
		return err
	}
	claims, err := sdkClaims(ctx)
	if err != nil {
		return err
	}
	old, ok := claims[oldID]
	if !ok {
		return fmt.Errorf("missing old lease %s", oldID)
	}
	deadline, err := time.Parse(time.RFC3339Nano, old.Deadline)
	if err != nil {
		return err
	}
	if shutdown {
		if err := sdkCompose(ctx, dir, prefix, "stop", "-t", "7"); err != nil {
			return err
		}
		if err := sdkCompose(ctx, dir, prefix, "start"); err != nil {
			return err
		}
	}
	freshID, _, err := waitSDKCalls(ctx, func(jobID string, c faultproxy.Calls) bool { return jobID != oldID && strings.Contains(c.Payload, id) })
	if err != nil {
		return err
	}
	if time.Now().Before(deadline) {
		return fmt.Errorf("unexpected replacement before original lease expired")
	}
	before, err := callbackSnapshot(ctx, client, id)
	if err != nil {
		return err
	}
	if err := client.CompleteJob(ctx, oldID, old.Worker, map[string]any{"stale": true}); err != nil {
		return err
	}
	if err := client.FailJob(ctx, oldID, old.Worker, "stale", false); err != nil {
		return err
	}
	after, err := callbackSnapshot(ctx, client, id)
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("stale callback changed replacement attempt")
	}
	calls, err := faultControl(ctx, "", nil)
	if err != nil {
		return err
	}
	oldCalls := calls[oldID].Complete
	if err := releaseFault(ctx); err != nil {
		return err
	}
	inst, err := PollUntilTerminal(ctx, client, id, 15*time.Second)
	if err != nil {
		return err
	}
	if inst.Status != "COMPLETED" || inst.Variables["attemptId"] != freshID || inst.Variables["remaining"] != float64(2) {
		return fmt.Errorf("wrong replacement outcome: %+v", inst)
	}
	calls, err = faultControl(ctx, "", nil)
	if err != nil {
		return err
	}
	if calls[oldID].Complete != oldCalls {
		return fmt.Errorf("old owner kept retrying after lease/shutdown")
	}
	return nil
}
