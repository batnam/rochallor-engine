package kafka_outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
)

// relay drains dispatch_outbox rows, publishes them to Kafka, and deletes
// them in the same transaction that commits the successful publish.
// Also writes an audit_log row per published job so the durable
// dispatch trail lives in audit_log.
type relay struct {
	db           db.DB
	store        OutboxStore
	kafkaClient  *kgo.Client
	batchSize    int
	idleInterval time.Duration
	logger       *slog.Logger
}

func newRelay(database db.DB, store OutboxStore, kafkaClient *kgo.Client, batchSize int, logger *slog.Logger) *relay {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	return &relay{
		db:           database,
		store:        store,
		kafkaClient:  kafkaClient,
		batchSize:    batchSize,
		idleInterval: defaultIdleInterval * time.Millisecond,
		logger:       logger,
	}
}

// run loops until ctx is cancelled, draining batches. Errors are logged and
// retried on the next tick — a failing cycle does not propagate up.
// The caller (runtime) ensures this goroutine only runs while we are leader.
func (r *relay) run(ctx context.Context) {
	r.logger.Info("dispatch: relay started")
	defer r.logger.Info("dispatch: relay stopped")
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := r.drainOnce(ctx)
		if err != nil {
			r.logger.Warn("dispatch: relay cycle error", "err", err)
		}
		if n == 0 {
			// Empty drain — back off a bit before polling again so an idle
			// engine doesn't tight-loop against Postgres.
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.idleInterval):
			}
			// Still sample backlog so the gauge stays fresh even when idle.
			r.sampleBacklog(ctx)
		}
	}
}

// drainOnce runs one select-publish-delete-commit cycle. Returns the number
// of rows published (0 if the batch was empty).
func (r *relay) drainOnce(ctx context.Context) (int, error) {
	started := time.Now()
	defer func() {
		relayBatchLatencySecs.Observe(time.Since(started).Seconds())
	}()

	var published int
	err := r.db.RunInTx(ctx, "dispatch-relay", func(tx db.Tx) error {
		batch, err := r.store.ClaimBatch(ctx, tx, r.batchSize)
		if err != nil {
			return fmt.Errorf("claim outbox: %w", err)
		}
		if len(batch) == 0 {
			// Sample backlog opportunistically while the tx is cheap.
			if n, err := r.store.BacklogInTx(ctx, tx); err == nil {
				outboxBacklog.Set(float64(n))
			}
			return nil
		}

		// Produce the whole batch synchronously (ProduceSync waits for acks).
		records := make([]*kgo.Record, 0, len(batch))
		for _, o := range batch {
			records = append(records, &kgo.Record{
				Topic: topicFor(o.JobType),
				Key:   []byte(o.InstanceID),
				Value: o.Payload,
				Headers: []kgo.RecordHeader{
					{Key: "content-type", Value: []byte("application/x-protobuf; proto=workflow.v1.JobDispatchEvent")},
					{Key: "dedup-id", Value: []byte(o.ID)},
				},
			})
		}
		results := r.kafkaClient.ProduceSync(ctx, records...)
		if err := results.FirstErr(); err != nil {
			// Record per-error code in the producer-errors counter, then fail
			// the whole cycle. Leave rows in place so the next cycle retries.
			kafkaProducerErrors.WithLabelValues(classifyKafkaErr(err)).Inc()
			relayPublishTotal.WithLabelValues("error").Add(float64(len(batch)))
			return fmt.Errorf("kafka publish: %w", err)
		}

		// Delete the published rows inside the same tx. On commit, the publish
		// is acked AND the rows are gone — that's the atomic step.
		ids := make([]string, len(batch))
		for i, o := range batch {
			ids[i] = o.ID
		}
		if err := r.store.DeleteByIDs(ctx, tx, ids); err != nil {
			return fmt.Errorf("delete rows: %w", err)
		}
		if err := r.store.WriteAuditEntries(ctx, tx, batch); err != nil {
			return fmt.Errorf("audit log: %w", err)
		}
		published = len(batch)
		return nil
	})
	if err != nil {
		return 0, err
	}
	if published > 0 {
		relayPublishTotal.WithLabelValues("success").Add(float64(published))
		r.logger.Debug("dispatch: relay batch published", "count", published)
	}
	return published, nil
}

// sampleBacklog runs OUTSIDE any transaction to keep the backlog gauge fresh
// even during long idle windows.
func (r *relay) sampleBacklog(ctx context.Context) {
	if n, err := r.store.Backlog(ctx); err == nil {
		outboxBacklog.Set(float64(n))
	}
}

// classifyKafkaErr maps a franz-go error into a stable label value for the
// producer-errors counter. Returns "client" for values outside the library's
// kerr table.
func classifyKafkaErr(err error) string {
	if err == nil {
		return "ok"
	}
	// The franz-go kerr package wraps broker-side errors; a context error or
	// TCP error falls through to the coarse-grained "client" label.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "context"
	}
	return "client"
}
