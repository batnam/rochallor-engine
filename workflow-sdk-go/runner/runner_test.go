package runner_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/runner"
)

// fakeEngine is a fake EngineClient for testing.
type fakeEngine struct {
	jobs      []client.Job
	polled    atomic.Int64
	completed atomic.Int64
	failed    atomic.Int64
	failRetry atomic.Bool
}

func (f *fakeEngine) PollJobs(_ context.Context, req client.PollJobsRequest) ([]client.Job, error) {
	f.polled.Add(1)
	if len(f.jobs) == 0 {
		return nil, nil
	}
	j := f.jobs[0]
	f.jobs = f.jobs[1:]
	return []client.Job{j}, nil
}

func (f *fakeEngine) CompleteJob(_ context.Context, _, _ string, _ map[string]any) error {
	f.completed.Add(1)
	return nil
}

func (f *fakeEngine) FailJob(_ context.Context, _, _, _ string, retryable bool) error {
	f.failed.Add(1)
	f.failRetry.Store(retryable)
	return nil
}

func TestRunnerDispatchesAndCompletes(t *testing.T) {
	eng := &fakeEngine{
		jobs: []client.Job{{ID: "j1", JobType: "hello", InstanceID: "i1"}},
	}
	reg := handler.New()
	reg.Register("hello", func(ctx context.Context, j handler.JobContext) (handler.Result, error) {
		return handler.Result{VariablesToSet: map[string]any{"done": true}}, nil
	})

	r := runner.New(runner.Config{
		WorkerID:     "w1",
		Parallelism:  1,
		PollInterval: 10 * time.Millisecond,
	}, eng, reg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r.Run(ctx)

	if eng.completed.Load() != 1 {
		t.Errorf("want 1 CompleteJob call, got %d", eng.completed.Load())
	}
}

func TestRunnerGracefulShutdown(t *testing.T) {
	eng := &fakeEngine{}
	reg := handler.New()
	r := runner.New(runner.Config{
		WorkerID:     "w1",
		PollInterval: 10 * time.Millisecond,
	}, eng, reg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop within 2s after ctx cancel")
	}
}

func TestRunnerNonRetryableFailure(t *testing.T) {
	eng := &fakeEngine{
		jobs: []client.Job{{ID: "j1", JobType: "bad", InstanceID: "i1"}},
	}
	reg := handler.New()
	reg.Register("bad", func(ctx context.Context, j handler.JobContext) (handler.Result, error) {
		return handler.Result{}, &retry.NonRetryable{Cause: errors.New("fatal")}
	})

	r := runner.New(runner.Config{
		WorkerID:     "w1",
		PollInterval: 10 * time.Millisecond,
	}, eng, reg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r.Run(ctx)

	if eng.failed.Load() != 1 {
		t.Errorf("want 1 FailJob call, got %d", eng.failed.Load())
	}
	if eng.failRetry.Load() != false {
		t.Errorf("want retryable=false for NonRetryable error")
	}
}
