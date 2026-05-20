package boundary

import (
	"context"
	"log/slog"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
)

// housekeeperLockKey gates the housekeeper across replicas via a session-level
// PostgreSQL advisory lock. Distinct from timerSweeperLockKey so the two
// background loops do not block each other.
const housekeeperLockKey int64 = 0x6C756F6E_67686B70 // "luonghkp" low bits

// StartBoundaryHousekeeper runs a background goroutine that periodically
// deletes fired boundary_event_schedule rows older than retention. It exits
// when ctx is cancelled.
func StartBoundaryHousekeeper(ctx context.Context, dbConn db.DB, store BoundaryStore, interval, retention time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepHousekeeper(ctx, dbConn, store, retention)
			}
		}
	}()
}

func sweepHousekeeper(ctx context.Context, dbConn db.DB, store BoundaryStore, retention time.Duration) {
	acquired, release, err := dbConn.TryAcquireAdvisoryLock(ctx, housekeeperLockKey)
	if err != nil {
		slog.Error("boundary housekeeper: advisory lock acquire failed", "err", err)
		return
	}
	if !acquired {
		return
	}
	defer release()

	deleted, err := store.DeleteObsoleteBoundaryEvents(ctx, retention)
	if err != nil {
		slog.Error("boundary housekeeper: delete failed", "err", err)
		return
	}
	if deleted > 0 {
		slog.Info("boundary housekeeper: deleted obsolete rows",
			"deleted", deleted,
			"retention", retention.String(),
		)
	}
}
