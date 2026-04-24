// Package runner implements the poll/lock/dispatch loop for the Go SDK.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/metrics"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
)

const (
	defaultParallelism  = 64
	defaultPollInterval = 500 * time.Millisecond
)

// Config holds runner configuration.
type Config struct {
	WorkerID     string
	Parallelism  int           // goroutines (default 64)
	PollInterval time.Duration // between poll rounds (default 500ms)
}

// Runner polls the Engine and dispatches jobs to registered handlers.
type Runner struct {
	cfg      Config
	engine   client.EngineClient
	registry *handler.Registry
}

// New creates a Runner. cfg.WorkerID must not be empty.
func New(cfg Config, engine client.EngineClient, registry *handler.Registry) *Runner {
	if cfg.Parallelism <= 0 {
		cfg.Parallelism = defaultParallelism
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	return &Runner{cfg: cfg, engine: engine, registry: registry}
}

// Run starts the poll loop and blocks until ctx is cancelled.
// All in-flight handlers are drained before Run returns.
func (r *Runner) Run(ctx context.Context) {
	sem := make(chan struct{}, r.cfg.Parallelism)
	var wg sync.WaitGroup

	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()

	jobTypes := r.registry.JobTypes()
	slog.Info("runner: starting", "worker_id", r.cfg.WorkerID, "job_types", jobTypes, "parallelism", r.cfg.Parallelism)

	for {
		select {
		case <-ctx.Done():
			slog.Info("runner: received stop signal, draining in-flight jobs")
			wg.Wait()
			slog.Info("runner: stopped")
			return
		case <-ticker.C:
			r.pollAndDispatch(ctx, sem, &wg, jobTypes)
		}
	}
}

func (r *Runner) pollAndDispatch(ctx context.Context, sem chan struct{}, wg *sync.WaitGroup, jobTypes []string) {
	start := time.Now()
	jobs, err := r.engine.PollJobs(ctx, client.PollJobsRequest{
		WorkerID: r.cfg.WorkerID,
		JobTypes: jobTypes,
		MaxJobs:  r.cfg.Parallelism,
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
	h, err := r.registry.Get(j.JobType)
	if err != nil {
		slog.Error("runner: no handler", "job_id", j.ID, "job_type", j.JobType)
		_ = r.engine.FailJob(ctx, j.ID, r.cfg.WorkerID, "no handler registered for "+j.JobType, false)
		return
	}

	var vars map[string]any
	if len(j.Variables) > 0 {
		_ = json.Unmarshal(j.Variables, &vars)
	}

	jctx := handler.JobContext{
		JobID:            j.ID,
		InstanceID:       j.InstanceID,
		StepID:           j.StepExecutionID,
		JobType:          j.JobType,
		RetriesRemaining: j.RetriesRemaining,
		Variables:        vars,
	}

	start := time.Now()
	slog.Info("runner: dispatching job", "job_id", j.ID, "job_type", j.JobType)
	result, err := safeCall(ctx, h, jctx)
	metrics.HandlerLatency.WithLabelValues(j.JobType).Observe(time.Since(start).Seconds())

	if err == nil {
		slog.Info("runner: job completed", "job_id", j.ID)
		metrics.JobsCompletedTotal.WithLabelValues(j.JobType, "success").Inc()
		if completeErr := r.engine.CompleteJob(ctx, j.ID, r.cfg.WorkerID, result.VariablesToSet); completeErr != nil {
			slog.Error("runner: CompleteJob failed", "job_id", j.ID, "err", completeErr)
		}
		return
	}

	// Handler failed
	isNonRetryable := retry.IsNonRetryable(err)
	if isNonRetryable {
		slog.Warn("runner: job failed (non-retryable)", "job_id", j.ID, "err", err)
	} else {
		slog.Warn("runner: job failed (retryable)", "job_id", j.ID, "err", err)
	}
	metrics.JobsCompletedTotal.WithLabelValues(j.JobType, "failure").Inc()
	if !isNonRetryable {
		metrics.RetriesTotal.WithLabelValues(j.JobType).Inc()
	}

	if failErr := r.engine.FailJob(ctx, j.ID, r.cfg.WorkerID, err.Error(), !isNonRetryable); failErr != nil {
		slog.Error("runner: FailJob failed", "job_id", j.ID, "err", failErr)
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
