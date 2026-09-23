package job

import (
	"context"
	"fmt"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/id"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// Retry replaces a LOCKED delivery attempt without spending a business retry.
// The old job ID is never reused, even when the same worker picks up the retry.
func Retry(ctx context.Context, dbConn db.DB, store JobStore, d dispatch.Dispatcher, jobID string) error {
	_, err := retryJob(ctx, dbConn, store, d, jobID, false)
	return err
}

func retryJob(ctx context.Context, dbConn db.DB, store JobStore, d dispatch.Dispatcher, jobID string, expiredOnly bool) (bool, error) {
	replaced := false
	err := dbConn.RunInTx(ctx, "job.retry", func(tx db.Tx) error {
		state, err := store.LockJobExecution(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if !state.Active() || state.JobStatus != instance.JobStatusLocked || (expiredOnly && state.LeaseValid) {
			return nil
		}
		if err := store.CancelLockedJob(ctx, tx, jobID); err != nil {
			return err
		}
		j, err := store.GetJobForFail(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if err := enqueueReplacement(ctx, tx, store, d, j); err != nil {
			return err
		}
		replaced = true
		return nil
	})
	return replaced && err == nil, err
}

// The replacement row and its dispatch must commit together. Keeping the
// retired row makes old callbacks idempotent and preserves delivery history.
func enqueueReplacement(ctx context.Context, tx db.Tx, store JobStore, d dispatch.Dispatcher, j dispatch.DispatchJob) error {
	j.ID = id.NewJob()
	j.CreatedAt = time.Now()
	if err := store.InsertJob(ctx, tx, j); err != nil {
		return err
	}
	if err := d.Enqueue(ctx, tx, j); err != nil {
		return fmt.Errorf("enqueue replacement job: %w", err)
	}
	return nil
}
