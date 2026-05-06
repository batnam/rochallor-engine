// Package db defines the storage-abstraction seam used by all domain
// packages (instance, job, boundary, definition). It is intentionally tiny:
// only context-scoped transaction lifecycle and advisory-lock primitives.
//
// The package has no dependencies beyond the standard library — in
// particular, no pgx import — so domain code that imports `db` cannot
// transitively pick up driver types.
package db

import "context"

// Tx is an opaque transaction token. Domain code passes it through to
// Store methods but never inspects it. The marker method name signals
// that callers should never implement this interface themselves; only
// internal/storage/postgres creates real Tx values, and test mocks in
// the instance package create fake ones.
type Tx interface {
	TxMarker()
}

// DB is the lifecycle / coordination interface for the storage backend.
// It manages transactions and distributed advisory locks. Per-table SQL
// lives behind per-package Store interfaces, NOT here.
type DB interface {
	// RunInTx begins a transaction, calls fn, and commits on nil-error
	// or rolls back on any non-nil error returned by fn.
	//
	// Implementations are expected to record transaction wall-clock
	// duration into the obs.DBTransactionDuration histogram labelled by
	// txType, and to emit one structured log line per transaction so
	// every state-transition is observable without per-callsite logging.
	RunInTx(ctx context.Context, txType string, fn func(Tx) error) error

	// TryAcquireAdvisoryLock attempts a non-blocking session-level
	// advisory lock. On success returns (true, release, nil) where
	// release MUST be called to free the lock (typically via defer).
	// On contention returns (false, nil, nil) — callers should skip
	// their periodic work and try again next interval. Errors are
	// reserved for backend-level failures only.
	TryAcquireAdvisoryLock(ctx context.Context, key int64) (acquired bool, release func(), err error)
}
