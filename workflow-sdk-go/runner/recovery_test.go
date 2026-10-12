package runner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
)

type recoveryClient struct {
	poll     func(context.Context, client.PollJobsRequest) ([]client.Job, error)
	complete func(context.Context, map[string]any) error
	fail     func(context.Context, string, bool) error
}

func (c recoveryClient) PollJobs(ctx context.Context, req client.PollJobsRequest) ([]client.Job, error) {
	if c.poll != nil {
		return c.poll(ctx, req)
	}
	return nil, nil
}
func (c recoveryClient) CompleteJob(ctx context.Context, _, _ string, vars map[string]any) error {
	return c.complete(ctx, vars)
}
func (c recoveryClient) FailJob(ctx context.Context, _, _, reason string, retryable bool) error {
	return c.fail(ctx, reason, retryable)
}

func TestCallbackRetainsOutcome(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "fail"}[failure], func(t *testing.T) {
			var handlers, completes, fails atomic.Int32
			engine := recoveryClient{
				complete: func(ctx context.Context, vars map[string]any) error {
					if vars["value"] != float64(7) && vars["value"] != 7 {
						t.Errorf("output changed: %v", vars)
					}
					vars["value"] = 9
					if completes.Add(1) == 1 {
						return errors.New("lost response")
					}
					return nil
				},
				fail: func(ctx context.Context, reason string, retryable bool) error {
					if reason != "non-retryable: business" || retryable {
						t.Errorf("changed failure %s %v", reason, retryable)
					}
					if fails.Add(1) == 1 {
						return errors.New("unavailable")
					}
					return nil
				},
			}
			registry := handler.New()
			registry.Register("test", func(ctx context.Context, j handler.JobContext) (handler.Result, error) {
				handlers.Add(1)
				if failure {
					return handler.Result{}, &retry.NonRetryable{Cause: errors.New("business")}
				}
				return handler.Result{VariablesToSet: map[string]any{"value": 7}}, nil
			})
			deadline := time.Now().Add(time.Second)
			New(Config{WorkerID: "w"}, engine, registry).dispatch(context.Background(), client.Job{ID: "j", JobType: "test", LockExpiresAt: &deadline})
			if handlers.Load() != 1 || (failure && (fails.Load() != 2 || completes.Load() != 0)) || (!failure && (completes.Load() != 2 || fails.Load() != 0)) {
				t.Fatalf("handler=%d complete=%d fail=%d", handlers.Load(), completes.Load(), fails.Load())
			}
		})
	}
}

func TestClaimUsesOnlyFreeSlots(t *testing.T) {
	requests := make(chan int, 4)
	release := make(chan struct{})
	var next atomic.Int32
	engine := recoveryClient{poll: func(ctx context.Context, req client.PollJobsRequest) ([]client.Job, error) {
		requests <- req.MaxJobs
		return []client.Job{{ID: "j", JobType: "test"}}, nil
	}, complete: func(context.Context, map[string]any) error { return nil }, fail: func(context.Context, string, bool) error { return nil }}
	registry := handler.New()
	registry.Register("test", func(context.Context, handler.JobContext) (handler.Result, error) {
		next.Add(1)
		<-release
		return handler.Result{}, nil
	})
	r := New(Config{WorkerID: "w", Parallelism: 2}, engine, registry)
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	r.pollAndDispatch(context.Background(), sem, &wg, []string{"test"})
	r.pollAndDispatch(context.Background(), sem, &wg, []string{"test"})
	a, b := <-requests, <-requests
	close(release)
	wg.Wait()
	if a != 2 || b != 1 {
		t.Fatalf("want maxJobs=2,1; got %d,%d", a, b)
	}
}

func TestLeaseBoundsRetriesAndKeepsHandlerSlot(t *testing.T) {
	deadline := time.Now().Add(100 * time.Millisecond)
	var callbacks atomic.Int32
	engine := recoveryClient{complete: func(ctx context.Context, _ map[string]any) error { callbacks.Add(1); <-ctx.Done(); return ctx.Err() }, fail: func(context.Context, string, bool) error { t.Error("completion became failure"); return nil }}
	registry := handler.New()
	registry.Register("test", func(context.Context, handler.JobContext) (handler.Result, error) { return handler.Result{}, nil })
	start := time.Now()
	New(Config{}, engine, registry).dispatch(context.Background(), client.Job{JobType: "test", LockExpiresAt: &deadline})
	if time.Since(start) > 500*time.Millisecond || callbacks.Load() != 1 {
		t.Fatalf("lease not respected: %s, calls=%d", time.Since(start), callbacks.Load())
	}
}

func TestLegacyMissingLeaseMakesOneAttempt(t *testing.T) {
	var calls atomic.Int32
	engine := recoveryClient{complete: func(context.Context, map[string]any) error { calls.Add(1); return errors.New("unavailable") }, fail: func(context.Context, string, bool) error { t.Error("completion became failure"); return nil }}
	registry := handler.New()
	registry.Register("test", func(context.Context, handler.JobContext) (handler.Result, error) { return handler.Result{}, nil })
	New(Config{}, engine, registry).dispatch(context.Background(), client.Job{JobType: "test"})
	if calls.Load() != 1 {
		t.Fatalf("legacy calls=%d", calls.Load())
	}
}

func TestPendingCallbackAndExpiredHandlerKeepCapacity(t *testing.T) {
	for _, slowHandler := range []bool{false, true} {
		t.Run(map[bool]string{false: "callback", true: "handler"}[slowHandler], func(t *testing.T) {
			deadline := time.Now().Add(100 * time.Millisecond)
			started := make(chan struct{})
			release := make(chan struct{})
			var polls, callbacks atomic.Int32
			engine := recoveryClient{poll: func(context.Context, client.PollJobsRequest) ([]client.Job, error) {
				polls.Add(1)
				return []client.Job{{JobType: "test", LockExpiresAt: &deadline}}, nil
			}, complete: func(ctx context.Context, _ map[string]any) error {
				callbacks.Add(1)
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}, fail: func(context.Context, string, bool) error { t.Error("unexpected failure callback"); return nil }}
			registry := handler.New()
			registry.Register("test", func(context.Context, handler.JobContext) (handler.Result, error) {
				if slowHandler {
					close(started)
					<-release
				}
				return handler.Result{}, nil
			})
			r := New(Config{Parallelism: 1}, engine, registry)
			sem := make(chan struct{}, 1)
			var wg sync.WaitGroup
			r.pollAndDispatch(context.Background(), sem, &wg, []string{"test"})
			<-started
			if slowHandler {
				time.Sleep(120 * time.Millisecond)
			}
			r.pollAndDispatch(context.Background(), sem, &wg, []string{"test"})
			close(release)
			wg.Wait()
			if polls.Load() != 1 {
				t.Fatalf("over-claimed with full capacity: %d", polls.Load())
			}
			if slowHandler && callbacks.Load() != 0 {
				t.Fatal("expired handler sent callback")
			}
		})
	}
}
