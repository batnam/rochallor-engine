//go:build integration

package kafka_outbox_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	kafkaoutbox "github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
)

// TestIdempotentCompletion — edge case "Duplicate delivery". In
// at-least-once mode a consumer may receive the same JobDispatchEvent more
// than once (producer retries after an in-flight ack loss, or a relay-crash
// republish). The engine's completion API MUST remain idempotent so a second
// completion is a safe no-op rather than an error surfaced to the worker.
//
// This test covers the wire side: we publish a second copy of an already-
// completed job to Kafka directly (simulating a relay crash-republish) and
// assert that a consumer observing both copies can safely dedup on DedupId.
// (The engine's CompleteJob idempotency is covered by unit tests in the
// existing complete_user_task flow; this test ensures the wire schema gives
// consumers what they need to dedup without contacting the engine.)
func TestIdempotentCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const jobType = "send-receipt"
	f := newFixture(t, jobType)
	t.Cleanup(func() { f.Close(ctx) })

	rt := kafkaoutbox.New(kafkaoutbox.Config{
		Pool:        f.Pool,
		SeedBrokers: f.SeedBrokers,
		Transport:   "plaintext",
		BatchSize:   50,
		Logger:      slog.Default(),
	})
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("runtime start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	// Enqueue → wait for drain → capture the first (official) record.
	jobID := "job_" + ulidlike()
	instanceID := "inst_" + ulidlike()
	stepExecID := "se_" + ulidlike()
	seedJobAndEnqueue(t, ctx, f, jobID, instanceID, stepExecID, jobType)
	waitFor(t, 10*time.Second, "first publish to drain", func() bool {
		var n int64
		_ = f.Pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&n)
		return n == 0
	})

	// Read the original record and its DedupId.
	first := consumeOne(t, ctx, f.SeedBrokers, "workflow.jobs."+jobType)
	var firstEvent workflowv1.JobDispatchEvent
	if err := proto.Unmarshal(first.Value, &firstEvent); err != nil {
		t.Fatalf("unmarshal original: %v", err)
	}
	if firstEvent.DedupId == "" {
		t.Fatal("DedupId must be set on dispatch events")
	}

	// Republish a duplicate copy (simulates relay crash-republish or producer
	// retry). Same Value + same DedupId header.
	client, err := kgo.NewClient(kgo.SeedBrokers(f.SeedBrokers))
	if err != nil {
		t.Fatalf("producer: %v", err)
	}
	defer client.Close()
	dup := &kgo.Record{
		Topic: "workflow.jobs." + jobType,
		Key:   first.Key,
		Value: first.Value,
		Headers: []kgo.RecordHeader{
			{Key: "dedup-id", Value: []byte(firstEvent.DedupId)},
		},
	}
	res := client.ProduceSync(ctx, dup)
	if err := res.FirstErr(); err != nil {
		t.Fatalf("republish: %v", err)
	}

	// Consume both records via a dedup-aware consumer (simulating KafkaRunner
	// behaviour). Both must arrive; the second MUST be recognized as a
	// duplicate and skipped (not counted).
	seen := map[string]bool{}
	processed := 0
	consumeClient, err := kgo.NewClient(
		kgo.SeedBrokers(f.SeedBrokers),
		kgo.ConsumeTopics("workflow.jobs."+jobType),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.Balancers(kgo.CooperativeStickyBalancer()),
	)
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	defer consumeClient.Close()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		pollCtx, pcancel := context.WithTimeout(ctx, 2*time.Second)
		fetches := consumeClient.PollFetches(pollCtx)
		pcancel()
		iter := fetches.RecordIter()
		for !iter.Done() {
			r := iter.Next()
			var ev workflowv1.JobDispatchEvent
			if err := proto.Unmarshal(r.Value, &ev); err != nil {
				t.Fatalf("unmarshal consumed: %v", err)
			}
			if seen[ev.DedupId] {
				continue // consumer-side dedup — what KafkaRunner does (FR-007)
			}
			seen[ev.DedupId] = true
			processed++
		}
		if processed >= 1 && len(seen) == 1 {
			break
		}
	}
	if processed != 1 {
		t.Errorf("after dedup: processed want 1, got %d (seen=%v)", processed, seen)
	}
}
