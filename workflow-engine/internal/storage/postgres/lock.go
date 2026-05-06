package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Advisory-lock keys for sweeper leader-election live in the domain packages:
//   - LeaseSweeperLockKey → internal/job/lease_sweeper.go
//   - TimerSweeperLockKey → internal/boundary/timer_sweeper.go

// TryAcquireAdvisoryLock attempts a non-blocking session-level advisory lock
// via pg_try_advisory_lock. On success it returns (true, release, nil) where
// release must be called to free the lock (typically via defer). On contention
// it returns (false, nil, nil) — callers should skip their periodic work and
// try again next interval. Errors are DB-level failures only.
//
// Important: the lock is session-scoped, so the same pgxpool connection must
// be used for both acquire and release. The implementation holds one connection
// for the lock's lifetime — do not call this in tight loops against a small pool.
func TryAcquireAdvisoryLock(ctx context.Context, pool *pgxpool.Pool, key int64) (acquired bool, release func(), err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("advisory-lock acquire conn: %w", err)
	}

	var got bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil {
		conn.Release()
		return false, nil, fmt.Errorf("pg_try_advisory_lock(%d): %w", key, err)
	}
	if !got {
		conn.Release()
		return false, nil, nil
	}

	release = func() {
		// Use background context so releases happen even under ctx cancellation.
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		conn.Release()
	}
	return true, release, nil
}
