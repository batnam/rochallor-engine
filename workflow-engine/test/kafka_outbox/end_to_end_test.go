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
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	kafkaoutbox "github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
)

// TestEndToEnd — US1 Acceptance #1. Enqueue a job via the kafka_outbox
// Dispatcher inside a tx, start the Runtime, and assert:
//   - the outbox row briefly exists then disappears (delete-on-publish),
//   - a Kafka message lands on workflow.jobs.<jobType> keyed by instance_id,
//   - an audit_log row with action=DISPATCHED_VIA_BROKER was recorded.
func TestEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const jobType = "send-email"
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

	// Seed a row in `job` so the outbox FK resolves, then Enqueue inside the
	// same tx. In production the enqueue is driven by instance.handleServiceTask;
	// here we drive it directly to keep the integration test focused on the
	// relay's drain → publish → delete invariants.
	jobID := "job_" + ulidlike()
	instanceID := "inst_" + ulidlike()
	stepExecID := "se_" + ulidlike()
	seedJobAndEnqueue(t, ctx, f, jobID, instanceID, stepExecID, jobType)

	// Assert outbox has exactly one pending row initially — drain is async
	// but the row must exist for at least a moment.
	// (On very fast drain the row may already be gone; accept either 0 or 1.)
	var count int64
	_ = f.Pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&count)
	if count > 1 {
		t.Fatalf("outbox count immediately after enqueue: got %d, want 0 or 1", count)
	}

	// Wait for the relay to drain the row.
	waitFor(t, 10*time.Second, "outbox row to drain", func() bool {
		var n int64
		_ = f.Pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&n)
		return n == 0
	})

	// Consume the message from Kafka.
	rec := consumeOne(t, ctx, f.SeedBrokers, "workflow.jobs."+jobType)
	if string(rec.Key) != instanceID {
		t.Errorf("record key: want %q, got %q", instanceID, string(rec.Key))
	}
	var event workflowv1.JobDispatchEvent
	if err := proto.Unmarshal(rec.Value, &event); err != nil {
		t.Fatalf("unmarshal JobDispatchEvent: %v", err)
	}
	if event.JobId != jobID {
		t.Errorf("JobDispatchEvent.JobId: want %q, got %q", jobID, event.JobId)
	}
	if event.InstanceId != instanceID {
		t.Errorf("JobDispatchEvent.InstanceId: want %q, got %q", instanceID, event.InstanceId)
	}
	if event.JobType != jobType {
		t.Errorf("JobDispatchEvent.JobType: want %q, got %q", jobType, event.JobType)
	}
	if event.DedupId == "" {
		t.Error("JobDispatchEvent.DedupId must be set")
	}

	// Audit row must exist.
	var auditKind string
	if err := f.Pool.QueryRow(ctx,
		`SELECT kind FROM audit_log WHERE instance_id = $1 AND kind = 'DISPATCHED_VIA_BROKER' LIMIT 1`,
		instanceID,
	).Scan(&auditKind); err != nil {
		t.Fatalf("audit_log lookup: %v", err)
	}
	if auditKind != "DISPATCHED_VIA_BROKER" {
		t.Errorf("audit kind: got %q", auditKind)
	}
}

// seedJobAndEnqueue inserts minimal parent rows (instance → step_execution →
// job) to satisfy the FK chain, then calls Dispatcher.Enqueue inside the same
// tx. Commits before returning so the relay can observe both rows.
func seedJobAndEnqueue(t *testing.T, ctx context.Context, f *testFixture, jobID, instanceID, stepExecID, jobType string) {
	t.Helper()
	tx, err := f.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	mustExec(t, ctx, tx,
		`INSERT INTO workflow_instance (id, definition_id, definition_version, status, current_step_ids, variables)
		 VALUES ($1, $2, 1, 'ACTIVE', $3, '{}'::jsonb)`,
		instanceID, "def-"+jobType, []string{"s1"},
	)
	mustExec(t, ctx, tx,
		`INSERT INTO step_execution (id, instance_id, step_id, step_type, attempt_number, status)
		 VALUES ($1, $2, 's1', 'SERVICE_TASK', 1, 'RUNNING')`,
		stepExecID, instanceID,
	)
	mustExec(t, ctx, tx,
		`INSERT INTO job (id, instance_id, step_execution_id, job_type, status, retries_remaining, payload)
		 VALUES ($1, $2, $3, $4, 'UNLOCKED', 3, '{}'::jsonb)`,
		jobID, instanceID, stepExecID, jobType,
	)
	if err := (kafkaoutbox.Dispatcher{}).Enqueue(ctx, tx, dispatch.DispatchJob{
		ID:               jobID,
		InstanceID:       instanceID,
		StepExecutionID:  stepExecID,
		JobType:          jobType,
		RetriesRemaining: 3,
		Payload:          []byte("{}"),
		CreatedAt:        time.Now(),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func mustExec(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// consumeOne subscribes to a topic and returns the first record seen, failing
// the test on timeout.
func consumeOne(t *testing.T, ctx context.Context, brokers, topic string) *kgo.Record {
	t.Helper()
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.Balancers(kgo.CooperativeStickyBalancer()),
	)
	if err != nil {
		t.Fatalf("kgo client: %v", err)
	}
	defer client.Close()
	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fetches := client.PollFetches(pollCtx)
	if errs := fetches.Errors(); len(errs) > 0 {
		t.Fatalf("poll fetches: %v", errs[0].Err)
	}
	iter := fetches.RecordIter()
	if iter.Done() {
		t.Fatalf("no record on topic %s after timeout", topic)
	}
	return iter.Next()
}
