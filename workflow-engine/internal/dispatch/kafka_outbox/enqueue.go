// Package kafka_outbox is the event-driven Dispatcher / Runtime used when the
// engine runs with WE_DISPATCH_MODE=kafka_outbox. The hot path (Enqueue)
// writes a dispatch_outbox row inside the caller's transaction; the relay
// goroutine drains those rows to Kafka with at-least-once semantics.
package kafka_outbox

import (
	"context"
	"fmt"

	"github.com/oklog/ulid/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
)

// schemaVersion is written into JobDispatchEvent.schema_version. Consumers
// treat higher values as "OK but there might be new fields I don't know"; the
// contract is carried by proto field numbers, not by this version.
const schemaVersion = 1

// Dispatcher is the event-driven Dispatcher. Its Enqueue serializes a
// JobDispatchEvent proto and INSERTs a row into dispatch_outbox inside the
// caller's transaction via OutboxStore. No network I/O happens here;
// publish is the relay's job.
type Dispatcher struct {
	store OutboxStore
}

// Enqueue writes one pending dispatch row. It is safe to call multiple times
// for the same tx (e.g., parallel branches emitting several jobs) — each call
// uses a fresh ULID so the rows coexist without conflicting.
func (d Dispatcher) Enqueue(ctx context.Context, tx db.Tx, job dispatch.DispatchJob) error {
	outboxID := ulid.Make().String()
	event := &workflowv1.JobDispatchEvent{
		SchemaVersion:    schemaVersion,
		DedupId:          outboxID,
		JobId:            job.ID,
		InstanceId:       job.InstanceID,
		StepExecutionId:  job.StepExecutionID,
		JobType:          job.JobType,
		RetriesRemaining: int32(job.RetriesRemaining),
		JobPayload:       job.Payload,
		CreatedAt:        timestamppb.New(job.CreatedAt),
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal JobDispatchEvent: %w", err)
	}
	return d.store.Enqueue(ctx, tx, OutboxRow{
		ID:         outboxID,
		JobID:      job.ID,
		InstanceID: job.InstanceID,
		JobType:    job.JobType,
		Payload:    payload,
	})
}
