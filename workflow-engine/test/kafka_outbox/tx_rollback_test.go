//go:build integration

package kafka_outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	kafkaoutbox "github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
)

// TestTxRollback — US1 Acceptance #4, FR-002 edge case. A rolled-back
// transaction MUST NOT leave an outbox row and MUST NOT publish to Kafka.
// This is the durability-over-speed invariant: we only publish work that
// has been committed.
func TestTxRollback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const jobType = "charge-card"
	f := newFixture(t, jobType)
	t.Cleanup(func() { f.Close(ctx) })

	// Begin a tx, enqueue — but rollback instead of commit.
	instanceID := "inst_" + ulidlike()
	stepExecID := "se_" + ulidlike()
	jobID := "job_" + ulidlike()

	tx, err := f.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	mustExec(t, ctx, tx,
		`INSERT INTO workflow_instance (id, definition_id, definition_version, status, current_step_ids, variables)
		 VALUES ($1, 'def', 1, 'ACTIVE', $2, '{}'::jsonb)`, instanceID, []string{"s1"})
	mustExec(t, ctx, tx,
		`INSERT INTO step_execution (id, instance_id, step_id, step_type, attempt_number, status)
		 VALUES ($1, $2, 's1', 'SERVICE_TASK', 1, 'RUNNING')`, stepExecID, instanceID)
	mustExec(t, ctx, tx,
		`INSERT INTO job (id, instance_id, step_execution_id, job_type, status, retries_remaining, payload)
		 VALUES ($1, $2, $3, $4, 'UNLOCKED', 3, '{}'::jsonb)`, jobID, instanceID, stepExecID, jobType)

	if err := (kafkaoutbox.Dispatcher{}).Enqueue(ctx, tx, dispatch.DispatchJob{
		ID: jobID, InstanceID: instanceID, StepExecutionID: stepExecID,
		JobType: jobType, RetriesRemaining: 3, Payload: []byte("{}"), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Roll back — this is the crux of the test.
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Assert nothing made it to the outbox post-rollback.
	var outboxCount int64
	_ = f.Pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&outboxCount)
	if outboxCount != 0 {
		t.Fatalf("outbox rows after rollback: want 0, got %d", outboxCount)
	}

	// And nothing made it to the job table either.
	var jobCount int64
	_ = f.Pool.QueryRow(ctx, `SELECT count(*) FROM job WHERE id = $1`, jobID).Scan(&jobCount)
	if jobCount != 0 {
		t.Fatalf("job rows after rollback: want 0, got %d", jobCount)
	}
}
