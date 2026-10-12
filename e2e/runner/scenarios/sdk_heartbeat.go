package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/batnam/e2e/runner/faultproxy"
)

// Explicit compatibility probe, selected only for an older engine image.
func RunSDKHeartbeatUnsupported(t TestReporter, client ClientIface, dir, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := setFault(ctx, prefix, "", "complete"); err != nil {
		t.Errorf("reset proxy: %v", err)
		return
	}
	id, err := startHeartbeatProbe(ctx, client, prefix, 0, false, false)
	if err != nil {
		t.Errorf("start: %v", err)
		return
	}
	compose := filepath.Join(dir, "..", "docker-compose-polling.yml")
	service := "worker-" + prefix
	for ctx.Err() == nil {
		container, err := exec.CommandContext(ctx, "docker", "compose", "-f", compose, "ps", "-aq", service).Output()
		if err != nil {
			t.Errorf("locate worker: %v", err)
			return
		}
		output, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .}}", strings.TrimSpace(string(container))).Output()
		if err != nil {
			t.Errorf("inspect worker: %v", err)
			return
		}
		var state struct {
			RestartCount int
			State        struct{ ExitCode int }
		}
		if err := json.Unmarshal(output, &state); err != nil {
			t.Errorf("worker state: %v", err)
			return
		}
		if state.RestartCount > 0 || state.State.ExitCode != 0 {
			logs, err := exec.CommandContext(ctx, "docker", "compose", "-f", compose, "logs", "--no-color", service).Output()
			if err != nil || !strings.Contains(strings.ToLower(string(logs)), "heartbeat") || !strings.Contains(strings.ToLower(string(logs)), "support") {
				t.Errorf("worker stopped without an explicit unsupported-heartbeat error: %v", err)
				return
			}
			instance, err := client.GetInstance(ctx, id)
			if err != nil || instance.Status != "ACTIVE" || instance.Variables["handlerCalls"] != nil {
				t.Errorf("unsupported engine ran a handler: instance=%+v error=%v", instance, err)
				return
			}
			claims, err := heartbeatClaims(ctx, id)
			if err != nil || len(claims) != 1 {
				t.Errorf("unexpected claims: %v error=%v", claims, err)
				return
			}
			beats, err := heartbeatStats(ctx)
			if err != nil {
				t.Errorf("heartbeat stats: %v", err)
				return
			}
			calls, err := faultControl(ctx, "", nil)
			if err != nil {
				t.Errorf("callback stats: %v", err)
				return
			}
			for jobID := range claims {
				if beats[jobID].Count != 0 || beats[jobID].Requests != 1 {
					t.Errorf("unsupported heartbeat retried or falsely accepted: %+v", beats[jobID])
					return
				}
				if calls[jobID].Complete != 0 || calls[jobID].Fail != 0 {
					t.Errorf("unsupported heartbeat sent a business callback: %+v", calls[jobID])
					return
				}
			}
			t.Logf("older engine rejected heartbeat explicitly; handler did not run; retry budget untouched")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("worker did not report unsupported heartbeat before timeout")
}

func heartbeatStats(ctx context.Context) (map[string]faultproxy.Heartbeat, error) {
	port := os.Getenv("SDK_FAULT_PROXY_PORT")
	if port == "" {
		port = "19095"
	}
	request, err := http.NewRequestWithContext(ctx, "GET", "http://localhost:"+port+"/__heartbeats", nil)
	if err != nil {
		return nil, err
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result map[string]faultproxy.Heartbeat
	err = json.NewDecoder(response.Body).Decode(&result)
	return result, err
}

func startHeartbeatProbe(ctx context.Context, client ClientIface, prefix string, duration int, failure, timer bool) (string, error) {
	id := fmt.Sprintf("%s-heartbeat-%d", prefix, time.Now().UnixNano())
	boundary := ""
	if timer {
		boundary = `,"boundaryEvents":[{"type":"TIMER","duration":"PT2S","targetStepId":"end","interrupting":true}]`
	}
	definition := fmt.Sprintf(`{"id":%q,"name":"SDK heartbeat","steps":[{"id":"task","name":"Task","type":"SERVICE_TASK","jobType":%q,"retryCount":2,"nextStep":"end"%s},{"id":"end","name":"End","type":"END"}]}`, id, prefix+"-sdk-heartbeat", boundary)
	if err := client.UploadDefinition(ctx, []byte(definition)); err != nil {
		return "", err
	}
	return client.StartInstance(ctx, id, map[string]any{"durationMs": duration, "failure": failure})
}

func heartbeatClaims(ctx context.Context, instance string) (map[string]faultproxy.Claim, error) {
	all, err := sdkClaims(ctx)
	if err != nil {
		return nil, err
	}
	result := map[string]faultproxy.Claim{}
	for id, claim := range all {
		if claim.InstanceID == instance {
			result[id] = claim
		}
	}
	return result, nil
}

func waitHeartbeatClaim(ctx context.Context, instance string) (string, error) {
	for ctx.Err() == nil {
		claims, err := heartbeatClaims(ctx, instance)
		if err != nil {
			return "", err
		}
		beats, err := heartbeatStats(ctx)
		if err != nil {
			return "", err
		}
		for id := range claims {
			if beats[id].Count > 0 {
				return id, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", ctx.Err()
}

func heartbeatOutcome(ctx context.Context, client ClientIface, id string, handlers, budget float64, attempts int) error {
	instance, err := PollUntilTerminal(ctx, client, id, 20*time.Second)
	if err != nil {
		return err
	}
	if instance.Status != "COMPLETED" || instance.Variables["handlerCalls"] != handlers || instance.Variables["remaining"] != budget {
		return fmt.Errorf("wrong heartbeat outcome: %+v", instance)
	}
	claims, err := heartbeatClaims(ctx, id)
	if err != nil {
		return err
	}
	if len(claims) != attempts {
		return fmt.Errorf("expected %d attempts, observed %d claims", attempts, len(claims))
	}
	history, err := client.GetHistory(ctx, id)
	if err != nil {
		return err
	}
	ends := 0
	for _, step := range history {
		if step.StepID == "end" {
			ends++
		}
	}
	if ends != 1 {
		return fmt.Errorf("workflow completed %d times", ends)
	}
	return nil
}

// RunSDKHeartbeat runs beside the existing heartbeat-off polling fault suite.
// The worker has a separate opt-in registry for *-sdk-heartbeat jobs, while
// normal jobs retain their original lease expiry and capacity behavior.
func RunSDKHeartbeat(t TestReporter, client ClientIface, dir, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	defer releaseFault(context.Background())
	tests := []struct {
		name string
		run  func() error
	}{
		{"90-second handler / 30-second lease", func() error {
			if err := setFault(ctx, prefix, "", "complete"); err != nil {
				return err
			}
			id, err := startHeartbeatProbe(ctx, client, prefix, 90000, false, false)
			if err != nil {
				return err
			}
			jobID, err := waitHeartbeatClaim(ctx, id)
			if err != nil {
				return err
			}
			instance, err := PollUntilTerminal(ctx, client, id, 110*time.Second)
			if err != nil {
				return err
			}
			if instance.Status != "COMPLETED" {
				return fmt.Errorf("long handler: %+v", instance)
			}
			if err := heartbeatOutcome(ctx, client, id, 1, 2, 1); err != nil {
				return err
			}
			beats, err := heartbeatStats(ctx)
			if err != nil {
				return err
			}
			if beats[jobID].Count < 8 {
				return fmt.Errorf("long task heartbeat count=%d", beats[jobID].Count)
			}
			return nil
		}},
		{"completion pending across renewals", func() error { return heartbeatPending(ctx, client, prefix, false) }},
		{"failure pending across renewals", func() error { return heartbeatPending(ctx, client, prefix, true) }},
		{"worker killed during pending callback", func() error {
			if err := setFault(ctx, prefix, "hold-before", "complete"); err != nil {
				return err
			}
			id, err := startHeartbeatProbe(ctx, client, prefix, 0, false, false)
			if err != nil {
				return err
			}
			old, _, err := waitSDKCalls(ctx, func(_ string, c faultproxy.Calls) bool { return strings.Contains(c.Payload, id) })
			if err != nil {
				return err
			}
			if err := sdkCompose(ctx, dir, prefix, "stop", "-t", "0"); err != nil {
				return err
			}
			if err := sdkCompose(ctx, dir, prefix, "start"); err != nil {
				return err
			}
			fresh, _, err := waitSDKCalls(ctx, func(jobID string, c faultproxy.Calls) bool { return jobID != old && strings.Contains(c.Payload, id) })
			if err != nil {
				return err
			}
			before, err := callbackSnapshot(ctx, client, id)
			if err != nil {
				return err
			}
			claims, err := heartbeatClaims(ctx, id)
			if err != nil {
				return err
			}
			if err := client.CompleteJob(ctx, old, claims[old].Worker, map[string]any{"stale": true}); err != nil {
				return err
			}
			after, err := callbackSnapshot(ctx, client, id)
			if err != nil {
				return err
			}
			if before != after {
				return fmt.Errorf("stale killed worker changed replacement state")
			}
			if err := releaseFault(ctx); err != nil {
				return err
			}
			if err := heartbeatOutcome(ctx, client, id, 1, 2, 2); err != nil {
				return err
			}
			instance, err := client.GetInstance(ctx, id)
			if err != nil {
				return err
			}
			if instance.Variables["attemptId"] != fresh {
				return fmt.Errorf("wrong replacement job %v", instance.Variables["attemptId"])
			}
			return nil
		}},
		{"cancel and interrupting timer stop renewal", func() error {
			for _, timer := range []bool{false, true} {
				if err := setFault(ctx, prefix, "", "complete"); err != nil {
					return err
				}
				id, err := startHeartbeatProbe(ctx, client, prefix, 90000, false, timer)
				if err != nil {
					return err
				}
				jobID, err := waitHeartbeatClaim(ctx, id)
				if err != nil {
					return err
				}
				if !timer {
					if _, err := client.CancelInstance(ctx, id, "heartbeat cancellation"); err != nil {
						return err
					}
				}
				instance, err := PollUntilTerminal(ctx, client, id, 10*time.Second)
				if err != nil {
					return err
				}
				wanted := "CANCELLED"
				if timer {
					wanted = "COMPLETED"
				}
				if instance.Status != wanted {
					return fmt.Errorf("interruption status=%s", instance.Status)
				}
				// One heartbeat interval plus margin lets the SDK observe loss.
				time.Sleep(12 * time.Second)
				before, err := heartbeatStats(ctx)
				if err != nil {
					return err
				}
				time.Sleep(2 * time.Second)
				after, err := heartbeatStats(ctx)
				if err != nil {
					return err
				}
				if before[jobID] != after[jobID] {
					return fmt.Errorf("terminal attempt kept renewing")
				}
				calls, err := faultControl(ctx, "", nil)
				if err != nil {
					return err
				}
				if calls[jobID].Complete != 0 || calls[jobID].Fail != 0 {
					return fmt.Errorf("cancelled handler sent a callback: %+v", calls[jobID])
				}
			}
			return nil
		}},
	}
	for _, test := range tests {
		if err := test.run(); err != nil {
			t.Errorf("%s: %v", test.name, err)
			return
		}
		t.Logf("%s: passed", test.name)
	}
}

func heartbeatPending(ctx context.Context, client ClientIface, prefix string, failure bool) error {
	kind := "complete"
	if failure {
		kind = "fail"
	}
	if err := setFault(ctx, prefix, "hold-before", kind); err != nil {
		return err
	}
	id, err := startHeartbeatProbe(ctx, client, prefix, 0, failure, false)
	if err != nil {
		return err
	}
	jobID, _, err := waitSDKCalls(ctx, func(_ string, c faultproxy.Calls) bool { return strings.Contains(c.Payload, id) })
	if err != nil {
		return err
	}
	time.Sleep(35 * time.Second)
	claims, err := heartbeatClaims(ctx, id)
	if err != nil {
		return err
	}
	beats, err := heartbeatStats(ctx)
	if err != nil {
		return err
	}
	if len(claims) != 1 || beats[jobID].Count < 4 {
		return fmt.Errorf("pending callback lost lease: claims=%d heartbeats=%d", len(claims), beats[jobID].Count)
	}
	if err := releaseFault(ctx); err != nil {
		return err
	}
	handlers, budget, attempts := float64(1), float64(2), 1
	if failure {
		handlers, budget, attempts = 2, 1, 2
	}
	if err := heartbeatOutcome(ctx, client, id, handlers, budget, attempts); err != nil {
		return err
	}
	calls, err := faultControl(ctx, "", nil)
	if err != nil {
		return err
	}
	if calls[jobID].Changed || (failure && calls[jobID].Complete != 0) || (!failure && calls[jobID].Fail != 0) {
		return fmt.Errorf("pending callback changed outcome: %+v", calls[jobID])
	}
	return nil
}
