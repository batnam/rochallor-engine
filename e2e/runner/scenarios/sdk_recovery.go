package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/batnam/e2e/runner/faultproxy"
)

func faultControl(ctx context.Context, path string, rule *faultproxy.Rule) (map[string]faultproxy.Calls, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	port := os.Getenv("SDK_FAULT_PROXY_PORT")
	if port == "" {
		port = "19095"
	}
	method := http.MethodGet
	var body []byte
	if rule != nil {
		method = http.MethodPost
		body, _ = json.Marshal(rule)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://localhost:"+port+"/__faults"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		data, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("fault proxy: %d %s", response.StatusCode, data)
	}
	if response.StatusCode == 204 {
		return nil, nil
	}
	var calls map[string]faultproxy.Calls
	err = json.NewDecoder(response.Body).Decode(&calls)
	return calls, err
}

func setFault(ctx context.Context, prefix, mode, kind string) error {
	_, err := faultControl(ctx, "", &faultproxy.Rule{Mode: mode, Callback: kind, WorkerPrefix: "worker-" + prefix, Match: "sdkRecovery"})
	return err
}

func releaseFault(ctx context.Context) error {
	_, err := faultControl(ctx, "/release", &faultproxy.Rule{})
	return err
}

func waitSDKCalls(ctx context.Context, match func(string, faultproxy.Calls) bool) (string, faultproxy.Calls, error) {
	for ctx.Err() == nil {
		calls, err := faultControl(ctx, "", nil)
		if err != nil {
			return "", faultproxy.Calls{}, err
		}
		for id, call := range calls {
			if match(id, call) {
				return id, call, nil
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return "", faultproxy.Calls{}, ctx.Err()
}

func startSDKProbe(ctx context.Context, client ClientIface, prefix string, failure bool) (string, error) {
	jobType := prefix + "-sdk-recovery"
	if failure {
		jobType = prefix + "-sdk-failure"
	}
	id := fmt.Sprintf("%s-%d", jobType, time.Now().UnixNano())
	definition := fmt.Sprintf(`{"id":%q,"name":"SDK callback recovery","steps":[{"id":"task","name":"Task","type":"SERVICE_TASK","jobType":%q,"retryCount":2,"nextStep":"end"},{"id":"end","name":"End","type":"END"}]}`, id, jobType)
	if err := client.UploadDefinition(ctx, []byte(definition)); err != nil {
		return "", err
	}
	return client.StartInstance(ctx, id, nil)
}

// RunSDKCallbackRecovery injects real transport failures around the engine
// callback. Persisted variables/history and retry budget are the oracle.
func RunSDKCallbackRecovery(t TestReporter, client ClientIface, _ string, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	defer releaseFault(context.Background())
	for _, kind := range []string{"complete", "fail"} {
		for _, mode := range []string{"fail-once", "lost-response"} {
			if err := checkSDKCallback(ctx, client, prefix, mode, kind); err != nil {
				t.Errorf("%s/%s: %v", kind, mode, err)
				return
			}
			t.Logf("%s/%s: unchanged callback retried; workflow completed once", kind, mode)
		}
	}
}

func checkSDKCallback(parent context.Context, client ClientIface, prefix, mode, kind string) error {
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	if err := setFault(ctx, prefix, mode, kind); err != nil {
		return err
	}
	failure := kind == "fail"
	id, err := startSDKProbe(ctx, client, prefix, failure)
	if err != nil {
		return err
	}
	inst, err := PollUntilTerminal(ctx, client, id, 20*time.Second)
	if err != nil {
		return err
	}
	wantCalls, wantBudget := float64(1), float64(2)
	if failure {
		wantCalls, wantBudget = 2, 1
	}
	if inst.Status != "COMPLETED" || inst.Variables["handlerCalls"] != wantCalls || inst.Variables["remaining"] != wantBudget {
		return fmt.Errorf("handler reran, retry budget changed or workflow did not complete: %+v", inst)
	}
	_, call, err := waitSDKCalls(ctx, func(_ string, call faultproxy.Calls) bool {
		return strings.Contains(call.Payload, id) && ((kind == "complete" && call.Complete >= 2) || (kind == "fail" && call.Fail >= 2))
	})
	if err != nil {
		return fmt.Errorf("callback was not retried: %w", err)
	}
	if call.Changed || (kind == "complete" && call.Fail != 0) || (kind == "fail" && call.Complete != 0) {
		return fmt.Errorf("callback outcome changed: %+v", call)
	}
	history, err := client.GetHistory(ctx, id)
	if err != nil {
		return err
	}
	ends, tasks := 0, 0
	for _, step := range history {
		if step.StepID == "end" {
			ends++
		}
		if step.StepID == "task" {
			tasks++
		}
	}
	if ends != 1 || tasks != 1 {
		return fmt.Errorf("duplicate workflow progression: task=%d end=%d", tasks, ends)
	}
	return nil
}

func sdkCompose(ctx context.Context, scenariosDir, prefix string, args ...string) error {
	compose, service := "docker-compose-polling.yml", "worker-"+prefix
	if os.Getenv("WE_DISPATCH_MODE") == "kafka_outbox" {
		compose, service = "docker-compose-kafka-outbox.yml", "e2e-worker-"+prefix
	}
	command := append([]string{"compose", "-f", filepath.Join(scenariosDir, "..", compose)}, args...)
	command = append(command, service)
	output, err := exec.CommandContext(ctx, "docker", command...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("worker %v: %w: %s", args, err, output)
	}
	return nil
}
