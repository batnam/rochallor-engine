package instance

import (
	"context"
	"errors"
	"fmt"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// SignalWaitAndAdvance atomically resumes a workflow parked at a WAIT step.
// The caller-supplied variables (optional) are shallow-merged into
// workflow_instance.variables; the step's RUNNING step_execution row is
// closed; and the next step is dispatched via advancePastStep.
//
// Returns:
//   - ErrInstanceNotFound      — no instance with that id
//   - ErrInstanceTerminal      — instance in COMPLETED / FAILED / CANCELLED
//   - ErrWaitStepNotParked     — step id not in current_step_ids, not a WAIT
//     step, or its step_execution is not RUNNING
//   - ErrStepTypeMismatch      — caller sent the stable id of a non-WAIT step
func (s *Service) SignalWaitAndAdvance(ctx context.Context, instanceID, waitStepID string, variables map[string]any) error {
	// Pre-tx: peek definition_version (immutable per-instance) and resolve
	// the target step so the FOR UPDATE lock is held only for the state write.
	instDefID, instDefVersion, err := s.store.GetInstanceDefinitionInfo(ctx, instanceID)
	if err != nil {
		return err
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

	return s.db.RunInTx(ctx, "instance.signal_wait", func(tx db.Tx) error {
		inst, err := s.store.GetInstanceForUpdate(ctx, tx, instanceID)
		if err != nil {
			if errors.Is(err, ErrInstanceNotFound) {
				return ErrInstanceNotFound
			}
			return fmt.Errorf("signal wait: load instance: %w", err)
		}

		if inst.Status == InstanceStatusCompleted || inst.Status == InstanceStatusFailed || inst.Status == InstanceStatusCancelled {
			return ErrInstanceTerminal
		}

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
		rows, err := s.store.CompleteStepExecutionByStep(ctx, tx, instanceID, waitStepID, inst.Variables)
		if err != nil {
			return fmt.Errorf("signal wait: %w", err)
		}
		if rows == 0 {
			return ErrWaitStepNotParked
		}

		if err := s.store.UpdateInstanceVariablesPartial(ctx, tx, instanceID, variables); err != nil {
			return fmt.Errorf("signal wait: %w", err)
		}

		return s.advancePastStep(ctx, tx, inst, def, step)
	})
}
