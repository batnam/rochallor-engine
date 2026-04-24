//go:build integration

package kafka_outbox_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/config"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
)

func TestSwitchover_PollToKafka(t *testing.T) {
	ctx := context.Background()
	// Use a simpler job type to avoid any potential topic naming issues.
	jobType := "s1"
	f := newFixture(t, jobType)
	t.Cleanup(func() { f.Close(ctx) })

	// 1. Boot engine in POLLING mode.
	defRepo := definition.NewRepository(f.Pool)
	evaluator := func(expr string, vars map[string]any) (any, error) { return expr, nil }
	instance.SetExpressionEvaluator(evaluator)

	pollDisp := polling.New().Dispatcher()
	instSvc := instance.NewService(f.Pool, defRepo, pollDisp)

	def := &definition.WorkflowDefinition{
		ID:   "def-" + jobType,
		Name: "Test Switchover",
		Steps: []definition.WorkflowStep{
			{ID: "s1", Type: definition.StepTypeServiceTask},
		},
	}
	if _, err := defRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload def: %v", err)
	}

	inst, err := instSvc.Start(ctx, def.ID, 1, map[string]any{}, "")
	if err != nil {
		t.Fatalf("start instance: %v", err)
	}

	var jobID string
	err = f.Pool.QueryRow(ctx, `SELECT id FROM job WHERE instance_id = $1 AND status = 'UNLOCKED'`, inst.ID).Scan(&jobID)
	if err != nil {
		t.Fatalf("find job: %v", err)
	}

	// 2. Now "Switch" to kafka_outbox mode.
	// Simulate the job being LOCKED but then worker dies, so it expires.
	_, err = f.Pool.Exec(ctx, `UPDATE job SET status = 'LOCKED', worker_id = 'worker1', locked_at = now() - interval '1 minute', lock_expires_at = now() - interval '30 seconds' WHERE id = $1`, jobID)
	if err != nil {
		t.Fatalf("lock job: %v", err)
	}

	kafkaRT := kafka_outbox.New(kafka_outbox.Config{
		Pool:        f.Pool,
		SeedBrokers: f.SeedBrokers,
		Transport:   config.KafkaTransportPlaintext,
		BatchSize:   10,
		Logger:      slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err := kafkaRT.Start(ctx); err != nil {
		t.Fatalf("start kafka runtime: %v", err)
	}
	t.Cleanup(func() { kafkaRT.Stop(ctx) })

	// Start the lease sweeper with a cancellable context.
	sweeperCtx, cancelSweeper := context.WithCancel(ctx)
	t.Cleanup(cancelSweeper)
	job.StartLeaseSweeper(sweeperCtx, f.Pool, kafkaRT.Dispatcher(), 100*time.Millisecond)

	// Verify it was re-enqueued (audit log row created by relay).
	t.Log("Waiting for audit log row...")
	waitFor(t, 20*time.Second, "audit log row for dispatch", func() bool {
		var exists bool
		f.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_log WHERE kind = 'DISPATCHED_VIA_BROKER' AND detail->>'job_id' = $1)`, jobID).Scan(&exists)
		if !exists {
			var outboxCount int
			f.Pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&outboxCount)
			t.Logf("Audit row not found yet. Current outbox count: %d", outboxCount)
		}
		return exists
	})

	// Final check: job is now UNLOCKED again (from its perspective) but outbox row is gone.
	var status string
	err = f.Pool.QueryRow(ctx, `SELECT status FROM job WHERE id = $1`, jobID).Scan(&status)
	if err != nil {
		t.Fatalf("check job status: %v", err)
	}
	if status != "UNLOCKED" {
		t.Errorf("expected job status UNLOCKED, got %s", status)
	}
	t.Log("Test finished successfully")
}
