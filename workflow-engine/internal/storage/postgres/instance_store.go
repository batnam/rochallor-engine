package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// InstanceStore implements instance.Store.
type InstanceStore struct {
	pool *pgxpool.Pool
}

// NewInstanceStore returns an instance.Store backed by pool.
func NewInstanceStore(pool *pgxpool.Pool) instance.Store {
	return &InstanceStore{pool: pool}
}

// ─── reads ────────────────────────────────────────────────────────────────────

func (s *InstanceStore) GetInstanceDefinitionInfo(ctx context.Context, instanceID string) (string, int, error) {
	var defID string
	var defVersion int
	err := s.pool.QueryRow(ctx,
		`SELECT definition_id, definition_version FROM workflow_instance WHERE id = $1`,
		instanceID,
	).Scan(&defID, &defVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, instance.ErrInstanceNotFound
		}
		return "", 0, fmt.Errorf("get instance definition info: %w", err)
	}
	return defID, defVersion, nil
}

func (s *InstanceStore) GetInstance(ctx context.Context, instanceID string) (*instance.WorkflowInstance, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, definition_id, definition_version, status, current_step_ids, variables,
		        started_at, completed_at, failure_reason, business_key
		   FROM workflow_instance WHERE id = $1`,
		instanceID,
	)
	var inst instance.WorkflowInstance
	if err := row.Scan(
		&inst.ID, &inst.DefinitionID, &inst.DefinitionVersion, &inst.Status,
		&inst.CurrentStepIDs, &inst.Variables, &inst.StartedAt, &inst.CompletedAt,
		&inst.FailureReason, &inst.BusinessKey,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, instance.ErrInstanceNotFound
		}
		return nil, fmt.Errorf("get instance: %w", err)
	}
	return &inst, nil
}

func (s *InstanceStore) ListInstances(ctx context.Context, definitionID, status, businessKey string, page, pageSize int) (instance.ListResult, error) {
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := page * pageSize

	var conds []string
	var args []any
	n := 1
	if definitionID != "" {
		conds = append(conds, fmt.Sprintf("definition_id = $%d", n))
		args = append(args, definitionID)
		n++
	}
	if status != "" {
		conds = append(conds, fmt.Sprintf("status = $%d", n))
		args = append(args, status)
		n++
	}
	if businessKey != "" {
		conds = append(conds, fmt.Sprintf("business_key = $%d", n))
		args = append(args, businessKey)
		n++
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	itemArgs := append(append([]any{}, args...), pageSize, offset)
	rows, err := s.pool.Query(ctx,
		`SELECT id, definition_id, definition_version, status, current_step_ids, variables,
		        started_at, completed_at, failure_reason, business_key
		   FROM workflow_instance`+where+
			fmt.Sprintf(" ORDER BY started_at DESC LIMIT $%d OFFSET $%d", n, n+1),
		itemArgs...,
	)
	if err != nil {
		return instance.ListResult{}, fmt.Errorf("list instances: query: %w", err)
	}
	defer rows.Close()

	var items []instance.WorkflowInstance
	for rows.Next() {
		var inst instance.WorkflowInstance
		if err = rows.Scan(
			&inst.ID, &inst.DefinitionID, &inst.DefinitionVersion, &inst.Status,
			&inst.CurrentStepIDs, &inst.Variables, &inst.StartedAt, &inst.CompletedAt,
			&inst.FailureReason, &inst.BusinessKey,
		); err != nil {
			return instance.ListResult{}, fmt.Errorf("list instances: scan: %w", err)
		}
		items = append(items, inst)
	}
	if err = rows.Err(); err != nil {
		return instance.ListResult{}, fmt.Errorf("list instances: rows: %w", err)
	}

	var total int
	_ = s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM workflow_instance"+where, args...).Scan(&total)
	return instance.ListResult{Items: items, Total: total}, nil
}

func (s *InstanceStore) GetHistory(ctx context.Context, instanceID string) ([]instance.StepExecution, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, instance_id, step_id, step_type, attempt_number, status,
		        started_at, ended_at, failure_reason
		   FROM step_execution WHERE instance_id = $1 ORDER BY started_at`,
		instanceID,
	)
	if err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}
	defer rows.Close()

	var execs []instance.StepExecution
	for rows.Next() {
		var se instance.StepExecution
		if err = rows.Scan(
			&se.ID, &se.InstanceID, &se.StepID, &se.StepType, &se.AttemptNumber, &se.Status,
			&se.StartedAt, &se.EndedAt, &se.FailureReason,
		); err != nil {
			return nil, fmt.Errorf("scan step execution: %w", err)
		}
		execs = append(execs, se)
	}
	return execs, rows.Err()
}

func (s *InstanceStore) CancelInstance(ctx context.Context, instanceID, reason string) (*instance.WorkflowInstance, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE workflow_instance
		    SET status = $1, failure_reason = $2, completed_at = now()
		  WHERE id = $3 AND status IN ('ACTIVE','WAITING')
		  RETURNING id, definition_id, definition_version, status, current_step_ids, variables, started_at, failure_reason`,
		string(instance.InstanceStatusCancelled), reason, instanceID,
	)
	var inst instance.WorkflowInstance
	if err := row.Scan(
		&inst.ID, &inst.DefinitionID, &inst.DefinitionVersion, &inst.Status,
		&inst.CurrentStepIDs, &inst.Variables, &inst.StartedAt, &inst.FailureReason,
	); err != nil {
		return nil, fmt.Errorf("cancel: %w", err)
	}
	return &inst, nil
}

func (s *InstanceStore) GetJobInstanceAndStepExec(ctx context.Context, jobID string) (string, string, error) {
	var instanceID, stepExecID string
	err := s.pool.QueryRow(ctx,
		`SELECT instance_id, step_execution_id FROM job WHERE id = $1`,
		jobID,
	).Scan(&instanceID, &stepExecID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", fmt.Errorf("job %q not found", jobID)
		}
		return "", "", fmt.Errorf("load job: %w", err)
	}
	return instanceID, stepExecID, nil
}

func (s *InstanceStore) GetStepExecutionStepIDByID(ctx context.Context, stepExecID string) (string, error) {
	var stepID string
	if err := s.pool.QueryRow(ctx,
		`SELECT step_id FROM step_execution WHERE id = $1`,
		stepExecID,
	).Scan(&stepID); err != nil {
		return "", fmt.Errorf("load step_execution: %w", err)
	}
	return stepID, nil
}

// ─── transactional writes ─────────────────────────────────────────────────────

func (s *InstanceStore) InsertInstance(ctx context.Context, tx db.Tx,
	id, defID string, defVersion int, status instance.InstanceStatus,
	currentStepIDs []string, variables []byte, businessKey *string,
) (*instance.WorkflowInstance, error) {
	pg := Unwrap(tx)
	var bk any
	if businessKey != nil {
		bk = *businessKey
	}
	var inst instance.WorkflowInstance
	var startedAt time.Time
	err := pg.QueryRow(ctx,
		`INSERT INTO workflow_instance
		   (id, definition_id, definition_version, status, current_step_ids, variables, business_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, definition_id, definition_version, status, current_step_ids, variables, started_at, business_key`,
		id, defID, defVersion, string(status), currentStepIDs, variables, bk,
	).Scan(
		&inst.ID, &inst.DefinitionID, &inst.DefinitionVersion, &inst.Status,
		&inst.CurrentStepIDs, &inst.Variables, &startedAt, &inst.BusinessKey,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "uniq_workflow_instance_bk_def_active" {
			return nil, instance.ErrBusinessKeyConflict
		}
		return nil, fmt.Errorf("insert instance: %w", err)
	}
	inst.StartedAt = startedAt
	return &inst, nil
}

func (s *InstanceStore) GetInstanceForUpdate(ctx context.Context, tx db.Tx, instanceID string) (*instance.WorkflowInstance, error) {
	pg := Unwrap(tx)
	var inst instance.WorkflowInstance
	err := ObserveLockWait("instance.for_update", func() error {
		return pg.QueryRow(ctx,
			`SELECT id, definition_id, definition_version, status, current_step_ids, variables, started_at, business_key
			   FROM workflow_instance WHERE id = $1 FOR UPDATE`,
			instanceID,
		).Scan(&inst.ID, &inst.DefinitionID, &inst.DefinitionVersion, &inst.Status,
			&inst.CurrentStepIDs, &inst.Variables, &inst.StartedAt, &inst.BusinessKey)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, instance.ErrInstanceNotFound
		}
		return nil, fmt.Errorf("get instance for update: %w", err)
	}
	return &inst, nil
}

func (s *InstanceStore) UpdateInstanceCurrentSteps(ctx context.Context, tx db.Tx, instanceID string, stepIDs []string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE workflow_instance SET current_step_ids = $1 WHERE id = $2`,
		stepIDs, instanceID,
	)
	if err != nil {
		return fmt.Errorf("update current steps: %w", err)
	}
	return nil
}

func (s *InstanceStore) UpdateInstanceStatus(ctx context.Context, tx db.Tx, instanceID string, status instance.InstanceStatus) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE workflow_instance SET status = $1 WHERE id = $2`,
		string(status), instanceID,
	)
	if err != nil {
		return fmt.Errorf("update instance status: %w", err)
	}
	return nil
}

func (s *InstanceStore) UpdateInstanceStatusAndSteps(ctx context.Context, tx db.Tx, instanceID string, status instance.InstanceStatus, stepIDs []string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE workflow_instance SET current_step_ids = $1, status = $2 WHERE id = $3`,
		stepIDs, string(status), instanceID,
	)
	if err != nil {
		return fmt.Errorf("update instance status+steps: %w", err)
	}
	return nil
}

func (s *InstanceStore) CompleteInstance(ctx context.Context, tx db.Tx, instanceID string, stepIDs []string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE workflow_instance SET status = 'COMPLETED', completed_at = now(), current_step_ids = $1 WHERE id = $2`,
		stepIDs, instanceID,
	)
	if err != nil {
		return fmt.Errorf("complete instance: %w", err)
	}
	return nil
}

func (s *InstanceStore) FailInstance(ctx context.Context, tx db.Tx, instanceID, reason string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE workflow_instance SET status = 'FAILED', failure_reason = $1, completed_at = now() WHERE id = $2`,
		reason, instanceID,
	)
	if err != nil {
		return fmt.Errorf("fail instance: %w", err)
	}
	return nil
}

func (s *InstanceStore) UpdateInstanceVariablesPartial(ctx context.Context, tx db.Tx, instanceID string, patch map[string]any) error {
	if len(patch) == 0 {
		return nil
	}

	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}

	sql := `
        UPDATE workflow_instance 
        SET variables = (
            CASE WHEN jsonb_typeof(variables) = 'object' THEN variables ELSE '{}'::jsonb END
        ) || $1::jsonb 
        WHERE id = $2
    `

	if _, err := Unwrap(tx).Exec(ctx, sql, string(patchJSON), instanceID); err != nil {
		return fmt.Errorf("update variables (partial): %w", err)
	}

	return nil
}

func (s *InstanceStore) CountStepAttempts(ctx context.Context, tx db.Tx, instanceID, stepID string) (int, error) {
	var n int
	err := Unwrap(tx).QueryRow(ctx,
		`SELECT COUNT(*) FROM step_execution WHERE instance_id = $1 AND step_id = $2`,
		instanceID, stepID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count step attempts: %w", err)
	}
	return n, nil
}

func (s *InstanceStore) InsertStepExecution(ctx context.Context, tx db.Tx,
	id, instanceID, stepID, stepType string, attempt int, inputSnapshot []byte,
) error {
	_, err := Unwrap(tx).Exec(ctx,
		`INSERT INTO step_execution (id, instance_id, step_id, step_type, attempt_number, status, input_snapshot)
		 VALUES ($1, $2, $3, $4, $5, 'RUNNING', $6)`,
		id, instanceID, stepID, stepType, attempt, inputSnapshot,
	)
	if err != nil {
		return fmt.Errorf("insert step_execution: %w", err)
	}
	return nil
}

func (s *InstanceStore) CompleteStepExecutionByID(ctx context.Context, tx db.Tx, stepExecID string, outputSnapshot []byte) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE step_execution SET status = 'COMPLETED', ended_at = now(), output_snapshot = $1 WHERE id = $2`,
		outputSnapshot, stepExecID,
	)
	if err != nil {
		return fmt.Errorf("complete step_execution by id: %w", err)
	}
	return nil
}

func (s *InstanceStore) CompleteStepExecutionByStep(ctx context.Context, tx db.Tx, instanceID, stepID string, outputSnapshot []byte) (int64, error) {
	ct, err := Unwrap(tx).Exec(ctx,
		`UPDATE step_execution
		    SET status='COMPLETED', ended_at=now(), output_snapshot=$1
		  WHERE instance_id=$2 AND step_id=$3 AND status='RUNNING'`,
		outputSnapshot, instanceID, stepID,
	)
	if err != nil {
		return 0, fmt.Errorf("complete step_execution by step: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (s *InstanceStore) CompleteStepExecutionByStepNoOutput(ctx context.Context, tx db.Tx, instanceID, stepID string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE step_execution SET status = 'COMPLETED', ended_at = now()
		  WHERE instance_id = $1 AND step_id = $2 AND status = 'RUNNING'`,
		instanceID, stepID,
	)
	if err != nil {
		return fmt.Errorf("complete step_execution by step (no output): %w", err)
	}
	return nil
}

func (s *InstanceStore) FailStepExecutionByID(ctx context.Context, tx db.Tx, stepExecID, reason string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE step_execution SET status = 'FAILED', ended_at = now(), failure_reason = $1 WHERE id = $2`,
		reason, stepExecID,
	)
	if err != nil {
		return fmt.Errorf("fail step_execution by id: %w", err)
	}
	return nil
}

func (s *InstanceStore) FailStepExecutionByStep(ctx context.Context, tx db.Tx, instanceID, stepID, reason string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE step_execution SET status = 'FAILED', ended_at = now(), failure_reason = $1
		  WHERE instance_id = $2 AND step_id = $3 AND status = 'RUNNING'`,
		reason, instanceID, stepID,
	)
	if err != nil {
		return fmt.Errorf("fail step_execution by step: %w", err)
	}
	return nil
}

func (s *InstanceStore) GetStepExecutionStepID(ctx context.Context, tx db.Tx, stepExecID string) (string, error) {
	var stepID string
	if err := Unwrap(tx).QueryRow(ctx,
		`SELECT step_id FROM step_execution WHERE id = $1`,
		stepExecID,
	).Scan(&stepID); err != nil {
		return "", fmt.Errorf("get step_execution step_id: %w", err)
	}
	return stepID, nil
}

func (s *InstanceStore) InsertJob(ctx context.Context, tx db.Tx,
	id, instanceID, stepExecID, jobType string, retriesRemaining int, payload []byte,
) error {
	_, err := Unwrap(tx).Exec(ctx,
		`INSERT INTO job (id, instance_id, step_execution_id, job_type, status, retries_remaining, payload)
		 VALUES ($1, $2, $3, $4, 'UNLOCKED', $5, $6)`,
		id, instanceID, stepExecID, jobType, retriesRemaining, payload,
	)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}
	return nil
}

func (s *InstanceStore) GetJobStatusForUpdate(ctx context.Context, tx db.Tx, jobID string) (string, error) {
	var status string
	if err := Unwrap(tx).QueryRow(ctx,
		`SELECT status FROM job WHERE id = $1 FOR UPDATE`,
		jobID,
	).Scan(&status); err != nil {
		return "", fmt.Errorf("get job status: %w", err)
	}
	return status, nil
}

func (s *InstanceStore) MarkJobCompleted(ctx context.Context, tx db.Tx, jobID, workerID string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE job SET status = 'COMPLETED', worker_id = $1 WHERE id = $2`,
		workerID, jobID,
	)
	if err != nil {
		return fmt.Errorf("mark job completed: %w", err)
	}
	return nil
}

func (s *InstanceStore) CancelJobByStepExecution(ctx context.Context, tx db.Tx, stepExecID string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE job SET status = 'CANCELLED'
		  WHERE step_execution_id = $1 AND status IN ('PENDING', 'UNLOCKED', 'LOCKED')`,
		stepExecID,
	)
	if err != nil {
		return fmt.Errorf("cancel job by step_execution: %w", err)
	}
	return nil
}

func (s *InstanceStore) InsertUserTask(ctx context.Context, tx db.Tx,
	id, instanceID, stepExecID, stepID string, payload []byte,
) error {
	_, err := Unwrap(tx).Exec(ctx,
		`INSERT INTO user_task (id, instance_id, step_execution_id, step_id, status, payload)
		 VALUES ($1, $2, $3, $4, 'OPEN', $5)`,
		id, instanceID, stepExecID, stepID, payload,
	)
	if err != nil {
		return fmt.Errorf("insert user_task: %w", err)
	}
	return nil
}

func (s *InstanceStore) CompleteUserTask(ctx context.Context, tx db.Tx, instanceID, stepID string, result []byte) (int64, error) {
	ct, err := Unwrap(tx).Exec(ctx,
		`UPDATE user_task
		    SET status='COMPLETED', result=$1, completed_at=now()
		  WHERE instance_id=$2 AND step_id=$3 AND status='OPEN'`,
		result, instanceID, stepID,
	)
	if err != nil {
		return 0, fmt.Errorf("complete user_task: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (s *InstanceStore) CancelUserTaskByStepExecution(ctx context.Context, tx db.Tx, stepExecID string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE user_task SET status='CANCELLED', completed_at=now()
		  WHERE step_execution_id = $1 AND status='OPEN'`,
		stepExecID,
	)
	if err != nil {
		return fmt.Errorf("cancel user_task by step_execution: %w", err)
	}
	return nil
}

func (s *InstanceStore) InsertBoundaryEventSchedule(ctx context.Context, tx db.Tx,
	id, instanceID, stepExecID, targetStepID string,
	fireAt time.Time, interrupting bool,
) error {
	_, err := Unwrap(tx).Exec(ctx,
		`INSERT INTO boundary_event_schedule (id, instance_id, step_execution_id, target_step_id, fire_at, interrupting)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		id, instanceID, stepExecID, targetStepID, fireAt, interrupting,
	)
	if err != nil {
		return fmt.Errorf("insert boundary_event_schedule: %w", err)
	}
	return nil
}

func (s *InstanceStore) CountCompletedBranchLeafs(ctx context.Context, tx db.Tx, instanceID string, leafStepIDs []string) (int, error) {
	var n int
	if err := Unwrap(tx).QueryRow(ctx,
		`SELECT COUNT(DISTINCT step_id) FROM step_execution
		  WHERE instance_id = $1 AND step_id = ANY($2) AND status = 'COMPLETED'`,
		instanceID, leafStepIDs,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count completed branch leafs: %w", err)
	}
	return n, nil
}

func (s *InstanceStore) GetLatestStepExecutionStatus(ctx context.Context, tx db.Tx, instanceID, stepID string) (instance.StepExecutionStatus, error) {
	var status string
	err := Unwrap(tx).QueryRow(ctx,
		`SELECT status FROM step_execution
		  WHERE instance_id = $1 AND step_id = $2
		  ORDER BY attempt_number DESC
		  LIMIT 1`,
		instanceID, stepID,
	).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", instance.ErrStepNotRetryable
		}
		return "", fmt.Errorf("get latest step_execution status: %w", err)
	}
	return instance.StepExecutionStatus(status), nil
}

func (s *InstanceStore) ReactivateInstance(ctx context.Context, tx db.Tx, instanceID string) error {
	_, err := Unwrap(tx).Exec(ctx,
		`UPDATE workflow_instance
		    SET status = 'ACTIVE', failure_reason = NULL, completed_at = NULL
		  WHERE id = $1`,
		instanceID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "uniq_workflow_instance_bk_def_active" {
			return instance.ErrBusinessKeyConflict
		}
		return fmt.Errorf("reactivate instance: %w", err)
	}
	return nil
}

// Compile-time interface assertion.
var _ instance.Store = (*InstanceStore)(nil)
