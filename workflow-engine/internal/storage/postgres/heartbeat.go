package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/jackc/pgx/v5"
)

func (s *JobStore) ExtendJobLease(ctx context.Context, tx db.Tx, jobID, workerID string, seconds int) (time.Time, bool, error) {
	state, err := lockJobExecution(ctx, tx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if !state.Active() || state.JobStatus != instance.JobStatusLocked || state.WorkerID != workerID || !state.LeaseValid {
		return time.Time{}, false, nil
	}
	var expiry time.Time
	// Recheck against DB time at the actual update, even if the lease expired
	// between inspection and this statement. Never shorten a longer lease.
	err = Unwrap(tx).QueryRow(ctx, `UPDATE job
		SET lock_expires_at = GREATEST(lock_expires_at, clock_timestamp() + $3 * interval '1 second')
		WHERE id=$1 AND worker_id=$2 AND status='LOCKED' AND lock_expires_at > clock_timestamp()
		RETURNING lock_expires_at`, jobID, workerID, seconds).Scan(&expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("extend job lease: %w", err)
	}
	return expiry, true, nil
}
