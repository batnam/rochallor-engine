package kafka_outbox

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
)

// Runtime is the per-process lifecycle for event-driven dispatch. It owns the
// Kafka client, the leader-election loop, and the relay goroutine. At most
// one replica per Postgres holds the advisory lock and publishes at any time
// other replicas keep polling for leadership but stay idle
// on the publish path.
type Runtime struct {
	cfg    Config
	logger *slog.Logger

	kafkaClient *kgo.Client
	leader      *leaderElection
	relay       *relay

	// leaderCtx / leaderCancel scope the relay goroutine's lifetime to the
	// current leadership tenure. When leadership is lost, leaderCancel()
	// signals the relay to stop; a new tenure gets a fresh ctx.
	mu           sync.Mutex
	leaderCtx    context.Context
	leaderCancel context.CancelFunc
	relayWG      sync.WaitGroup

	// rootCtx / rootCancel scope the leader-election goroutine's lifetime
	// to the Runtime (Start to Stop).
	rootCtx    context.Context
	rootCancel context.CancelFunc
	electionWG sync.WaitGroup

	started bool
}

// New constructs a Runtime from Config. It does NOT open any network
// connection; that happens in Start so constructor failures are purely
// config / nil-check based.
func New(cfg Config) *Runtime {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Runtime{cfg: cfg, logger: logger}
}

// Start performs the R-008 validation sequence and launches the leader-
// election + relay goroutines. Returns an error if any dependency is missing
// or unreachable — no silent fallback (FR-008).
//
// Validation steps (in order):
//  1. Config completeness (pool, seed brokers present).
//  2. Migration 0009 applied (table dispatch_outbox exists).
//  3. Open Kafka client + Metadata request against every topic (R-002).
//     Fail fast if any topic is missing or if the broker is unreachable.
//  4. Start the leader-election loop.
//
// Idempotent: calling Start twice returns an error rather than double-booting.
func (r *Runtime) Start(ctx context.Context) error {
	if r.cfg.Pool == nil {
		return fmt.Errorf("kafka_outbox: Config.Pool is required")
	}
	if r.cfg.SeedBrokers == "" {
		return fmt.Errorf("kafka_outbox: Config.SeedBrokers is required")
	}
	if r.started {
		return fmt.Errorf("kafka_outbox: runtime already started")
	}

	// 2. Migration 0009 check
	if err := r.checkMigration(ctx); err != nil {
		return err
	}

	// Register Prometheus metrics exactly once per process (FR-010).
	ensureMetricsRegistered()

	client, err := newKafkaClient(r.cfg)
	if err != nil {
		return fmt.Errorf("kafka_outbox: build kafka client: %w", err)
	}

	// 3. Topic validation (Metadata request)
	if err := r.validateTopics(ctx, client); err != nil {
		client.Close()
		return err
	}

	r.kafkaClient = client
	r.relay = newRelay(r.cfg.Pool, client, r.cfg.BatchSize, r.logger)

	// rootCtx scopes the election loop's lifetime.
	r.rootCtx, r.rootCancel = context.WithCancel(context.Background())

	r.leader = newLeaderElection(r.cfg.Pool, r.logger,
		func(_ context.Context) { r.onLeader() },
		func(_ context.Context) { r.onLeaderLost() },
	)
	r.electionWG.Add(1)
	go func() {
		defer r.electionWG.Done()
		r.leader.run(r.rootCtx)
	}()
	r.started = true
	r.logger.Info("kafka_outbox: runtime started",
		"transport", r.cfg.Transport,
		"seed_brokers", r.cfg.SeedBrokers,
	)
	return nil
}

// validateTopics issues a Metadata request against every configured topic.
// topics are not auto-created.
func (r *Runtime) validateTopics(ctx context.Context, client *kgo.Client) error {
	if len(r.cfg.JobTypes) == 0 {
		return nil
	}
	r.logger.Info("kafka_outbox: validating topics", "count", len(r.cfg.JobTypes))
	admin := kadm.NewClient(client)
	topics := make([]string, 0, len(r.cfg.JobTypes))
	for _, jt := range r.cfg.JobTypes {
		topics = append(topics, topicFor(jt))
	}

	// ListTopics with specific names returns only those. If a topic is
	// missing, it is either absent from the map or present with an error.
	metadata, err := admin.ListTopics(ctx, topics...)
	if err != nil {
		return fmt.Errorf("kafka_outbox: metadata request failed: %w", err)
	}
	for _, t := range topics {
		m, ok := metadata[t]
		if !ok {
			return fmt.Errorf("kafka_outbox: topic %q is missing (auto-creation is disabled per R-002)", t)
		}
		if m.Err != nil {
			return fmt.Errorf("kafka_outbox: topic %q metadata error: %w", t, m.Err)
		}
	}
	return nil
}

func (r *Runtime) checkMigration(ctx context.Context) error {
	var exists bool
	err := r.cfg.Pool.QueryRow(ctx, `
                SELECT EXISTS (
                        SELECT FROM information_schema.tables 
                        WHERE  table_schema = 'public'
                        AND    table_name   = 'dispatch_outbox'
                )`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("kafka_outbox: migration check failed: %w", err)
	}
	if !exists {
		return fmt.Errorf("kafka_outbox: migration 0009 not applied (table dispatch_outbox missing)")
	}
	return nil
}

// onLeader launches the relay goroutine scoped to the current leadership.
func (r *Runtime) onLeader() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.leaderCtx != nil {
		return // already running for this tenure
	}
	r.leaderCtx, r.leaderCancel = context.WithCancel(r.rootCtx)
	r.relayWG.Add(1)
	go func(ctx context.Context) {
		defer r.relayWG.Done()
		r.relay.run(ctx)
	}(r.leaderCtx)
}

// onLeaderLost signals the relay goroutine to stop and resets state so the
// next acquire reopens a fresh tenure.
func (r *Runtime) onLeaderLost() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.leaderCancel != nil {
		r.leaderCancel()
	}
	r.leaderCtx = nil
	r.leaderCancel = nil
	// Wait for the relay to drain the in-flight batch. Holding r.mu while
	// we wait is intentional — it serializes leadership transitions.
	r.mu.Unlock()
	r.relayWG.Wait()
	r.mu.Lock()
}

// Stop gracefully shuts down the relay (if running), releases the advisory
// lock (if held), and closes the Kafka client. Safe to call multiple times.
func (r *Runtime) Stop(ctx context.Context) error {
	if !r.started {
		return nil
	}
	r.started = false

	// Signal the election goroutine to exit.
	if r.rootCancel != nil {
		r.rootCancel()
	}
	r.electionWG.Wait()

	// Release the lock + close the dedicated connection.
	if r.leader != nil {
		r.leader.Stop(ctx)
	}

	// Cancel the relay's context (if still running) and wait for it to
	// finish its current batch.
	r.mu.Lock()
	if r.leaderCancel != nil {
		r.leaderCancel()
	}
	r.mu.Unlock()
	r.relayWG.Wait()

	if r.kafkaClient != nil {
		r.kafkaClient.Close()
		r.kafkaClient = nil
	}
	r.logger.Info("kafka_outbox: runtime stopped")
	return nil
}

// Dispatcher returns the hot-path Dispatcher for this runtime.
// The returned value is stateless and stable for the lifetime of the Runtime.
func (r *Runtime) Dispatcher() dispatch.Dispatcher {
	return Dispatcher{}
}
