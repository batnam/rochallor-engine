// Package postgres provides the PostgreSQL storage implementation for the
// workflow engine. All repository interfaces are implemented here using
// github.com/jackc/pgx/v5.
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
)

// PoolOptions configures NewPoolWithOptions. Zero values fall back to the
// historical runtime.NumCPU()*4 sizing with a floor of 4.
type PoolOptions struct {
	// MaxConns caps the pgxpool. 0 means "use the default
	// runtime.NumCPU()*4 with a floor of 4".
	MaxConns int32
	// MinConns is the minimum idle connections the pool will keep open.
	// 0 preserves pgxpool's on-demand behaviour.
	MinConns int32
}

// NewPool creates and validates a pgx connection pool with default sizing
// (runtime.NumCPU() * 4, floor 4). Kept for callers that have not migrated
// to the config-driven path.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return NewPoolWithOptions(ctx, dsn, PoolOptions{})
}

// NewPoolWithOptions creates and validates a pgx connection pool with the
// supplied sizing. The pool is validated by a ping so misconfigured DSNs
// fail at startup, not at the first query.
func NewPoolWithOptions(ctx context.Context, dsn string, opts PoolOptions) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}

	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	} else {
		cfg.MaxConns = int32(runtime.NumCPU() * 4)
		if cfg.MaxConns < 4 {
			cfg.MaxConns = 4
		}
	}
	if opts.MinConns > 0 {
		cfg.MinConns = opts.MinConns
	}
	if cfg.MinConns > cfg.MaxConns {
		return nil, fmt.Errorf("postgres: DB_MIN_CONNS (%d) must be <= DB_MAX_CONNS (%d)", cfg.MinConns, cfg.MaxConns)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}

	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping failed — check WE_POSTGRES_DSN: %w", err)
	}

	return pool, nil
}

// RunInTx wraps pgx.BeginTxFunc and records transaction wall-clock duration
// (Begin → Commit/Rollback) into obs.DBTransactionDuration labelled by
// txType (FR-007 / SC-005). It also emits one structured log line per
// transaction so every workflow-instance state transition is visible
// without per-site log calls. The fn's error is returned unchanged.
func RunInTx(ctx context.Context, pool *pgxpool.Pool, txType string, opts pgx.TxOptions, fn func(tx pgx.Tx) error) error {
	start := time.Now()
	err := pgx.BeginTxFunc(ctx, pool, opts, fn)
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

// ObserveLockWait runs fn and records its wall-clock duration into
// obs.DBLockWaitDuration labelled by operation. Intended for wrapping
// FOR UPDATE / FOR UPDATE SKIP LOCKED queries so pre-acquire wait time
// surfaces as a first-class signal.
func ObserveLockWait(operation string, fn func() error) error {
	start := time.Now()
	err := fn()
	obs.DBLockWaitDuration.WithLabelValues(operation).Observe(time.Since(start).Seconds())
	return err
}
