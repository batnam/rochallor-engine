// Package kafkarunner implements a Kafka-consumer-group runner for the
// engine's event-driven dispatch mode. It is a sibling of the existing
// runner.Runner (the polling variant); existing polling SDK users are
// untouched.
//
// Behaviour per specs/006-kafka-outbox-dispatch/contracts/dispatch-event.proto.md:
//   - Consumes from workflow.jobs.<jobType> topics using franz-go's
//     cooperative-sticky consumer group (workflow.workers.<jobType>).
//   - Decodes JobDispatchEvent protobuf records.
//   - Deduplicates on DedupId over a 10-minute window to absorb producer-
//     side retries and relay-crash-induced republishes (FR-007).
//   - Invokes the user-registered Handler from handler.Registry.
//   - Reports completion through the existing engine CompleteJob / FailJob
//     RPCs (FR-006) — never writes directly to the DB and never publishes
//     back to the broker.
package kafkarunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
	workflowv1 "github.com/batnam/rochallor-engine/workflow-sdk-go/internal/gen/workflow/v1"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
)

// Config holds runner configuration. JobTypes is the set of engine job types
// this worker fleet handles; the runner subscribes to workflow.jobs.<jobType>
// for each and joins consumer group workflow.workers.<jobType>.
type Config struct {
	// WorkerID is the stable worker identifier reported to the engine on
	// complete/fail RPCs. Must be non-empty.
	WorkerID string

	// SeedBrokers is a comma-separated list of host:port broker addresses.
	SeedBrokers string

	// JobTypes is the list of job_type strings this runner handles.
	JobTypes []string

	// DedupWindow controls how long observed DedupIds are remembered.
	// Default: 10 minutes.
	DedupWindow time.Duration

	// KgoOpts lets callers pass extra franz-go options (SASL/TLS, logger, etc).
	KgoOpts []kgo.Opt
}

// Runner is the Kafka-consumer equivalent of runner.Runner. Construct one
// with New and call Run in your worker's goroutine; Stop is signalled by
// ctx cancellation.
type Runner struct {
	cfg      Config
	engine   client.EngineClient
	registry *handler.Registry

	// dedup tracks recently seen DedupIds with a TTL sweep. Access protected
	// by its own mutex because the sweeper runs concurrently with consume.
	dedupMu sync.Mutex
	dedup   map[string]time.Time
}

// New validates cfg and constructs a Runner. Returns an error if cfg is
// missing required fields (WorkerID, SeedBrokers, at least one JobType).
func New(cfg Config, engine client.EngineClient, registry *handler.Registry) (*Runner, error) {
	if cfg.WorkerID == "" {
		return nil, errors.New("kafkarunner: Config.WorkerID is required")
	}
	if cfg.SeedBrokers == "" {
		return nil, errors.New("kafkarunner: Config.SeedBrokers is required")
	}
	if len(cfg.JobTypes) == 0 {
		return nil, errors.New("kafkarunner: at least one JobType is required")
	}
	if cfg.DedupWindow <= 0 {
		cfg.DedupWindow = 10 * time.Minute
	}
	return &Runner{
		cfg:      cfg,
		engine:   engine,
		registry: registry,
		dedup:    make(map[string]time.Time),
	}, nil
}

// Run joins the consumer groups and dispatches records to handlers until
// ctx is cancelled. In-flight handlers are awaited before Run returns.
func (r *Runner) Run(ctx context.Context) error {
	topics := make([]string, 0, len(r.cfg.JobTypes))
	for _, jt := range r.cfg.JobTypes {
		topics = append(topics, "workflow.jobs."+jt)
	}

	slog.Info("kafkarunner: starting", "worker_id", r.cfg.WorkerID, "job_types", r.cfg.JobTypes)

	// One consumer-group per job type matches contracts/kafka-topics.md §3.
	// franz-go joins multiple groups via a single client only when every
	// topic maps to the same group; we want per-type groups so canary
	// fleets don't steal each other's work. Use one client per job type.
	var clients []*kgo.Client
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()

	errCh := make(chan error, len(r.cfg.JobTypes))
	var wg sync.WaitGroup

	for _, jt := range r.cfg.JobTypes {
		topic := "workflow.jobs." + jt
		group := "workflow.workers." + jt
		opts := append([]kgo.Opt{},
			kgo.SeedBrokers(r.cfg.SeedBrokers),
			kgo.ConsumerGroup(group),
			kgo.ConsumeTopics(topic),
			// Standardized cooperative partition assignment strategy
			kgo.Balancers(kgo.CooperativeStickyBalancer()),
			kgo.DisableAutoCommit(),
		)
		opts = append(opts, r.cfg.KgoOpts...)
		cl, err := kgo.NewClient(opts...)
		if err != nil {
			return fmt.Errorf("kgo client for %s: %w", topic, err)
		}
		clients = append(clients, cl)
		wg.Add(1)
		go func(c *kgo.Client) {
			defer wg.Done()
			if err := r.consumeLoop(ctx, c); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
			}
		}(cl)
	}

	// Dedup TTL sweeper.
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.dedupSweep(ctx)
	}()

	wg.Wait()
	slog.Info("kafkarunner: stopped")
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// consumeLoop polls records, dispatches them, and commits offsets on success.
func (r *Runner) consumeLoop(ctx context.Context, c *kgo.Client) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fetches := c.PollFetches(ctx)
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				slog.Warn("kafkarunner: fetch error", "topic", e.Topic, "err", e.Err)
			}
		}
		it := fetches.RecordIter()
		for !it.Done() {
			rec := it.Next()
			r.dispatchRecord(ctx, rec)
		}
		// Commit offsets for the records we've processed. On a crash before
		// commit, the broker redelivers — dedup catches the duplicate.
		if err := c.CommitUncommittedOffsets(ctx); err != nil {
			slog.Warn("kafkarunner: commit offsets", "err", err)
		}
	}
}

func (r *Runner) dispatchRecord(ctx context.Context, rec *kgo.Record) {
	var event workflowv1.JobDispatchEvent
	if err := proto.Unmarshal(rec.Value, &event); err != nil {
		slog.Error("kafkarunner: proto unmarshal", "topic", rec.Topic, "err", err)
		return
	}
	if event.DedupId != "" && r.seenRecently(event.DedupId) {
		slog.Debug("kafkarunner: skipping duplicate job", "job_id", event.JobId, "dedup_id", event.DedupId)
		return
	}

	h, err := r.registry.Get(event.JobType)
	if err != nil {
		slog.Error("kafkarunner: no handler", "job_id", event.JobId, "job_type", event.JobType)
		_ = r.engine.FailJob(ctx, event.JobId, r.cfg.WorkerID, "no handler registered for "+event.JobType, false)
		return
	}

	var vars map[string]any
	if len(event.JobPayload) > 0 {
		_ = json.Unmarshal(event.JobPayload, &vars)
	}
	jctx := handler.JobContext{
		JobID:            event.JobId,
		InstanceID:       event.InstanceId,
		StepID:           event.StepExecutionId,
		JobType:          event.JobType,
		RetriesRemaining: int(event.RetriesRemaining),
		Variables:        vars,
	}

	slog.Info("kafkarunner: dispatching job", "job_id", event.JobId, "job_type", event.JobType)
	result, err := safeCall(ctx, h, jctx)
	if err == nil {
		slog.Info("kafkarunner: job completed", "job_id", event.JobId)
		if completeErr := r.engine.CompleteJob(ctx, event.JobId, r.cfg.WorkerID, result.VariablesToSet); completeErr != nil {
			slog.Error("kafkarunner: CompleteJob failed", "job_id", event.JobId, "err", completeErr)
		}
		return
	}
	isNonRetryable := retry.IsNonRetryable(err)
	if isNonRetryable {
		slog.Warn("kafkarunner: job failed (non-retryable)", "job_id", event.JobId, "err", err)
	} else {
		slog.Warn("kafkarunner: job failed (retryable)", "job_id", event.JobId, "err", err)
	}
	if failErr := r.engine.FailJob(ctx, event.JobId, r.cfg.WorkerID, err.Error(), !isNonRetryable); failErr != nil {
		slog.Error("kafkarunner: FailJob failed", "job_id", event.JobId, "err", failErr)
	}
}

// seenRecently returns true if dedupID was seen within the DedupWindow and,
// as a side effect, records this observation. Callers that never retry on
// their own rely on this window absorbing broker-side redeliveries.
func (r *Runner) seenRecently(dedupID string) bool {
	r.dedupMu.Lock()
	defer r.dedupMu.Unlock()
	now := time.Now()
	if seen, ok := r.dedup[dedupID]; ok {
		if now.Sub(seen) < r.cfg.DedupWindow {
			return true
		}
	}
	r.dedup[dedupID] = now
	return false
}

// dedupSweep runs every DedupWindow/4 and evicts entries older than the window.
func (r *Runner) dedupSweep(ctx context.Context) {
	interval := r.cfg.DedupWindow / 4
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-r.cfg.DedupWindow)
			r.dedupMu.Lock()
			for k, v := range r.dedup {
				if v.Before(cutoff) {
					delete(r.dedup, k)
				}
			}
			r.dedupMu.Unlock()
		}
	}
}

// safeCall executes h, recovering from panics to preserve the runner's loop.
func safeCall(ctx context.Context, h handler.Handler, jctx handler.JobContext) (res handler.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &retry.NonRetryable{Cause: fmt.Errorf("handler panic: %v", r)}
		}
	}()
	return h(ctx, jctx)
}
