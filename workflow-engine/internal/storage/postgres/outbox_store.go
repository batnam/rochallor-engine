package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
)

// OutboxStore implements kafka_outbox.OutboxStore against Postgres.
type OutboxStore struct {
	pool *pgxpool.Pool
}

// NewOutboxStore returns the Postgres-backed kafka_outbox.OutboxStore.
func NewOutboxStore(pool *pgxpool.Pool) kafka_outbox.OutboxStore {
	return &OutboxStore{pool: pool}
}

func (s *OutboxStore) Enqueue(ctx context.Context, tx db.Tx, row kafka_outbox.OutboxRow) error {
	if _, err := Unwrap(tx).Exec(ctx,
		`INSERT INTO dispatch_outbox (id, job_id, instance_id, job_type, payload)
		 VALUES ($1, $2, $3, $4, $5)`,
		row.ID, row.JobID, row.InstanceID, row.JobType, row.Payload,
	); err != nil {
		return fmt.Errorf("insert dispatch_outbox: %w", err)
	}
	return nil
}

func (s *OutboxStore) ClaimBatch(ctx context.Context, tx db.Tx, limit int) ([]kafka_outbox.OutboxRow, error) {
	rows, err := Unwrap(tx).Query(ctx,
		`SELECT id, job_id, instance_id, job_type, payload
		 FROM dispatch_outbox
		 ORDER BY created_at
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("select outbox: %w", err)
	}
	defer rows.Close()

	var out []kafka_outbox.OutboxRow
	for rows.Next() {
		var r kafka_outbox.OutboxRow
		if err := rows.Scan(&r.ID, &r.JobID, &r.InstanceID, &r.JobType, &r.Payload); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return out, nil
}

func (s *OutboxStore) DeleteByIDs(ctx context.Context, tx db.Tx, ids []string) error {
	if _, err := Unwrap(tx).Exec(ctx,
		`DELETE FROM dispatch_outbox WHERE id = ANY($1)`, ids,
	); err != nil {
		return fmt.Errorf("delete rows: %w", err)
	}
	return nil
}

func (s *OutboxStore) WriteAuditEntries(ctx context.Context, tx db.Tx, rows []kafka_outbox.OutboxRow) error {
	if len(rows) == 0 {
		return nil
	}
	instanceIDs := make([]string, len(rows))
	jobIDs := make([]string, len(rows))
	for i, r := range rows {
		instanceIDs[i] = r.InstanceID
		jobIDs[i] = r.JobID
	}
	// One INSERT with unnest arrays keeps this cheap even at batch size 200.
	if _, err := Unwrap(tx).Exec(ctx,
		`INSERT INTO audit_log (actor, kind, instance_id, detail)
		 SELECT 'dispatch-relay', 'DISPATCHED_VIA_BROKER', instance_id, jsonb_build_object('job_id', job_id)
		 FROM unnest($1::text[], $2::text[]) AS t(instance_id, job_id)`,
		instanceIDs, jobIDs,
	); err != nil {
		return fmt.Errorf("insert audit rows: %w", err)
	}
	return nil
}

func (s *OutboxStore) Backlog(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *OutboxStore) BacklogInTx(ctx context.Context, tx db.Tx) (int64, error) {
	var n int64
	if err := Unwrap(tx).QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *OutboxStore) CheckMigrationApplied(ctx context.Context) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE  table_schema = 'public'
			AND    table_name   = 'dispatch_outbox'
		)`).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}
