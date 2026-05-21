package kafka_outbox

import (
	"context"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
)

// OutboxRow is the narrow read/write model carried in and out of
// dispatch_outbox. The created_at column is set by Postgres on INSERT
// (default now()) and is only used by the SQL ORDER BY clause, so it
// does not appear here.
type OutboxRow struct {
	ID         string
	JobID      string
	InstanceID string
	JobType    string
	Payload    []byte
}

// OutboxStore is the per-table repository for dispatch_outbox + the
// audit_log rows the relay writes alongside each published batch.
// Implementations live in internal/storage/postgres; this interface lets
// the kafka_outbox package stay free of pgx imports.
type OutboxStore interface {
	// Enqueue writes one pending dispatch row inside the caller's tx.
	Enqueue(ctx context.Context, tx db.Tx, row OutboxRow) error

	// ClaimBatch returns up to `limit` pending rows ordered by created_at,
	// holding pg_advisory row locks (SELECT ... FOR UPDATE SKIP LOCKED).
	// Returns an empty slice when nothing is available.
	ClaimBatch(ctx context.Context, tx db.Tx, limit int) ([]OutboxRow, error)

	// DeleteByIDs removes the rows previously claimed in the same tx.
	DeleteByIDs(ctx context.Context, tx db.Tx, ids []string) error

	// WriteAuditEntries appends one audit_log row per published dispatch
	// in the same tx that commits the delete.
	WriteAuditEntries(ctx context.Context, tx db.Tx, rows []OutboxRow) error

	// Backlog returns the current dispatch_outbox row count using a
	// pool-level read (no caller tx).
	Backlog(ctx context.Context) (int64, error)

	// BacklogInTx is the same count but reuses an open tx so an empty
	// drain cycle can sample the gauge without a second connection.
	BacklogInTx(ctx context.Context, tx db.Tx) (int64, error)

	// CheckMigrationApplied returns whether the dispatch_outbox table
	// exists. The runtime calls this once at Start as a fail-fast check
	// that migration 0009 has been applied.
	CheckMigrationApplied(ctx context.Context) (bool, error)
}
