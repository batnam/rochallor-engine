package job

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
)

// leaseSweeperLockKey gates the lease-expiry sweeper across replicas via a
// session-level PostgreSQL advisory lock. The literal value matches the
// pre-refactor postgres.LeaseSweeperLockKey so a rolling deploy across
// multiple replicas does not lose the gate.
const leaseSweeperLockKey int64 = 0x6C756F6E_676C7331 // "luonglse" low bits

// StartLeaseSweeper runs a background goroutine that periodically unlocks
// jobs whose lock_expires_at has passed (worker crash / slow worker). It
// exits when ctx is cancelled.
//
// Across multiple engine replicas the sweep is gated by leaseSweeperLockKey
// so only one replica sweeps per interval.
func StartLeaseSweeper(ctx context.Context, dbConn db.DB, store JobStore, d dispatch.Dispatcher, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepExpiredLeases(ctx, dbConn, store, d)
			}
		}
	}()
}

func sweepExpiredLeases(ctx context.Context, dbConn db.DB, store JobStore, d dispatch.Dispatcher) {
	acquired, release, err := dbConn.TryAcquireAdvisoryLock(ctx, leaseSweeperLockKey)
	if err != nil {
		slog.Error("lease sweeper: advisory lock acquire failed", "err", err)
		return
	}
	if !acquired {
		return
	}
	defer release()

	err = dbConn.RunInTx(ctx, "job.lease_sweeper", func(tx db.Tx) error {
		expired, err := store.GetExpiredLeases(ctx, tx)
		if err != nil {
			return err
		}
		if len(expired) == 0 {
			return nil
		}
		for _, j := range expired {
			if err := d.Enqueue(ctx, tx, j); err != nil {
				return fmt.Errorf("re-enqueue job %q: %w", j.ID, err)
			}
			if _, err := store.UnlockJob(ctx, tx, j.ID); err != nil {
				return err
			}
		}
		obs.JobTimeoutTotal.Add(float64(len(expired)))
		slog.Info("lease sweeper: reclaimed expired jobs", "count", len(expired))
		return nil
	})
	if err != nil {
		slog.Error("lease sweeper: sweep failed", "err", err)
	}
}
