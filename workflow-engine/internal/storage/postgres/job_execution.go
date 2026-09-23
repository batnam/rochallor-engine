package postgres

import (
	"context"
	"fmt"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// Lock instance before job, matching manual retry and boundary interruption.
// Every callback checks these states before changing even an output snapshot.
func lockJobExecution(ctx context.Context, tx db.Tx, jobID string) (instance.JobExecution, error) {
	var state instance.JobExecution
	err := ObserveLockWait("instance.for_update", func() error {
		return Unwrap(tx).QueryRow(ctx,
			`SELECT status FROM workflow_instance
			 WHERE id = (SELECT instance_id FROM job WHERE id = $1) FOR UPDATE`, jobID,
		).Scan(&state.InstanceStatus)
	})
	if err != nil {
		return state, fmt.Errorf("lock job instance: %w", err)
	}
	err = Unwrap(tx).QueryRow(ctx,
		`SELECT j.status, COALESCE(j.worker_id, ''),
		        COALESCE(j.lock_expires_at > clock_timestamp(), false), s.status
		 FROM job j JOIN step_execution s ON s.id = j.step_execution_id
		 WHERE j.id = $1 FOR UPDATE OF j`, jobID,
	).Scan(&state.JobStatus, &state.WorkerID, &state.LeaseValid, &state.StepStatus)
	if err != nil {
		return state, fmt.Errorf("lock job execution: %w", err)
	}
	return state, nil
}

func (s *JobStore) LockJobExecution(ctx context.Context, tx db.Tx, jobID string) (instance.JobExecution, error) {
	return lockJobExecution(ctx, tx, jobID)
}

func (s *InstanceStore) LockJobExecution(ctx context.Context, tx db.Tx, jobID string) (instance.JobExecution, error) {
	return lockJobExecution(ctx, tx, jobID)
}
