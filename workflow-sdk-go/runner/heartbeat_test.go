package runner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
)

type heartbeatClient struct {
	recoveryClient
	extend func(context.Context, string, string, int) (time.Time, error)
}

func (c heartbeatClient) ExtendJobLease(ctx context.Context, j, w string, s int) (time.Time, error) {
	return c.extend(ctx, j, w, s)
}

func TestHeartbeatCoversLongHandlerAndPendingCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var heartbeats, handlers, callbacks atomic.Int32
		var handlerFinished time.Time
		engine := heartbeatClient{extend: func(ctx context.Context, j, w string, s int) (time.Time, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("unbounded heartbeat call")
			}
			heartbeats.Add(1)
			return time.Now().Add(30 * time.Second), nil
		}, recoveryClient: recoveryClient{complete: func(ctx context.Context, vars map[string]any) error {
			callbacks.Add(1)
			if vars["answer"] != float64(42) {
				t.Errorf("mutated outcome: %v", vars)
			}
			vars["answer"] = 99
			if time.Since(handlerFinished) < 35*time.Second {
				return errors.New("network outage")
			}
			return nil
		}, fail: func(context.Context, string, bool) error { t.Error("completion became failure"); return nil }}}
		registry := handler.New()
		registry.Register("long", func(ctx context.Context, _ handler.JobContext) (handler.Result, error) {
			handlers.Add(1)
			time.Sleep(90 * time.Second)
			if ctx.Err() != nil {
				t.Fatal("healthy handler lost its lease")
			}
			handlerFinished = time.Now()
			return handler.Result{VariablesToSet: map[string]any{"answer": 42}}, nil
		})
		expiry := time.Now().Add(30 * time.Second)
		New(Config{WorkerID: "w", Heartbeat: true}, engine, registry).dispatch(t.Context(), client.Job{ID: "j", JobType: "long", LockExpiresAt: &expiry})
		if handlers.Load() != 1 || callbacks.Load() < 2 || heartbeats.Load() < 13 {
			t.Fatalf("handler=%d callbacks=%d heartbeat=%d", handlers.Load(), callbacks.Load(), heartbeats.Load())
		}
	})
}

func TestHeartbeatLossCancelsHandlerButRetainsSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var polls, beats, callbacks atomic.Int32
		started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		expiry := time.Now().Add(30 * time.Second)
		engine := heartbeatClient{extend: func(context.Context, string, string, int) (time.Time, error) {
			if beats.Add(1) == 1 {
				return time.Now().Add(30 * time.Second), nil
			}
			return time.Time{}, client.ErrLeaseLost
		},
			recoveryClient: recoveryClient{poll: func(context.Context, client.PollJobsRequest) ([]client.Job, error) {
				polls.Add(1)
				return []client.Job{{ID: "j", JobType: "long", LockExpiresAt: &expiry}}, nil
			}, complete: func(context.Context, map[string]any) error { callbacks.Add(1); return nil }, fail: func(context.Context, string, bool) error { callbacks.Add(1); return nil }}}
		registry := handler.New()
		registry.Register("long", func(ctx context.Context, _ handler.JobContext) (handler.Result, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-release
			return handler.Result{}, nil
		})
		r := New(Config{WorkerID: "w", Heartbeat: true, Parallelism: 1}, engine, registry)
		sem := make(chan struct{}, 1)
		var wg sync.WaitGroup
		r.pollAndDispatch(t.Context(), sem, &wg, []string{"long"})
		<-started
		<-cancelled
		r.pollAndDispatch(t.Context(), sem, &wg, []string{"long"})
		if polls.Load() != 1 || len(sem) != 1 {
			t.Fatal("lost lease released capacity before handler stopped")
		}
		close(release)
		wg.Wait()
		if callbacks.Load() != 0 {
			t.Fatal("lost attempt sent callback")
		}
	})
}

func TestHeartbeatFailureIsBoundedByLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		expiry := time.Now().Add(5 * time.Second)
		var calls atomic.Int32
		engine := heartbeatClient{extend: func(ctx context.Context, _ string, _ string, _ int) (time.Time, error) {
			calls.Add(1)
			<-ctx.Done()
			return time.Time{}, ctx.Err()
		}}
		r := New(Config{Heartbeat: true, LeaseSeconds: 5}, engine, handler.New())
		started := time.Now()
		_, _, err := r.leaseContext(t.Context(), client.Job{ID: "j", LockExpiresAt: &expiry})
		if !errors.Is(err, client.ErrLeaseLost) || time.Since(started) > 5*time.Second || calls.Load() != 1 {
			t.Fatalf("err=%v elapsed=%s calls=%d", err, time.Since(started), calls.Load())
		}
	})
}

func TestHungHeartbeatDoesNotOverlapRequestsOrPreventExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		expiry := time.Now().Add(10 * time.Second)
		release := make(chan struct{})
		var calls atomic.Int32
		engine := heartbeatClient{extend: func(context.Context, string, string, int) (time.Time, error) {
			calls.Add(1)
			<-release // Deliberately ignore the shorter request timeout.
			return time.Now().Add(time.Minute), nil
		}}
		started := time.Now()
		_, _, err := New(Config{Heartbeat: true}, engine, handler.New()).leaseContext(t.Context(), client.Job{ID: "j", LockExpiresAt: &expiry})
		close(release)
		synctest.Wait()
		if !errors.Is(err, client.ErrLeaseLost) || time.Since(started) != 10*time.Second || calls.Load() != 1 {
			t.Fatalf("err=%v elapsed=%s calls=%d", err, time.Since(started), calls.Load())
		}
	})
}

func TestHeartbeatUnsupportedAndCustomClientCompatibility(t *testing.T) {
	legacy := New(Config{Heartbeat: true}, recoveryClient{}, handler.New())
	if err := legacy.RunWithError(t.Context()); !errors.Is(err, client.ErrHeartbeatUnsupported) {
		t.Fatalf("missing capability: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := New(Config{}, recoveryClient{}, handler.New()).RunWithError(ctx); err != nil {
		t.Fatalf("legacy client rejected with heartbeat off: %v", err)
	}
	for _, cfg := range []Config{{LeaseSeconds: 4}, {Heartbeat: true, HeartbeatInterval: 11 * time.Second}, {Heartbeat: true, HeartbeatInterval: -1}} {
		if err := New(cfg, heartbeatClient{}, handler.New()).RunWithError(t.Context()); err == nil {
			t.Fatalf("accepted invalid config: %+v", cfg)
		}
	}
	expiry := time.Now().Add(time.Second)
	engine := heartbeatClient{extend: func(context.Context, string, string, int) (time.Time, error) {
		return time.Time{}, client.ErrHeartbeatUnsupported
	}}
	_, _, err := New(Config{Heartbeat: true}, engine, handler.New()).leaseContext(t.Context(), client.Job{ID: "j", LockExpiresAt: &expiry})
	if !errors.Is(err, client.ErrHeartbeatUnsupported) {
		t.Fatalf("old engine: %v", err)
	}
}

func TestShutdownBoundsDrainAndPreventsRestartWithLiveHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		engine := recoveryClient{poll: func(context.Context, client.PollJobsRequest) ([]client.Job, error) {
			return []client.Job{{JobType: "slow"}}, nil
		}}
		registry := handler.New()
		registry.Register("slow", func(context.Context, handler.JobContext) (handler.Result, error) {
			close(started)
			<-release
			return handler.Result{}, nil
		})
		r := New(Config{Parallelism: 1, PollInterval: time.Millisecond, ShutdownTimeout: time.Second}, engine, registry)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- r.RunWithError(ctx) }()
		<-started
		before := time.Now()
		cancel()
		if err := <-done; err == nil {
			t.Fatal("uncooperative handler did not produce drain timeout")
		}
		if time.Since(before) > time.Second {
			t.Fatal("unbounded shutdown")
		}
		if err := r.RunWithError(t.Context()); err == nil {
			t.Fatal("restarted while an old handler still owned capacity")
		}
		close(release)
		synctest.Wait()
	})
}

func TestRunnerCanRestartAfterHeartbeatCapabilityRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for range 20 {
			var supported atomic.Bool
			var callbacks atomic.Int32
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			expiry := time.Now().Add(30 * time.Second)
			engine := heartbeatClient{extend: func(context.Context, string, string, int) (time.Time, error) {
				if !supported.Load() {
					return time.Time{}, client.ErrHeartbeatUnsupported
				}
				return time.Now().Add(30 * time.Second), nil
			}, recoveryClient: recoveryClient{poll: func(context.Context, client.PollJobsRequest) ([]client.Job, error) {
				return []client.Job{{ID: "a", JobType: "work", LockExpiresAt: &expiry}, {ID: "b", JobType: "work", LockExpiresAt: &expiry}}, nil
			}, complete: func(context.Context, map[string]any) error { callbacks.Add(1); cancel(); return nil }}}
			registry := handler.New()
			registry.Register("work", func(context.Context, handler.JobContext) (handler.Result, error) { return handler.Result{}, nil })
			r := New(Config{Heartbeat: true, Parallelism: 2, PollInterval: time.Millisecond}, engine, registry)
			if err := r.RunWithError(ctx); !errors.Is(err, client.ErrHeartbeatUnsupported) {
				t.Fatalf("first run: %v", err)
			}
			supported.Store(true)
			if err := r.RunWithError(ctx); err != nil {
				t.Fatalf("recovered engine still reports previous failure: %v", err)
			}
			if callbacks.Load() == 0 {
				t.Fatal("restarted runner never completed a job")
			}
			cancel()
		}
	})
}
