package scenarios

import (
	"context"
	"fmt"
	"os"
	"time"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

// callbackDelivery receives reserved jobs without an SDK worker completing them.
type callbackDelivery struct {
	client     ClientIface
	instanceID string
	jobType    string
	consumer   *kgo.Client
	seen       map[string]bool
}

func callbackJob(ctx context.Context, client ClientIface, prefix string) (*callbackDelivery, Job, error) {
	jobType := prefix + "-callback-probe"
	definitionID := fmt.Sprintf("%s-%d", jobType, time.Now().UnixNano())
	definition := fmt.Sprintf(`{"id":%q,"name":"Callback ordering","steps":[{"id":"task","name":"Task","type":"SERVICE_TASK","jobType":%q,"retryCount":2,"nextStep":"end"},{"id":"end","name":"End","type":"END"}]}`, definitionID, jobType)
	if err := client.UploadDefinition(ctx, []byte(definition)); err != nil {
		return nil, Job{}, err
	}
	instanceID, err := client.StartInstance(ctx, definitionID, nil)
	if err != nil {
		return nil, Job{}, err
	}
	d := &callbackDelivery{client: client, instanceID: instanceID, jobType: jobType, seen: make(map[string]bool)}
	if os.Getenv("WE_DISPATCH_MODE") == "kafka_outbox" {
		// Test topics have one partition. Start at the beginning and filter by
		// instance so dispatches published before subscription are not lost.
		d.consumer, err = kgo.NewClient(
			kgo.SeedBrokers("localhost:9092"),
			kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
				"workflow.jobs." + jobType: {0: kgo.NewOffset().AtStart()},
			}),
		)
		if err != nil {
			return nil, Job{}, err
		}
	}
	jobs, err := d.next(ctx, 10*time.Second)
	if err != nil || len(jobs) != 1 {
		d.close()
		return nil, Job{}, fmt.Errorf("expected one job, got %d (receive: %v)", len(jobs), err)
	}
	return d, jobs[0], nil
}

func (d *callbackDelivery) close() {
	if d.consumer != nil {
		d.consumer.Close()
	}
}

func (d *callbackDelivery) next(ctx context.Context, wait time.Duration) ([]Job, error) {
	if d.consumer == nil {
		return d.client.PollJobs(ctx, "callback-worker", d.jobType)
	}
	pollCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		fetches := d.consumer.PollFetches(pollCtx)
		if pollCtx.Err() != nil {
			return nil, ctx.Err()
		}
		if err := fetches.Err(); err != nil {
			return nil, err
		}
		var jobs []Job
		for _, record := range fetches.Records() {
			var event workflowv1.JobDispatchEvent
			if err := proto.Unmarshal(record.Value, &event); err != nil {
				return nil, err
			}
			// Relay redelivery of the same job is allowed; only a new job ID
			// represents another execution attempt.
			if event.InstanceId == d.instanceID && !d.seen[event.JobId] {
				d.seen[event.JobId] = true
				jobs = append(jobs, Job{ID: event.JobId, StepExecutionID: event.StepExecutionId, RetriesRemaining: int(event.RetriesRemaining)})
			}
		}
		if len(jobs) > 0 {
			return jobs, nil
		}
	}
}
