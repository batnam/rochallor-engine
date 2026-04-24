package kafka_outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
)

// outboxRow is the narrow read-model for dispatch_outbox rows the relay
// pulls per drain cycle.
type outboxRow struct {
	id         string
	jobID      string
	instanceID string
	jobType    string
	payload    []byte
}

// relay drains dispatch_outbox rows, publishes them to Kafka, and deletes
// them in the same transaction that commits the successful publish (INV-2,
// FR-017). Also writes an audit_log row per published job so the durable
// dispatch trail lives in audit_log (FR-015, FR-017).
type relay struct {
	pool         *pgxpool.Pool
	kafkaClient  *kgo.Client
	batchSize    int
	idleInterval time.Duration
	logger       *slog.Logger
}

func newRelay(pool *pgxpool.Pool, kafkaClient *kgo.Client, batchSize int, logger *slog.Logger) *relay {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	return &relay{
		pool:         pool,
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

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("relay: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	rows, err := tx.Query(ctx,
		`SELECT id, job_id, instance_id, job_type, payload
		 FROM dispatch_outbox
		 ORDER BY created_at
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`,
		r.batchSize,
	)
	if err != nil {
		return 0, fmt.Errorf("relay: select outbox: %w", err)
	}

	var batch []outboxRow
	for rows.Next() {
		var o outboxRow
		if err := rows.Scan(&o.id, &o.jobID, &o.instanceID, &o.jobType, &o.payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("relay: scan row: %w", err)
		}
		batch = append(batch, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("relay: iterate rows: %w", err)
	}
	if len(batch) == 0 {
		// Sample backlog opportunistically while the tx is cheap.
		r.sampleBacklogInTx(ctx, tx)
		return 0, tx.Commit(ctx)
	}

	// Produce the whole batch synchronously (ProduceSync waits for acks).
	records := make([]*kgo.Record, 0, len(batch))
	for _, o := range batch {
		records = append(records, &kgo.Record{
			Topic: topicFor(o.jobType),
			Key:   []byte(o.instanceID),
			Value: o.payload,
			Headers: []kgo.RecordHeader{
				{Key: "content-type", Value: []byte("application/x-protobuf; proto=workflow.v1.JobDispatchEvent")},
				{Key: "dedup-id", Value: []byte(o.id)},
			},
		})
	}
	results := r.kafkaClient.ProduceSync(ctx, records...)
	if err := results.FirstErr(); err != nil {
		// Record per-error code in the producer-errors counter, then fail
		// the whole cycle. INV-2: leave rows in place so the next cycle
		// retries them.
		kafkaProducerErrors.WithLabelValues(classifyKafkaErr(err)).Inc()
		relayPublishTotal.WithLabelValues("error").Add(float64(len(batch)))
		return 0, fmt.Errorf("relay: kafka publish: %w", err)
	}

	// Delete the published rows inside the same tx. On commit, the publish
	// is acked AND the rows are gone — that's the INV-2 atomic step.
	ids := make([]string, len(batch))
	for i, o := range batch {
		ids[i] = o.id
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM dispatch_outbox WHERE id = ANY($1)`, ids,
	); err != nil {
		return 0, fmt.Errorf("relay: delete rows: %w", err)
	}

	// Write the durable dispatch trail to audit_log (FR-015, FR-017).
	if err := insertAuditEntries(ctx, tx, batch); err != nil {
		return 0, fmt.Errorf("relay: audit log: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("relay: commit: %w", err)
	}

	relayPublishTotal.WithLabelValues("success").Add(float64(len(batch)))
	r.logger.Debug("dispatch: relay batch published", "count", len(batch))
	return len(batch), nil
}

// sampleBacklog runs OUTSIDE any transaction to keep the backlog gauge fresh
// even during long idle windows.
func (r *relay) sampleBacklog(ctx context.Context) {
	var n int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&n); err != nil {
		return
	}
	outboxBacklog.Set(float64(n))
}

func (r *relay) sampleBacklogInTx(ctx context.Context, tx pgx.Tx) {
	var n int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&n); err == nil {
		outboxBacklog.Set(float64(n))
	}
}

// insertAuditEntries writes one audit_log row per published dispatch. Uses a
// single multi-row INSERT for efficiency.
func insertAuditEntries(ctx context.Context, tx pgx.Tx, batch []outboxRow) error {
	if len(batch) == 0 {
		return nil
	}
	// One INSERT with unnest arrays keeps this cheap even at batch size 200.
	instanceIDs := make([]string, len(batch))
	jobIDs := make([]string, len(batch))
	for i, o := range batch {
		instanceIDs[i] = o.instanceID
		jobIDs[i] = o.jobID
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO audit_log (actor, kind, instance_id, detail)
		 SELECT 'dispatch-relay', 'DISPATCHED_VIA_BROKER', instance_id, jsonb_build_object('job_id', job_id)
		 FROM unnest($1::text[], $2::text[]) AS t(instance_id, job_id)`,
		instanceIDs, jobIDs,
	)
	if err != nil {
		return fmt.Errorf("insert audit rows: %w", err)
	}
	return nil
}

// classifyKafkaErr maps a franz-go error into a stable label value for the
// producer-errors counter. Returns "unknown" for values outside the library's
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
