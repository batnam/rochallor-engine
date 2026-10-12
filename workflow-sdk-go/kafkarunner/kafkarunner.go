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
//     side retries and relay-crash-induced republishes.
//   - Invokes the user-registered Handler from handler.Registry.
//   - Reports completion through the existing engine CompleteJob / FailJob
//     RPCs — never writes directly to the DB and never publishes
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
	"github.com/batnam/rochallor-engine/workflow-sdk-go/internal/callback"
	workflowv1 "github.com/batnam/rochallor-engine/workflow-sdk-go/internal/gen/workflow/v1"
	"github.com/batnam/rochallor-engine/workflow-sdk-go/metrics"
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
	var owners []*ownership
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
		)
		opts = append(opts, r.cfg.KgoOpts...)
		owner := newOwnership(ctx)
		// Ownership and commit policy cannot be overridden by tuning options.
		opts = append(opts, kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(),
			kgo.OnPartitionsAssigned(owner.assigned),
			kgo.OnPartitionsRevoked(owner.revoked), kgo.OnPartitionsLost(owner.revoked))
		cl, err := kgo.NewClient(opts...)
		if err != nil {
			return fmt.Errorf("kgo client for %s: %w", topic, err)
		}
		clients = append(clients, cl)
		owners = append(owners, owner)
	}
	// Start only after all clients have been constructed successfully.
	for i, cl := range clients {
		wg.Add(1)
		go func(c *kgo.Client, owner *ownership) {
			defer wg.Done()
			if err := r.consumeLoop(ctx, c, owner); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- err
				cancel()
			}
		}(cl, owners[i])
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

// consumeLoop processes one record at a time. A pending record prevents any
// later record from its partition from being committed, including on shutdown.
func (r *Runner) consumeLoop(ctx context.Context, c *kgo.Client, owners *ownership) error {
	defer c.AllowRebalance()
	for ctx.Err() == nil {
		fetches := c.PollRecords(ctx, 1)
		records := fetches.Records()
		if len(records) == 0 {
			c.AllowRebalance()
			for _, e := range fetches.Errors() {
				slog.Warn("kafkarunner: fetch error", "topic", e.Topic, "err", e.Err)
			}
			continue
		}
		rec := records[0]
		// Capture the assignment before allowing rebalance callbacks. They
		// cancel this context on revocation while the handler/retry is running.
		owner := owners.get(rec)
		c.AllowRebalance()
		if owner == nil {
			continue
		}
		if err := r.dispatchRecord(owner.ctx, rec); err != nil {
			if owner.ctx.Err() != nil && ctx.Err() == nil {
				continue
			}
			return fmt.Errorf("record %s/%d/%d remains unacknowledged: %w", rec.Topic, rec.Partition, rec.Offset, err)
		}
		err := owners.commit(rec, owner, func(ownerCtx context.Context) error {
			commitCtx, cancel := context.WithTimeout(ownerCtx, callback.AttemptTimeout)
			defer cancel()
			return c.CommitRecords(commitCtx, rec)
		})
		if err != nil {
			if owner.ctx.Err() != nil && ctx.Err() == nil {
				continue
			}
			return fmt.Errorf("commit %s/%d/%d: %w", rec.Topic, rec.Partition, rec.Offset, err)
		}
	}
	return ctx.Err()
}

func (r *Runner) dispatchRecord(ctx context.Context, rec *kgo.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	log := slog.With("topic", rec.Topic, "partition", rec.Partition, "offset", rec.Offset)
	var event workflowv1.JobDispatchEvent
	if err := proto.Unmarshal(rec.Value, &event); err != nil {
		metrics.KafkaRecordErrors.Inc()
		log.Error("kafkarunner: invalid record; stopping without commit", "err", err)
		return fmt.Errorf("decode dispatch event: %w", err)
	}
	if event.JobId == "" {
		metrics.KafkaRecordErrors.Inc()
		log.Error("kafkarunner: dispatch event has no job ID; stopping without commit")
		return errors.New("dispatch event has no job ID; stopping without commit")
	}
	log = log.With("job_id", event.JobId, "dedup_id", event.DedupId)
	if event.DedupId != "" && r.seenRecently(event.DedupId) {
		log.Debug("kafkarunner: skipping confirmed duplicate")
		return nil
	}

	kind := "complete"
	var variables map[string]any
	var handlerErr error
	h, handlerErr := r.registry.Get(event.JobType)
	if handlerErr == nil && len(event.JobPayload) > 0 {
		if err := json.Unmarshal(event.JobPayload, &variables); err != nil {
			handlerErr = &retry.NonRetryable{Cause: fmt.Errorf("invalid job payload: %w", err)}
		}
	}
	var result handler.Result
	if handlerErr == nil {
		log.Info("kafkarunner: dispatching job", "job_type", event.JobType)
		result, handlerErr = safeCall(ctx, h, handler.JobContext{
			JobID: event.JobId, InstanceID: event.InstanceId, StepID: event.StepExecutionId,
			JobType: event.JobType, RetriesRemaining: int(event.RetriesRemaining), Variables: variables,
		})
	} else if h == nil {
		handlerErr = &retry.NonRetryable{Cause: handlerErr}
	}

	// Freeze the JSON result before any callback attempt. A retained handler
	// map must not change a later retry's payload.
	var encoded []byte
	if handlerErr == nil {
		var err error
		encoded, err = json.Marshal(result.VariablesToSet)
		if err != nil {
			handlerErr = &retry.NonRetryable{Cause: fmt.Errorf("invalid handler result: %w", err)}
		}
	}
	var send func(context.Context) error
	if handlerErr != nil {
		kind = "fail"
		reason, retryable := handlerErr.Error(), !retry.IsNonRetryable(handlerErr)
		send = func(attempt context.Context) error {
			return r.engine.FailJob(attempt, event.JobId, r.cfg.WorkerID, reason, retryable)
		}
	} else {
		send = func(attempt context.Context) error {
			var output map[string]any
			_ = json.Unmarshal(encoded, &output)
			return r.engine.CompleteJob(attempt, event.JobId, r.cfg.WorkerID, output)
		}
	}
	if err := callback.Retry(ctx, "kafka", kind, log, send); err != nil {
		return err
	}
	if event.DedupId != "" {
		r.dedupMu.Lock()
		r.dedup[event.DedupId] = time.Now()
		r.dedupMu.Unlock()
	}
	log.Info("kafkarunner: callback confirmed", "callback", kind)
	return nil
}

// seenRecently checks only confirmed outcomes. Pending work is held by the
// dispatch call and never expires with the deduplication cache.
func (r *Runner) seenRecently(dedupID string) bool {
	r.dedupMu.Lock()
	defer r.dedupMu.Unlock()
	seen, ok := r.dedup[dedupID]
	return ok && time.Since(seen) < r.cfg.DedupWindow
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
