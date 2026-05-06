package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// CompleteUserTaskAndAdvance atomically completes an OPEN user_task — keyed
// by the stable (instanceID, userTaskStepID) pair — and advances the workflow.
// It is the primary external-resume entry point for USER_TASK steps.
func (s *Service) CompleteUserTaskAndAdvance(ctx context.Context, instanceID, userTaskStepID, completedBy string, variables map[string]any) error {
	// Pre-tx: peek definition_version (immutable per-instance) and resolve the
	// target step so the FOR UPDATE lock is held only for the state write.
	instDefID, instDefVersion, err := s.store.GetInstanceDefinitionInfo(ctx, instanceID)
	if err != nil {
		return err
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

	return s.db.RunInTx(ctx, "instance.complete_user_task", func(tx db.Tx) error {
		inst, err := s.store.GetInstanceForUpdate(ctx, tx, instanceID)
		if err != nil {
			if errors.Is(err, ErrInstanceNotFound) {
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

		resultJSON, err := json.Marshal(variables)
		if err != nil {
			return fmt.Errorf("complete user task: marshal result: %w", err)
		}
		rows, err := s.store.CompleteUserTask(ctx, tx, instanceID, userTaskStepID, resultJSON)
		if err != nil {
			return fmt.Errorf("complete user task: %w", err)
		}
		if rows == 0 {
			return ErrUserTaskNotFound
		}

		if _, err := s.store.CompleteStepExecutionByStep(ctx, tx, instanceID, userTaskStepID, inst.Variables); err != nil {
			return fmt.Errorf("complete user task: %w", err)
		}

		if err := s.store.UpdateInstanceVariablesPartial(ctx, tx, instanceID, variables); err != nil {
			return fmt.Errorf("complete user task: %w", err)
		}

		_ = completedBy // reserved for future audit; not persisted today

		return s.advancePastStep(ctx, tx, inst, def, step)
	})
}
