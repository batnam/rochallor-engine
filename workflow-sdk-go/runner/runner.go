// Package runner implements the poll/lock/dispatch loop for the Go SDK.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/internal/callback"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/metrics"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
)

const (
	defaultParallelism  = 64
	defaultPollInterval = 500 * time.Millisecond
)

// Config holds runner configuration.
type Config struct {
	WorkerID          string
	Parallelism       int           // goroutines (default 64)
	PollInterval      time.Duration // between poll rounds (default 500ms)
	LeaseSeconds      int           // zero keeps the engine default; otherwise 5–3600
	Heartbeat         bool          // opt-in; requires client.LeaseExtender and a capable engine
	HeartbeatInterval time.Duration // default lease/3; must not exceed lease/3
	ShutdownTimeout   time.Duration // bounded drain after cancellation (default 5s)
}

// Runner polls the Engine and dispatches jobs to registered handlers.
type Runner struct {
	cfg        Config
	engine     client.EngineClient
	registry   *handler.Registry
	stopErrors chan error
	runMu      sync.Mutex
	running    bool
}

// New creates a Runner. cfg.WorkerID must not be empty.
func New(cfg Config, engine client.EngineClient, registry *handler.Registry) *Runner {
	if cfg.Parallelism <= 0 {
		cfg.Parallelism = defaultParallelism
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	return &Runner{cfg: cfg, engine: engine, registry: registry, stopErrors: make(chan error, 1)}
}

// Run preserves the original API; RunWithError also returns configuration,
// unsupported-heartbeat and bounded-drain failures to the caller.
func (r *Runner) Run(ctx context.Context) {
	if err := r.RunWithError(ctx); err != nil {
		slog.Error("runner: stopped with error", "err", err)
	}
}

func (r *Runner) RunWithError(parent context.Context) (runErr error) {
	if err := r.validateHeartbeat(); err != nil {
		return err
	}
	r.runMu.Lock()
	if r.running {
		r.runMu.Unlock()
		return fmt.Errorf("runner is already running or still draining")
	}
	r.running = true
	// All prior handlers have drained. Concurrent failures from that run must
	// not stop a new run after the engine's capability/configuration is fixed.
	r.stopErrors = make(chan error, 1)
	r.runMu.Unlock()
	ctx, stop := context.WithCancel(parent)
	sem := make(chan struct{}, r.cfg.Parallelism)
	var wg sync.WaitGroup
	defer func() {
		stop()
		done := make(chan struct{})
		go func() { wg.Wait(); r.runMu.Lock(); r.running = false; r.runMu.Unlock(); close(done) }()
		timer := time.NewTimer(r.cfg.ShutdownTimeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			if runErr == nil {
				runErr = fmt.Errorf("shutdown drain timed out; uncooperative handlers may still be running")
			}
		}
	}()
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	jobTypes := r.registry.JobTypes()
	slog.Info("runner: starting", "worker_id", r.cfg.WorkerID, "job_types", jobTypes, "parallelism", r.cfg.Parallelism, "heartbeat", r.cfg.Heartbeat)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-r.stopErrors:
			return err
		case <-ticker.C:
			r.pollAndDispatch(ctx, sem, &wg, jobTypes)
		}
	}
}

func (r *Runner) pollAndDispatch(ctx context.Context, sem chan struct{}, wg *sync.WaitGroup, jobTypes []string) {
	available := cap(sem) - len(sem)
	if available == 0 {
		return
	}
	start := time.Now()
	pollCtx, cancel := context.WithTimeout(ctx, callback.AttemptTimeout)
	defer cancel()
	jobs, err := r.engine.PollJobs(pollCtx, client.PollJobsRequest{
		WorkerID:     r.cfg.WorkerID,
		JobTypes:     jobTypes,
		MaxJobs:      available,
		LeaseSeconds: r.cfg.LeaseSeconds,
	})
	metrics.PollLatency.Observe(time.Since(start).Seconds())

	if err != nil {
		slog.Error("runner: poll error", "err", err)
		return
	}
	if len(jobs) == 0 {
		metrics.LockConflicts.Add(1)
		return
	}

	for _, j := range jobs {
		j := j // capture
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r.dispatch(ctx, j)
		}()
	}
}

func (r *Runner) dispatch(ctx context.Context, j client.Job) {
	ctx, stopLease, leaseErr := r.leaseContext(ctx, j)
	if leaseErr != nil {
		slog.Error("runner: cannot start leased job", "job_id", j.ID, "err", leaseErr)
		if !errors.Is(leaseErr, client.ErrLeaseLost) && !errors.Is(leaseErr, context.Canceled) && !errors.Is(leaseErr, context.DeadlineExceeded) {
			select {
			case r.stopErrors <- leaseErr:
			default:
			}
		}
		return
	}
	defer stopLease()
	if ctx.Err() != nil {
		return
	}
	log := slog.With("job_id", j.ID, "job_type", j.JobType)
	var result handler.Result
	var vars map[string]any
	h, err := r.registry.Get(j.JobType)
	if err != nil {
		err = &retry.NonRetryable{Cause: fmt.Errorf("no handler registered for %s", j.JobType)}
	} else if len(j.Variables) > 0 {
		if decodeErr := json.Unmarshal(j.Variables, &vars); decodeErr != nil {
			err = &retry.NonRetryable{Cause: fmt.Errorf("invalid job payload: %w", decodeErr)}
		}
	}
	if err == nil {
		start := time.Now()
		result, err = safeCall(ctx, h, handler.JobContext{JobID: j.ID, InstanceID: j.InstanceID, StepID: j.StepExecutionID, JobType: j.JobType, RetriesRemaining: j.RetriesRemaining, Variables: vars})
		metrics.HandlerLatency.WithLabelValues(j.JobType).Observe(time.Since(start).Seconds())
	}
	if ctx.Err() != nil {
		return
	}
	var output []byte
	if err == nil {
		output, err = json.Marshal(result.VariablesToSet)
	}
	kind := "complete"
	var send func(context.Context) error
	if err == nil {
		metrics.JobsCompletedTotal.WithLabelValues(j.JobType, "success").Inc()
		send = func(ctx context.Context) error {
			var copy map[string]any
			if err := json.Unmarshal(output, &copy); err != nil {
				return err
			}
			return r.engine.CompleteJob(ctx, j.ID, r.cfg.WorkerID, copy)
		}
	} else {
		kind = "fail"
		reason, retryable := err.Error(), !retry.IsNonRetryable(err)
		metrics.JobsCompletedTotal.WithLabelValues(j.JobType, "failure").Inc()
		if retryable {
			metrics.RetriesTotal.WithLabelValues(j.JobType).Inc()
		}
		send = func(ctx context.Context) error { return r.engine.FailJob(ctx, j.ID, r.cfg.WorkerID, reason, retryable) }
	}
	deliver := callback.Retry
	if j.LockExpiresAt == nil {
		deliver = callback.Once
	}
	if err := deliver(ctx, "polling", kind, log, send); err != nil {
		log.Warn("runner: callback unacknowledged", "callback", kind, "err", err)
	}
}

// safeCall executes h, recovering from panics.
func safeCall(ctx context.Context, h handler.Handler, jctx handler.JobContext) (res handler.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &retry.NonRetryable{Cause: fmt.Errorf("handler panic: %v", r)}
		}
	}()
	return h(ctx, jctx)
}
