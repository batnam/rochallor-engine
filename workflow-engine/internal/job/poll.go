// Package job implements the job poll/complete/fail/retry lifecycle and the
// background lease-expiry sweeper. Workers interact with the Engine exclusively
// via the functions in this package.
package job

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

const defaultLockDuration = 30 * time.Second

// Poll claims up to max unlocked jobs of the given jobTypes for workerID.
// Uses SELECT … FOR UPDATE SKIP LOCKED so concurrent pollers never hand out the same job.
func Poll(ctx context.Context, pool *pgxpool.Pool, workerID string, jobTypes []string, max int) ([]instance.Job, error) {
	if len(jobTypes) == 0 || max <= 0 {
		return nil, nil
	}

	lockExpiresAt := time.Now().Add(defaultLockDuration)

	var rowsErr error
	var jobs []instance.Job
	err := postgres.ObserveLockWait("job.poll_skip_locked", func() error {
		rows, qerr := pool.Query(ctx, `
			UPDATE job
			SET    status = 'LOCKED',
			       worker_id = $1,
			       locked_at = now(),
			       lock_expires_at = $2
			WHERE  id IN (
			    SELECT id FROM job
			    WHERE  status = 'UNLOCKED'
			    AND    job_type = ANY($3)
			    ORDER  BY created_at
			    FOR UPDATE SKIP LOCKED
			    LIMIT  $4
			)
			RETURNING id, instance_id, step_execution_id, job_type, status,
			          worker_id, locked_at, lock_expires_at,
			          retries_remaining, payload, created_at`,
			workerID, lockExpiresAt, jobTypes, max,
		)
		if qerr != nil {
			return fmt.Errorf("poll jobs: %w", qerr)
		}
		defer rows.Close()

		for rows.Next() {
			var j instance.Job
			if serr := rows.Scan(
				&j.ID, &j.InstanceID, &j.StepExecutionID, &j.JobType, &j.Status,
				&j.WorkerID, &j.LockedAt, &j.LockExpiresAt,
				&j.RetriesRemaining, &j.Payload, &j.CreatedAt,
			); serr != nil {
				return fmt.Errorf("scan job: %w", serr)
			}
			obs.JobPickupLatency.Observe(time.Since(j.CreatedAt).Seconds())
			jobs = append(jobs, j)
		}
		rowsErr = rows.Err()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return jobs, rowsErr
}
