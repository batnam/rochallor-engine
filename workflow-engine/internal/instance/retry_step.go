package instance

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
)

// RetryFailedStep manually re-runs a FAILED step on a FAILED instance.
//
// The instance is flipped from FAILED back to ACTIVE (failure_reason +
// completed_at cleared) and the target step is re-dispatched through the
// same path used by the first attempt: a fresh step_execution row is
// inserted with attempt_number incremented, and the step-type handler
// fires (for SERVICE_TASK, a new UNLOCKED job is enqueued — the previously
// FAILED job row is left untouched, so workers ignore it).
//
// If variablesPatch is non-empty, its keys are shallow-merged into the
// instance variables BEFORE re-dispatch, so the new step_execution's
// input_snapshot and (for SERVICE_TASK) the dispatched job payload observe
// the patched values. This is the supported path for fixing the bad data
// that caused the original failure without starting a brand-new instance.
//
// Preconditions enforced inside the transaction:
//   - instance exists and is in status FAILED (else ErrInstanceNotFailed)
//   - stepID is declared in the workflow definition (else error)
//   - the latest step_execution attempt for (instance, step) is FAILED
//     (else ErrStepNotRetryable — RUNNING / COMPLETED / SKIPPED rows
//     mean nothing to retry).
//
// If the partial unique index on (business_key, definition_id) WHERE
// status IN ('ACTIVE','WAITING') rejects the ACTIVE flip because another
// in-flight instance now holds the same business_key, ErrBusinessKeyConflict
// is returned.
func (s *Service) RetryFailedStep(ctx context.Context, instanceID, stepID string, variablesPatch map[string]any) (*WorkflowInstance, error) {
	if instanceID == "" || stepID == "" {
		return nil, fmt.Errorf("retry step: instance_id and step_id are required")
	}

	instDefID, instDefVersion, err := s.store.GetInstanceDefinitionInfo(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	def, err := s.defRepo.GetVersion(ctx, instDefID, instDefVersion)
	if err != nil {
		return nil, fmt.Errorf("retry step: load definition: %w", err)
	}
	if findStep(def, stepID) == nil {
		return nil, fmt.Errorf("retry step: step %q not found in definition", stepID)
	}

	var out *WorkflowInstance
	err = s.db.RunInTx(ctx, "instance.retry_step", func(tx db.Tx) error {
		inst, err := s.store.GetInstanceForUpdate(ctx, tx, instanceID)
		if err != nil {
			return err
		}
		if inst.Status != InstanceStatusFailed {
			return ErrInstanceNotFailed
		}

		latestStatus, err := s.store.GetLatestStepExecutionStatus(ctx, tx, instanceID, stepID)
		if err != nil {
			return err
		}
		if latestStatus != StepExecutionStatusFailed {
			return ErrStepNotRetryable
		}

		if err := s.store.ReactivateInstance(ctx, tx, instanceID); err != nil {
			return err
		}
		inst.Status = InstanceStatusActive
		inst.FailureReason = nil
		inst.CompletedAt = nil
		// current_step_ids may still carry the failed step id from the
		// original dispatch — dispatchStep's withStep() is a no-op for ids
		// already present, so we don't need to reset it explicitly.

		// Shallow-merge the supplied patch BEFORE dispatchStep so the new
		// step_execution.input_snapshot and the job payload (for SERVICE_TASK)
		// observe the corrected values in the same transaction.
		if len(variablesPatch) > 0 {
			merged, err := mergeVariables(inst.Variables, variablesPatch)
			if err != nil {
				return fmt.Errorf("retry step: merge variables: %w", err)
			}
			inst.Variables = merged
			if err := s.store.UpdateInstanceVariablesPartial(ctx, tx, inst.ID, variablesPatch); err != nil {
				return err
			}
		}

		obs.FromContext(ctx).LogAttrs(ctx, slog.LevelInfo, "step manual retry",
			slog.String("instance_id", inst.ID),
			slog.String("step_id", stepID),
			slog.String("definition_id", def.ID),
			slog.Int("definition_version", def.Version),
			slog.Int("vars_patch_keys", len(variablesPatch)),
		)

		if err := s.dispatchStep(ctx, tx, inst, def, stepID); err != nil {
			return fmt.Errorf("retry step: dispatch: %w", err)
		}
		out = inst
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
