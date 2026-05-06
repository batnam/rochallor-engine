package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
)

// JobStore implements job.JobStore.
type JobStore struct {
	pool *pgxpool.Pool
}

// NewJobStore returns a job.JobStore backed by pool.
func NewJobStore(pool *pgxpool.Pool) job.JobStore {
	return &JobStore{pool: pool}
}

func (s *JobStore) GetJobForComplete(ctx context.Context, tx db.Tx, jobID string) (string, string, int, error) {
	var instanceID, stepExecID string
	var retriesRemaining int
	err := Unwrap(tx).QueryRow(ctx,
		`SELECT instance_id, step_execution_id, retries_remaining
		   FROM job WHERE id = $1 FOR UPDATE`,
		jobID,
	).Scan(&instanceID, &stepExecID, &retriesRemaining)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", 0, fmt.Errorf("job %q not found", jobID)
		}
		return "", "", 0, fmt.Errorf("load job for complete: %w", err)
	}
	return instanceID, stepExecID, retriesRemaining, nil
}

func (s *JobStore) GetJobStatusForIdempotency(ctx context.Context, tx db.Tx, jobID string) (string, error) {
	var status string
	if err := Unwrap(tx).QueryRow(ctx,
		`SELECT status FROM job WHERE id = $1`,
		jobID,
	).Scan(&status); err != nil {
		return "", fmt.Errorf("get job status: %w", err)
	}
	return status, nil
}

func (s *JobStore) GetStepExecutionStepID(ctx context.Context, tx db.Tx, stepExecID string) (string, error) {
	var stepID string
	if err := Unwrap(tx).QueryRow(ctx,
		`SELECT step_id FROM step_execution WHERE id = $1`,
		stepExecID,
	).Scan(&stepID); err != nil {
		return "", fmt.Errorf("load step_execution for complete: %w", err)
	}
	return stepID, nil
}

func (s *JobStore) MarkJobCompleted(ctx context.Context, tx db.Tx, jobID, workerID string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE job SET status = 'COMPLETED', worker_id = $1 WHERE id = $2`,
		workerID, jobID,
	)
	if err != nil {
		return fmt.Errorf("mark job completed: %w", err)
	}
	return nil
}

func (s *JobStore) MarkStepExecutionCompleted(ctx context.Context, tx db.Tx, stepExecID string, outputSnapshot []byte) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE step_execution SET status = 'COMPLETED', ended_at = now(), output_snapshot = $1 WHERE id = $2`,
		outputSnapshot, stepExecID,
	)
	if err != nil {
		return fmt.Errorf("mark step_execution completed: %w", err)
	}
	return nil
}

func (s *JobStore) GetJobForFail(ctx context.Context, tx db.Tx, jobID string) (dispatch.DispatchJob, error) {
	var j dispatch.DispatchJob
	err := Unwrap(tx).QueryRow(ctx,
		`SELECT id, instance_id, step_execution_id, job_type, retries_remaining, payload, created_at
		   FROM job WHERE id = $1 FOR UPDATE`,
		jobID,
	).Scan(&j.ID, &j.InstanceID, &j.StepExecutionID, &j.JobType, &j.RetriesRemaining, &j.Payload, &j.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dispatch.DispatchJob{}, fmt.Errorf("job %q not found", jobID)
		}
		return dispatch.DispatchJob{}, fmt.Errorf("load job for fail: %w", err)
	}
	return j, nil
}

func (s *JobStore) MarkJobFailed(ctx context.Context, tx db.Tx, jobID, workerID string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE job SET status = 'FAILED', worker_id = $1 WHERE id = $2`,
		workerID, jobID,
	)
	if err != nil {
		return fmt.Errorf("mark job failed: %w", err)
	}
	return nil
}

func (s *JobStore) MarkStepExecutionFailed(ctx context.Context, tx db.Tx, stepExecID, reason string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE step_execution SET status = 'FAILED', ended_at = now(), failure_reason = $1 WHERE id = $2`,
		reason, stepExecID,
	)
	if err != nil {
		return fmt.Errorf("mark step_execution failed: %w", err)
	}
	return nil
}

func (s *JobStore) MarkInstanceFailed(ctx context.Context, tx db.Tx, instanceID, reason string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE workflow_instance SET status = 'FAILED', failure_reason = $1, completed_at = now() WHERE id = $2`,
		reason, instanceID,
	)
	if err != nil {
		return fmt.Errorf("mark instance failed: %w", err)
	}
	return nil
}

func (s *JobStore) ReenqueueJob(ctx context.Context, tx db.Tx, jobID string, newRetriesRemaining int) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE job SET status = 'UNLOCKED', worker_id = NULL, locked_at = NULL, lock_expires_at = NULL,
		                 retries_remaining = $1 WHERE id = $2`,
		newRetriesRemaining, jobID,
	)
	if err != nil {
		return fmt.Errorf("re-enqueue job update: %w", err)
	}
	return nil
}

func (s *JobStore) UnlockJob(ctx context.Context, tx db.Tx, jobID string) (int64, error) {
	tag, err := Unwrap(tx).Exec(ctx,
		`UPDATE job
		    SET status = 'UNLOCKED',
		        worker_id = NULL,
		        locked_at = NULL,
		        lock_expires_at = NULL
		  WHERE id = $1 AND status = 'LOCKED'`,
		jobID,
	)
	if err != nil {
		return 0, fmt.Errorf("unlock job %q: %w", jobID, err)
	}
	return tag.RowsAffected(), nil
}

func (s *JobStore) GetExpiredLeases(ctx context.Context, tx db.Tx) ([]dispatch.DispatchJob, error) {
	rows, err := Unwrap(tx).Query(ctx,
		`SELECT id, instance_id, step_execution_id, job_type, retries_remaining, payload, created_at
		   FROM   job
		   WHERE  status = 'LOCKED' AND lock_expires_at < now()
		   FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return nil, fmt.Errorf("get expired leases: %w", err)
	}
	defer rows.Close()
	var out []dispatch.DispatchJob
	for rows.Next() {
		var j dispatch.DispatchJob
		if err := rows.Scan(&j.ID, &j.InstanceID, &j.StepExecutionID, &j.JobType, &j.RetriesRemaining, &j.Payload, &j.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *JobStore) PollJobs(ctx context.Context, workerID string, jobTypes []string, max int, lockDurationSeconds int) ([]instance.Job, error) {
	if len(jobTypes) == 0 || max <= 0 {
		return nil, nil
	}
	lockExpiresAt := time.Now().Add(time.Duration(lockDurationSeconds) * time.Second)

	var jobs []instance.Job
	var rowsErr error
	err := ObserveLockWait("job.poll_skip_locked", func() error {
		rows, qerr := s.pool.Query(ctx, `
			UPDATE job
			SET    status = 'LOCKED',
			       worker_id = $1,
			       locked_at = now(),
			       lock_expires_at = $2
			WHERE  id IN (
			    SELECT id FROM job
			    WHERE  status = 'UNLOCKED' AND job_type = ANY($3)
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

// Compile-time interface assertion.
var _ job.JobStore = (*JobStore)(nil)
