package job

import (
	"context"
	"fmt"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
)

// Retry re-enqueues a LOCKED job without decrementing retries_remaining.
// This is used for infrastructure-level retries (e.g., worker crash) distinct
// from business-logic retries handled in Fail.
func Retry(ctx context.Context, dbConn db.DB, store JobStore, d dispatch.Dispatcher, jobID string) error {
	return dbConn.RunInTx(ctx, "job.retry", func(tx db.Tx) error {
		j, err := store.GetJobForFail(ctx, tx, jobID)
		if err != nil {
			return fmt.Errorf("load job for retry: %w", err)
		}
		if err := d.Enqueue(ctx, tx, j); err != nil {
			return fmt.Errorf("re-enqueue job for retry: %w", err)
		}
		rows, err := store.UnlockJob(ctx, tx, jobID)
		if err != nil {
			return fmt.Errorf("retry job update %q: %w", jobID, err)
		}
		if rows == 0 {
			return fmt.Errorf("retry: job %q not found or not LOCKED", jobID)
		}
		return nil
	})
}
