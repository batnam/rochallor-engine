//go:build integration

package kafka_outbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/config"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

func TestSwitchover_KafkaToPoll(t *testing.T) {
	ctx := context.Background()
	jobType := "s1"
	f := newFixture(t, jobType)
	t.Cleanup(func() { f.Close(ctx) })

	// Wait for Kafka metadata propagation.
	time.Sleep(2 * time.Second)

	defRepo := definition.NewRepository(f.Pool)
	evaluator := func(expr string, vars map[string]any) (any, error) { return expr, nil }
	instance.SetExpressionEvaluator(evaluator)

	// 1. Boot in kafka_outbox mode.
	kafkaRT := kafka_outbox.New(kafka_outbox.Config{
		Pool:        f.Pool,
		SeedBrokers: f.SeedBrokers,
		Transport:   config.KafkaTransportPlaintext,
		BatchSize:   10,
	})
	if err := kafkaRT.Start(ctx); err != nil {
		t.Fatalf("start kafka runtime: %v", err)
	}
	// We'll stop it manually later.

	instSvc := instance.NewService(f.Pool, defRepo, kafkaRT.Dispatcher())

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

	// Create a job.
	inst, err := instSvc.Start(ctx, def.ID, 1, map[string]any{}, "")
	if err != nil {
		t.Fatalf("start instance: %v", err)
	}

	// Verify it reached the outbox and then was deleted (published).
	waitFor(t, 5*time.Second, "job to be published (outbox row deleted)", func() bool {
		var exists bool
		f.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dispatch_outbox WHERE instance_id = $1)`, inst.ID).Scan(&exists)
		return !exists
	})

	// Now shut down kafka_outbox mode.
	kafkaRT.Stop(ctx)

	// 2. Switch to POLLING mode.
	pollDisp := polling.New().Dispatcher()
	_ = pollDisp // used to satisfy reboot requirement
	// In real engine, this happens on reboot.

	// Verify the job row is still UNLOCKED in DB.
	var status string
	err = f.Pool.QueryRow(ctx, `SELECT status FROM job WHERE instance_id = $1`, inst.ID).Scan(&status)
	if err != nil {
		t.Fatalf("find job: %v", err)
	}
	if status != "UNLOCKED" {
		t.Errorf("expected job status UNLOCKED, got %s", status)
	}

	// Polling worker picks it up.
	var jobID string
	err = f.Pool.QueryRow(ctx, `
		UPDATE job SET status = 'LOCKED', worker_id = 'poll-worker', locked_at = now(), lock_expires_at = now() + interval '30 seconds'
		WHERE instance_id = $1 AND status = 'UNLOCKED'
		RETURNING id`, inst.ID).Scan(&jobID)
	if err != nil {
		t.Fatalf("polling worker claim: %v", err)
	}

	// Now simulate the Kafka worker (which already has the message) also trying to complete it.
	// We'll use instSvc.CompleteJobAndAdvance (the real API call).
	err = instSvc.CompleteJobAndAdvance(ctx, jobID, "kafka-worker", map[string]any{"result": "from-kafka"})
	if err != nil {
		t.Fatalf("kafka worker complete: %v", err)
	}

	// Verify instance advanced (or at least job is COMPLETED).
	err = f.Pool.QueryRow(ctx, `SELECT status FROM job WHERE id = $1`, jobID).Scan(&status)
	if err != nil {
		t.Fatalf("check job status: %v", err)
	}
	if status != "COMPLETED" {
		t.Errorf("expected job status COMPLETED, got %s", status)
	}

	// Now the polling worker (which also has it) tries to complete it.
	// It should succeed (idempotent) or fail gracefully, but not double-advance.
	err = instSvc.CompleteJobAndAdvance(ctx, jobID, "poll-worker", map[string]any{"result": "from-poll"})
	if err != nil {
		// If it's strictly idempotent, it might return nil.
		// Let's see what the current implementation does.
		t.Logf("poll worker complete (duplicate): %v", err)
	}

	// Verify no double-advance. This is harder to check without seeing history.
	var historyCount int
	f.Pool.QueryRow(ctx, `SELECT count(*) FROM step_execution WHERE instance_id = $1`, inst.ID).Scan(&historyCount)
	// Since it's a 1-step workflow, historyCount should be 1.
	if historyCount != 1 {
		t.Errorf("expected 1 step execution (no double-advance), got %d", historyCount)
	}
}
