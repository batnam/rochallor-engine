package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	pgstore "github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

// CompleteUserTaskAndAdvance atomically completes an OPEN user_task — keyed by
// the stable (instanceID, userTaskStepID) pair — and advances the workflow.
// It is the primary external-resume entry point for USER_TASK steps.
//
// In one transaction:
//  1. Locks workflow_instance FOR UPDATE.
//  2. Validates instance is non-terminal (else ErrInstanceTerminal).
//  3. Loads the definition and verifies the step exists and is a USER_TASK
//     (else ErrStepTypeMismatch).
//  4. Conditionally UPDATEs the OPEN user_task row and the RUNNING step_execution
//     row — zero rows affected on either side ⇒ ErrUserTaskNotFound.
//  5. Merges the supplied variables into workflow_instance.variables.
//  6. Calls advancePastStep to clean up current_step_ids, recompute status, and
//     dispatch the next step (nextStep or conditionalNextSteps).
//
// Idempotency is inherited from the conditional UPDATEs: a replayed call that
// finds the user_task already COMPLETED (zero rows affected) returns
// ErrUserTaskNotFound, which the REST handler treats as 404 on second attempt.
// Callers that want "at-least-once" safety should inspect the instance state
// before retrying.
func (s *Service) CompleteUserTaskAndAdvance(ctx context.Context, instanceID, userTaskStepID, completedBy string, variables map[string]any) error {
	// Pre-tx: peek definition_version (immutable per-instance) and resolve the
	// target step so the FOR UPDATE lock is held only for the state write.
	var instDefID string
	var instDefVersion int
	if err := s.pool.QueryRow(ctx,
		`SELECT definition_id, definition_version FROM workflow_instance WHERE id = $1`,
		instanceID,
	).Scan(&instDefID, &instDefVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInstanceNotFound
		}
		return fmt.Errorf("complete user task: peek instance: %w", err)
	}
	def, err := s.defRepo.GetVersion(ctx, instDefID, instDefVersion)
	if err != nil {
		return fmt.Errorf("complete user task: load definition: %w", err)
	}
	step := findStep(def, userTaskStepID)
	if step == nil {
		return ErrUserTaskNotFound
	}
	if step.Type != definition.StepTypeUserTask {
		return ErrStepTypeMismatch
	}

	return pgstore.RunInTx(ctx, s.pool, "instance.complete_user_task", pgx.TxOptions{}, func(tx pgx.Tx) error {
		var inst WorkflowInstance
		err := pgstore.ObserveLockWait("instance.for_update", func() error {
			return tx.QueryRow(ctx,
				`SELECT id, definition_id, definition_version, status, current_step_ids, variables, started_at
				  FROM workflow_instance WHERE id = $1 FOR UPDATE`,
				instanceID,
			).Scan(&inst.ID, &inst.DefinitionID, &inst.DefinitionVersion, &inst.Status,
				&inst.CurrentStepIDs, &inst.Variables, &inst.StartedAt)
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInstanceNotFound
			}
			return fmt.Errorf("complete user task: load instance: %w", err)
		}

		if inst.Status == InstanceStatusCompleted || inst.Status == InstanceStatusFailed || inst.Status == InstanceStatusCancelled {
			return ErrInstanceTerminal
		}

		// Merge variables first so the output_snapshot below reflects the merged state.
		merged, err := mergeVariables(inst.Variables, variables)
		if err != nil {
			return fmt.Errorf("complete user task: merge variables: %w", err)
		}
		inst.Variables = merged

		// Mark the OPEN user_task row COMPLETED. The WHERE status='OPEN' clause
		// makes this a no-op on replay and guards against races with interrupting
		// boundary events that may have CANCELLED the task.
		resultJSON, err := json.Marshal(variables)
		if err != nil {
			return fmt.Errorf("complete user task: marshal result: %w", err)
		}
		ct, err := tx.Exec(ctx,
			`UPDATE user_task
			   SET status='COMPLETED', result=$1, completed_at=now()
			 WHERE instance_id=$2 AND step_id=$3 AND status='OPEN'`,
			resultJSON, instanceID, userTaskStepID,
		)
		if err != nil {
			return fmt.Errorf("complete user task: update user_task: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return ErrUserTaskNotFound
		}

		// Close the RUNNING step_execution for this step with the merged snapshot.
		if _, err := tx.Exec(ctx,
			`UPDATE step_execution
			   SET status='COMPLETED', ended_at=now(), output_snapshot=$1
			 WHERE instance_id=$2 AND step_id=$3 AND status='RUNNING'`,
			inst.Variables, instanceID, userTaskStepID,
		); err != nil {
			return fmt.Errorf("complete user task: update step_execution: %w", err)
		}

		// Persist only the supplied keys via jsonb_set (US2). If `variables` is
		// empty the helper is a no-op, preserving prior behavior.
		if err := updateInstanceVariablesPartial(ctx, tx, instanceID, variables); err != nil {
			return fmt.Errorf("complete user task: %w", err)
		}

		_ = completedBy // reserved for future audit; not persisted today

		return s.advancePastStep(ctx, tx, &inst, def, step)
	})
}
