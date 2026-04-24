package instance

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	pgstore "github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

// SignalWaitAndAdvance atomically resumes a workflow parked at a WAIT step.
// The caller-supplied variables (optional) are shallow-merged into
// workflow_instance.variables; the step's RUNNING step_execution row is closed;
// and the next step is dispatched via advancePastStep.
//
// Returns:
//   - ErrInstanceNotFound      — no instance with that id
//   - ErrInstanceTerminal      — instance in COMPLETED / FAILED / CANCELLED
//   - ErrWaitStepNotParked     — step id not in current_step_ids, not a WAIT step
//     in the definition, or its step_execution is not RUNNING (covers the
//     "concurrent signal lost the race" case via zero-rows-affected on UPDATE)
//   - ErrStepTypeMismatch      — caller sent the stable step id of a non-WAIT step
func (s *Service) SignalWaitAndAdvance(ctx context.Context, instanceID, waitStepID string, variables map[string]any) error {
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
		return fmt.Errorf("signal wait: peek instance: %w", err)
	}
	def, err := s.defRepo.GetVersion(ctx, instDefID, instDefVersion)
	if err != nil {
		return fmt.Errorf("signal wait: load definition: %w", err)
	}
	step := findStep(def, waitStepID)
	if step == nil {
		return ErrWaitStepNotParked
	}
	if step.Type != definition.StepTypeWait {
		return ErrStepTypeMismatch
	}

	return pgstore.RunInTx(ctx, s.pool, "instance.signal_wait", pgx.TxOptions{}, func(tx pgx.Tx) error {
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
			return fmt.Errorf("signal wait: load instance: %w", err)
		}

		if inst.Status == InstanceStatusCompleted || inst.Status == InstanceStatusFailed || inst.Status == InstanceStatusCancelled {
			return ErrInstanceTerminal
		}

		// Step must currently be parked on the instance.
		parked := false
		for _, s := range inst.CurrentStepIDs {
			if s == waitStepID {
				parked = true
				break
			}
		}
		if !parked {
			return ErrWaitStepNotParked
		}

		merged, err := mergeVariables(inst.Variables, variables)
		if err != nil {
			return fmt.Errorf("signal wait: merge variables: %w", err)
		}
		inst.Variables = merged

		// Close the RUNNING step_execution — zero rows affected means another
		// signal raced us or the step was already advanced by a boundary event.
		ct, err := tx.Exec(ctx,
			`UPDATE step_execution
			   SET status='COMPLETED', ended_at=now(), output_snapshot=$1
			 WHERE instance_id=$2 AND step_id=$3 AND status='RUNNING'`,
			inst.Variables, instanceID, waitStepID,
		)
		if err != nil {
			return fmt.Errorf("signal wait: update step_execution: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return ErrWaitStepNotParked
		}

		if err := updateInstanceVariablesPartial(ctx, tx, instanceID, variables); err != nil {
			return fmt.Errorf("signal wait: %w", err)
		}

		return s.advancePastStep(ctx, tx, &inst, def, step)
	})
}
