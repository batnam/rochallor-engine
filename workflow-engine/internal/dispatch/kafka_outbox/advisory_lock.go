package kafka_outbox

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockKey is a stable 64-bit integer derived from the constant string
// "workflow-engine.dispatch_relay". Using a fixed numeric value avoids a
// Postgres roundtrip for hashtext() on every retry; correctness is that all
// replicas of the same engine agree on the key. We commit to this value via
// the constant below.
//
// Derivation: SELECT hashtext('workflow-engine.dispatch_relay')::bigint.
// The exact value matters only in that every replica uses the same one; a
// constant is both simpler and drift-proof across deployments.
const advisoryLockKey int64 = 4893219751932018491

// leaderElection runs a background loop that tries to acquire a PostgreSQL
// session-scoped advisory lock. At most one replica at a time will hold it
// The lock is held on a dedicated connection; if that
// connection dies, the session ends and the lock is released automatically.
type leaderElection struct {
	pool          *pgxpool.Pool
	retryInterval time.Duration
	logger        *slog.Logger

	// onLeader and onLeaderLost are invoked in the election goroutine when
	// leadership transitions. They must not block — relay start/stop should
	// be implemented as channel sends or atomic flag updates.
	onLeader     func(context.Context)
	onLeaderLost func(context.Context)

	// isLeader mirrors the lock state as an atomic bool so other components
	// (metrics, health checks) can read it without coordinating with the
	// election loop.
	isLeader atomic.Bool

	// conn is the dedicated pgx connection holding the advisory lock. It
	// is nil until the first successful acquire and after a graceful Stop.
	conn *pgx.Conn
}

func newLeaderElection(pool *pgxpool.Pool, logger *slog.Logger, onLeader, onLeaderLost func(context.Context)) *leaderElection {
	return &leaderElection{
		pool:          pool,
		retryInterval: defaultLeaderRetryInterval * time.Second,
		logger:        logger,
		onLeader:      onLeader,
		onLeaderLost:  onLeaderLost,
	}
}

// IsLeader returns true if this replica currently holds the relay advisory
// lock. Safe for concurrent callers (metrics, health probes).
func (le *leaderElection) IsLeader() bool {
	return le.isLeader.Load()
}

// run loops until ctx is cancelled, attempting to become leader and detecting
// leadership loss. It does NOT return an error — advisory-lock contention is
// normal (losing replicas) and not a failure condition.
func (le *leaderElection) run(ctx context.Context) {
	// Small jitter so replicas don't all retry at the same instant.
	jitter := time.Duration(rand.Int63n(int64(le.retryInterval / 4)))
	time.Sleep(jitter)

	for {
		if ctx.Err() != nil {
			return
		}
		if !le.isLeader.Load() {
			if err := le.tryAcquire(ctx); err != nil {
				le.logger.Debug("dispatch: advisory lock acquire failed", "err", err)
			} else if le.isLeader.Load() {
				relayLeader.Set(1)
				le.logger.Info("dispatch: became relay leader")
				if le.onLeader != nil {
					le.onLeader(ctx)
				}
			}
		} else {
			// Check liveness: a failed Ping on the dedicated connection
			// means the session ended (crash, broker-side TCP reset, DBA
			// termination) — Postgres has released the lock.
			if err := le.pingLockConn(ctx); err != nil {
				le.logger.Warn("dispatch: advisory lock session lost", "err", err)
				le.isLeader.Store(false)
				relayLeader.Set(0)
				le.closeLockConn(ctx)
				if le.onLeaderLost != nil {
					le.onLeaderLost(ctx)
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(le.retryInterval):
		}
	}
}

// tryAcquire opens a dedicated connection (or reuses one) and runs
// pg_try_advisory_lock. On success, sets isLeader=true.
func (le *leaderElection) tryAcquire(ctx context.Context) error {
	if le.conn == nil {
		conn, err := le.pool.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("acquire conn: %w", err)
		}
		// Hijack the connection from the pool so it is permanently ours
		// for the lifetime of the leadership (or until Stop). pgx's
		// Hijack() returns the raw *pgx.Conn and removes it from the pool.
		raw := conn.Hijack()
		le.conn = raw
	}
	var got bool
	if err := le.conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, advisoryLockKey).Scan(&got); err != nil {
		// The connection is probably dead — close it so the next attempt
		// gets a fresh one.
		le.closeLockConn(ctx)
		return fmt.Errorf("try advisory lock: %w", err)
	}
	if got {
		le.isLeader.Store(true)
	}
	return nil
}

// pingLockConn performs a cheap roundtrip to detect session liveness.
func (le *leaderElection) pingLockConn(ctx context.Context) error {
	if le.conn == nil {
		return fmt.Errorf("no lock connection")
	}
	return le.conn.Ping(ctx)
}

// Stop releases the lock if held and closes the dedicated connection.
// Safe to call multiple times; safe to call when we never became leader.
func (le *leaderElection) Stop(ctx context.Context) {
	if le.conn == nil {
		return
	}
	if le.isLeader.Load() {
		// Best-effort unlock on graceful shutdown — losing this call is
		// harmless because closing the connection ends the session and
		// Postgres releases the lock automatically.
		_, _ = le.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, advisoryLockKey)
		le.isLeader.Store(false)
		relayLeader.Set(0)
		if le.onLeaderLost != nil {
			le.onLeaderLost(ctx)
		}
	}

	le.closeLockConn(ctx)
}

func (le *leaderElection) closeLockConn(ctx context.Context) {
	if le.conn != nil {
		_ = le.conn.Close(ctx)
		le.conn = nil
	}
}
