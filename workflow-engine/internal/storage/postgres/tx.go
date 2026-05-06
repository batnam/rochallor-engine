package postgres

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
)

// pgTx wraps pgx.Tx and satisfies db.Tx. The wrapping is the entire
// reason db.Tx exists — domain packages can hold a *pgTx without
// importing pgx because it surfaces only as the db.Tx interface.
type pgTx struct{ inner pgx.Tx }

func (p *pgTx) TxMarker() {}

// Unwrap extracts the underlying pgx.Tx from a db.Tx. Only infrastructure
// packages that already import pgx (today: dispatch/kafka_outbox) may
// call this. Calling it from a domain package defeats the abstraction.
//
// Panics if tx is not a *pgTx — that indicates a programming error: a
// domain package created a fake transaction instead of going through
// pgDB.RunInTx.
func Unwrap(tx db.Tx) pgx.Tx {
	return tx.(*pgTx).inner
}

// pgDB wraps *pgxpool.Pool and satisfies db.DB.
type pgDB struct {
	pool *pgxpool.Pool
}

// NewDB returns a db.DB backed by pool. The pool is owned by the caller
// (cmd/engine/main.go) — the DB does not close it on shutdown.
func NewDB(pool *pgxpool.Pool) db.DB {
	return &pgDB{pool: pool}
}

func (p *pgDB) RunInTx(ctx context.Context, txType string, fn func(db.Tx) error) error {
	start := time.Now()
	err := pgx.BeginTxFunc(ctx, p.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fn(&pgTx{inner: tx})
	})
	dur := time.Since(start)
	obs.DBTransactionDuration.WithLabelValues(txType).Observe(dur.Seconds())

	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
	}
	attrs := []slog.Attr{
		slog.String("tx_type", txType),
		slog.Duration("duration", dur),
	}
	if err != nil {
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	obs.FromContext(ctx).LogAttrs(ctx, level, "engine tx", attrs...)
	return err
}

func (p *pgDB) TryAcquireAdvisoryLock(ctx context.Context, key int64) (bool, func(), error) {
	return TryAcquireAdvisoryLock(ctx, p.pool, key)
}
