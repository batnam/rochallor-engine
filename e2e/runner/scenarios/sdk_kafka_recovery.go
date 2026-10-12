package scenarios

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/batnam/e2e/runner/faultproxy"
	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"google.golang.org/protobuf/proto"
)

func RunSDKKafkaRecovery(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	defer releaseFault(context.Background())
	broker, err := kgo.NewClient(kgo.SeedBrokers("localhost:9092"), kgo.RecordPartitioner(kgo.ManualPartitioner()))
	if err != nil {
		t.Errorf("broker: %v", err)
		return
	}
	defer broker.Close()
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"pending-middle-record", func(ctx context.Context) error { return checkSDKPendingOffset(ctx, client, broker, prefix) }},
		{"kill-before-callback", func(ctx context.Context) error {
			return checkSDKWorkerRestart(ctx, client, broker, scenariosDir, prefix, "hold-before", 1, false)
		}},
		{"kill-after-callback-before-commit", func(ctx context.Context) error {
			return checkSDKWorkerRestart(ctx, client, broker, scenariosDir, prefix, "hold-after", 1, false)
		}},
		{"kill-during-retry", func(ctx context.Context) error {
			return checkSDKWorkerRestart(ctx, client, broker, scenariosDir, prefix, "hold-before", 2, false)
		}},
		{"shutdown-pending", func(ctx context.Context) error {
			return checkSDKWorkerRestart(ctx, client, broker, scenariosDir, prefix, "hold-before", 1, true)
		}},
		{"rebalance-pending", func(ctx context.Context) error { return checkSDKRebalance(ctx, client, broker, scenariosDir, prefix) }},
		{"malformed-record", func(ctx context.Context) error { return checkSDKPoison(ctx, client, broker, prefix) }},
	}
	for _, test := range cases {
		// Client defaults allow 45s without heartbeats before crash recovery.
		caseCtx, caseCancel := context.WithTimeout(ctx, 90*time.Second)
		err := test.run(caseCtx)
		caseCancel()
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			return
		}
		t.Logf("%s: workflow and committed offsets verified", test.name)
	}
}

func kafkaCommitted(ctx context.Context, broker *kgo.Client, prefix string) (int64, error) {
	group, topic := "workflow.workers."+prefix+"-sdk-recovery", "workflow.jobs."+prefix+"-sdk-recovery"
	request := kmsg.NewPtrOffsetFetchRequest()
	request.Group, request.Topics = group, []kmsg.OffsetFetchRequestTopic{{Topic: topic, Partitions: []int32{0}}}
	g := kmsg.NewOffsetFetchRequestGroup()
	g.Group, g.Topics = group, []kmsg.OffsetFetchRequestGroupTopic{{Topic: topic, Partitions: []int32{0}}}
	request.Groups = []kmsg.OffsetFetchRequestGroup{g}
	response, err := request.RequestWith(ctx, broker)
	if err != nil {
		return 0, err
	}
	if response.ErrorCode != 0 {
		return 0, fmt.Errorf("offset fetch error %d", response.ErrorCode)
	}
	for _, group := range response.Groups {
		if group.ErrorCode != 0 {
			return 0, fmt.Errorf("group offset error %d", group.ErrorCode)
		}
		for _, topic := range group.Topics {
			for _, partition := range topic.Partitions {
				if partition.ErrorCode != 0 {
					return 0, fmt.Errorf("partition offset error %d", partition.ErrorCode)
				}
				return partition.Offset, nil
			}
		}
	}
	for _, topic := range response.Topics {
		for _, partition := range topic.Partitions {
			if partition.ErrorCode != 0 {
				return 0, fmt.Errorf("partition offset error %d", partition.ErrorCode)
			}
			return partition.Offset, nil
		}
	}
	return -1, fmt.Errorf("missing committed offset response")
}

func sdkKafkaRecord(ctx context.Context, prefix, instance string) (*kgo.Record, error) {
	consumer, err := kgo.NewClient(kgo.SeedBrokers("localhost:9092"), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
		"workflow.jobs." + prefix + "-sdk-recovery": {0: kgo.NewOffset().AtStart()},
	}))
	if err != nil {
		return nil, err
	}
	defer consumer.Close()
	for ctx.Err() == nil {
		fetches := consumer.PollFetches(ctx)
		if err := fetches.Err(); err != nil {
			return nil, err
		}
		for _, record := range fetches.Records() {
			var event workflowv1.JobDispatchEvent
			if proto.Unmarshal(record.Value, &event) == nil && event.InstanceId == instance {
				return record, nil
			}
		}
	}
	return nil, ctx.Err()
}

func assertUncommitted(ctx context.Context, broker *kgo.Client, prefix string, record *kgo.Record) error {
	committed, err := kafkaCommitted(ctx, broker, prefix)
	if err != nil {
		return err
	}
	if committed > record.Offset {
		return fmt.Errorf("committed %d past unconfirmed record %d", committed, record.Offset)
	}
	return nil
}

func pendingSDKJob(ctx context.Context, client ClientIface, prefix, mode string, attempts int) (string, *kgo.Record, error) {
	if err := setFault(ctx, prefix, mode, "complete"); err != nil {
		return "", nil, err
	}
	id, err := startSDKProbe(ctx, client, prefix, false)
	if err != nil {
		return "", nil, err
	}
	_, _, err = waitSDKCalls(ctx, func(_ string, call faultproxy.Calls) bool {
		return strings.Contains(call.Payload, id) && call.Complete >= attempts && (mode != "hold-after" || call.Acknowledged > 0)
	})
	if err != nil {
		return "", nil, err
	}
	record, err := sdkKafkaRecord(ctx, prefix, id)
	return id, record, err
}

func completedSDKProbe(ctx context.Context, client ClientIface, id string) error {
	inst, err := PollUntilTerminal(ctx, client, id, 75*time.Second)
	if err != nil {
		return err
	}
	if inst.Status != "COMPLETED" || inst.Variables["handlerCalls"] != float64(1) || inst.Variables["remaining"] != float64(2) {
		return fmt.Errorf("unexpected recovery outcome: %+v", inst)
	}
	return nil
}

func checkSDKPendingOffset(ctx context.Context, client ClientIface, broker *kgo.Client, prefix string) error {
	id, record, err := pendingSDKJob(ctx, client, prefix, "hold-before", 2)
	if err != nil {
		return err
	}
	next, err := startSDKProbe(ctx, client, prefix, false)
	if err != nil {
		return err
	}
	if _, err := sdkKafkaRecord(ctx, prefix, next); err != nil {
		return err
	}
	if err := assertUncommitted(ctx, broker, prefix, record); err != nil {
		return err
	}
	inst, err := client.GetInstance(ctx, next)
	if err != nil {
		return err
	}
	if inst.Status != "ACTIVE" {
		return fmt.Errorf("later record advanced while predecessor pending: %+v", inst)
	}
	if err := releaseFault(ctx); err != nil {
		return err
	}
	for _, instance := range []string{id, next} {
		if err := completedSDKProbe(ctx, client, instance); err != nil {
			return err
		}
	}
	return nil
}

func checkSDKWorkerRestart(ctx context.Context, client ClientIface, broker *kgo.Client, dir, prefix, mode string, attempts int, graceful bool) error {
	id, record, err := pendingSDKJob(ctx, client, prefix, mode, attempts)
	if err != nil {
		return err
	}
	if err := assertUncommitted(ctx, broker, prefix, record); err != nil {
		return err
	}
	var before string
	if mode == "hold-after" {
		before, err = callbackSnapshot(ctx, client, id)
		if err != nil {
			return err
		}
	}
	if graceful {
		err = sdkCompose(ctx, dir, prefix, "stop", "--timeout", "10")
	} else {
		err = sdkCompose(ctx, dir, prefix, "kill", "--signal", "SIGKILL")
		if err == nil {
			err = sdkCompose(ctx, dir, prefix, "stop", "--timeout", "0")
		}
	}
	if err != nil {
		return err
	}
	if err := assertUncommitted(ctx, broker, prefix, record); err != nil {
		return err
	}
	if err := releaseFault(ctx); err != nil {
		return err
	}
	if err := sdkCompose(ctx, dir, prefix, "up", "--no-deps", "--no-recreate", "--no-build", "-d"); err != nil {
		return err
	}
	if err := completedSDKProbe(ctx, client, id); err != nil {
		return err
	}
	if mode == "hold-after" {
		_, _, err = waitSDKCalls(ctx, func(_ string, call faultproxy.Calls) bool {
			return strings.Contains(call.Payload, id) && call.Complete >= 2
		})
		if err != nil {
			return err
		}
		after, err := callbackSnapshot(ctx, client, id)
		if err != nil {
			return err
		}
		if before != after {
			return fmt.Errorf("redelivery after commit changed persisted workflow")
		}
	}
	return nil
}

func checkSDKRebalance(ctx context.Context, client ClientIface, broker *kgo.Client, dir, prefix string) error {
	id, record, err := pendingSDKJob(ctx, client, prefix, "hold-before", 1)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("sdk-rebalance-%s-%d", prefix, time.Now().UnixNano())
	if err := sdkCompose(ctx, dir, prefix, "run", "--detach", "--no-deps", "--name", name, "-e", "WORKER_ID=worker-"+prefix+"-replacement"); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, "docker", "rm", "--force", name).Run()
	}()
	request := kmsg.NewPtrDescribeGroupsRequest()
	request.Groups = []string{"workflow.workers." + prefix + "-sdk-recovery"}
	joined := false
	for ctx.Err() == nil {
		response, err := request.RequestWith(ctx, broker)
		if err != nil {
			return err
		}
		if len(response.Groups) == 1 && len(response.Groups[0].Members) >= 2 {
			joined = true
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !joined {
		return fmt.Errorf("replacement never joined: %w", ctx.Err())
	}
	if err := sdkCompose(ctx, dir, prefix, "stop", "--timeout", "10"); err != nil {
		return err
	}
	if err := assertUncommitted(ctx, broker, prefix, record); err != nil {
		return err
	}
	if err := releaseFault(ctx); err != nil {
		return err
	}
	if err := completedSDKProbe(ctx, client, id); err != nil {
		return err
	}
	return sdkCompose(ctx, dir, prefix, "up", "--no-deps", "--no-recreate", "--no-build", "-d")
}

// Run last: malformed records deliberately block this disposable topic. The
// test never resets offsets to hide a poison record.
func checkSDKPoison(ctx context.Context, client ClientIface, broker *kgo.Client, prefix string) error {
	if err := setFault(ctx, prefix, "", ""); err != nil {
		return err
	}
	// Rebalance/restart must finish before injecting poison, otherwise an idle
	// worker could make the blocked-offset assertion pass without reading it.
	warmup, err := startSDKProbe(ctx, client, prefix, false)
	if err != nil {
		return err
	}
	if err := completedSDKProbe(ctx, client, warmup); err != nil {
		return fmt.Errorf("worker was not ready before poison: %w", err)
	}
	record := &kgo.Record{Topic: "workflow.jobs." + prefix + "-sdk-recovery", Partition: 0, Value: []byte{0xff}}
	if err := broker.ProduceSync(ctx, record).FirstErr(); err != nil {
		return err
	}
	id, err := startSDKProbe(ctx, client, prefix, false)
	if err != nil {
		return err
	}
	if _, err := sdkKafkaRecord(ctx, prefix, id); err != nil {
		return err
	}
	marker := map[string]string{
		"go":     "invalid record; stopping without commit",
		"node":   "kafkarunner: unacknowledged",
		"python": "DecodeError",
		"java":   "InvalidProtocolBufferException",
	}[prefix]
	position := fmt.Sprintf("topic=%s partition=0 offset=%d", record.Topic, record.Offset)
	if prefix == "go" { // The Go E2E worker uses structured slog JSON.
		position = fmt.Sprintf("\"topic\":%q,\"partition\":0,\"offset\":%d", record.Topic, record.Offset)
	}
	for {
		logs, err := exec.CommandContext(ctx, "docker", "logs", "e2e-worker-"+prefix).CombinedOutput()
		if err != nil {
			return fmt.Errorf("poison worker logs: %w", err)
		}
		if strings.Contains(string(logs), position) && strings.Contains(string(logs), marker) {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("worker never reported poison at %s: %w", position, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(3 * time.Second):
	}
	if err := assertUncommitted(ctx, broker, prefix, record); err != nil {
		return err
	}
	inst, err := client.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	if inst.Status != "ACTIVE" {
		return fmt.Errorf("poison record was skipped: %+v", inst)
	}
	return nil
}
