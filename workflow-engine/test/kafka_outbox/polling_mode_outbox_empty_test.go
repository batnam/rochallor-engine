//go:build integration

package kafka_outbox_test

import (
	"context"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
)

// TestPollingModeOutboxEmpty — US2 Acceptance #2. In polling mode, assert that
// creating and completing jobs never writes to the dispatch_outbox table.
func TestPollingModeOutboxEmpty(t *testing.T) {
	ctx := context.Background()
	f := newPostgresFixture(t)
	t.Cleanup(func() { f.Close(ctx) })

	// 1. Setup engine services in polling mode.
	defRepo := definition.NewRepository(f.Pool)
	rt := polling.New()
	instSvc := instance.NewService(f.Pool, defRepo, rt.Dispatcher())

	// 2. Create a workflow definition with a SERVICE_TASK.
	def := &definition.WorkflowDefinition{
		ID:   "test-polling-outbox",
		Name: "Test Polling Outbox",
		Steps: []definition.WorkflowStep{
			{
				ID:      "s1",
				Type:    definition.StepTypeServiceTask,
				JobType: "test-job",
				Name:    "Step 1",
			},
		},
	}
	if _, err := defRepo.Upload(ctx, def); err != nil {
		t.Fatalf("create definition: %v", err)
	}

	// 3. Start N instances and verify outbox is empty throughout.
	const N = 5
	for i := 0; i < N; i++ {
		inst, err := instSvc.Start(ctx, def.ID, 0, map[string]any{"i": i}, "")
		if err != nil {
			t.Fatalf("start instance %d: %v", i, err)
		}

		// Assert outbox is empty after job creation.
		var count int
		err = f.Pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&count)
		if err != nil {
			t.Fatalf("query outbox: %v", err)
		}
		if count != 0 {
			t.Errorf("iteration %d: outbox count after Start: got %d, want 0", i, count)
		}

		// 4. Poll and complete the job.
		jobs, err := job.Poll(ctx, f.Pool, "worker-1", []string{"test-job"}, 1)
		if err != nil {
			t.Fatalf("poll job %d: %v", i, err)
		}
		if len(jobs) != 1 {
			t.Fatalf("poll job %d: got %d jobs, want 1", i, len(jobs))
		}

		err = instSvc.CompleteJobAndAdvance(ctx, jobs[0].ID, "worker-1", nil)
		if err != nil {
			t.Fatalf("complete job %d: %v", i, err)
		}

		// Assert outbox is still empty after completion.
		err = f.Pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&count)
		if err != nil {
			t.Fatalf("query outbox: %v", err)
		}
		if count != 0 {
			t.Errorf("iteration %d: outbox count after Complete: got %d, want 0", i, count)
		}

		// Final check on instance status.
		updated, err := instSvc.Get(ctx, inst.ID)
		if err != nil {
			t.Fatalf("get instance: %v", err)
		}
		if updated.Status != "COMPLETED" {
			t.Errorf("instance status: got %q, want COMPLETED", updated.Status)
		}
	}
}
