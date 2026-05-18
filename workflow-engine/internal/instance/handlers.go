package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/id"
)

// ─── step type handlers ───────────────────────────────────────────────────────

func (s *Service) handleServiceTask(ctx context.Context, tx db.Tx, inst *WorkflowInstance, step *definition.WorkflowStep, seID string, attempt int) error {
	jobType := step.JobType
	if jobType == "" {
		jobType = step.ID
	}
	retryCount := step.RetryCount
	jobID := id.NewJob()
	if err := s.store.InsertJob(ctx, tx, jobID, inst.ID, seID, jobType, retryCount, inst.Variables); err != nil {
		return fmt.Errorf("create job for step %q: %w", step.ID, err)
	}
	if err := s.dispatcher.Enqueue(ctx, tx, dispatch.DispatchJob{
		ID:               jobID,
		InstanceID:       inst.ID,
		StepExecutionID:  seID,
		JobType:          jobType,
		RetriesRemaining: retryCount,
		Payload:          []byte(inst.Variables),
		CreatedAt:        time.Now(),
	}); err != nil {
		return fmt.Errorf("dispatch enqueue for step %q: %w", step.ID, err)
	}
	return s.scheduleBoundaryEvents(ctx, tx, inst, step, seID)
}

func (s *Service) handleUserTask(ctx context.Context, tx db.Tx, inst *WorkflowInstance, step *definition.WorkflowStep, seID string) error {
	utID := id.NewUserTask()
	if err := s.store.InsertUserTask(ctx, tx, utID, inst.ID, seID, step.ID, inst.Variables); err != nil {
		return fmt.Errorf("create user_task for step %q: %w", step.ID, err)
	}
	if err := s.store.UpdateInstanceStatus(ctx, tx, inst.ID, InstanceStatusWaiting); err != nil {
		return err
	}
	inst.Status = InstanceStatusWaiting
	return s.scheduleBoundaryEvents(ctx, tx, inst, step, seID)
}

func (s *Service) handleDecision(ctx context.Context, tx db.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep) error {
	vars, err := variablesToMap(inst.Variables)
	if err != nil {
		return s.failInstance(ctx, tx, inst, step.ID, fmt.Sprintf("corrupt instance variables: %v", err))
	}
	branches := step.ConditionalNextSteps
	if branches != nil {
		for _, expr := range branches.Exprs {
			target := branches.Targets[expr]
			result, err := evaluateExpr(expr, vars)
			if err != nil {
				return s.failInstance(ctx, tx, inst, step.ID, fmt.Sprintf("expression eval error: %v", err))
			}
			matched, ok := result.(bool)
			if !ok {
				return s.failInstance(ctx, tx, inst, step.ID, fmt.Sprintf("expression %q: result is %T, not bool", expr, result))
			}
			if matched {
				if err := s.store.CompleteStepExecutionByStepNoOutput(ctx, tx, inst.ID, step.ID); err != nil {
					return err
				}
				removeFromCurrentSteps(inst, step.ID)
				return s.dispatchStep(ctx, tx, inst, def, target)
			}
		}
	}
	return s.failInstance(ctx, tx, inst, step.ID, "no conditionalNextSteps branch matched (DecisionNoBranchMatched)")
}

func (s *Service) handleTransformation(ctx context.Context, tx db.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep, seID string) error {
	vars, err := variablesToMap(inst.Variables)
	if err != nil {
		return fmt.Errorf("corrupt instance variables: %w", err)
	}
	delta := make(map[string]any, len(step.Transformations))
	for k, rawVal := range step.Transformations {
		var v any
		if err := json.Unmarshal(rawVal, &v); err != nil {
			return fmt.Errorf("transformation %q: unmarshal value: %w", k, err)
		}
		if strVal, ok := v.(string); ok && strings.HasPrefix(strVal, "${") && strings.HasSuffix(strVal, "}") {
			inner := strings.TrimSpace(strVal[2 : len(strVal)-1])
			if inner == "now()" {
				v = time.Now().UTC().Format(time.RFC3339)
			} else {
				result, err := evaluateExpr(inner, vars)
				if err != nil {
					return s.failInstance(ctx, tx, inst, step.ID, fmt.Sprintf("transformation %q: expression eval: %v", k, err))
				}
				v = result
			}
		}
		vars[k] = v
		delta[k] = v
	}

	merged, err := json.Marshal(vars)
	if err != nil {
		return fmt.Errorf("transformation: marshal merged vars: %w", err)
	}
	inst.Variables = merged

	if err := s.store.UpdateInstanceVariablesPartial(ctx, tx, inst.ID, delta); err != nil {
		return err
	}
	if err := s.store.CompleteStepExecutionByID(ctx, tx, seID, merged); err != nil {
		return err
	}

	removeFromCurrentSteps(inst, step.ID)
	return s.dispatchStep(ctx, tx, inst, def, step.NextStep)
}

func (s *Service) handleWait(ctx context.Context, tx db.Tx, inst *WorkflowInstance, step *definition.WorkflowStep, seID string) error {
	if err := s.store.UpdateInstanceStatus(ctx, tx, inst.ID, InstanceStatusWaiting); err != nil {
		return err
	}
	inst.Status = InstanceStatusWaiting
	return s.scheduleBoundaryEvents(ctx, tx, inst, step, seID)
}

func (s *Service) handleParallelGateway(ctx context.Context, tx db.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep) error {
	if err := s.store.CompleteStepExecutionByStepNoOutput(ctx, tx, inst.ID, step.ID); err != nil {
		return err
	}
	removeFromCurrentSteps(inst, step.ID)
	for _, branchID := range step.ParallelNextSteps {
		if err := s.dispatchStep(ctx, tx, inst, def, branchID); err != nil {
			return fmt.Errorf("parallel branch %q: %w", branchID, err)
		}
	}
	return nil
}

func (s *Service) handleJoinGateway(ctx context.Context, tx db.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep, seID string) error {
	pgStep := findParallelGatewayFor(def, step.ID)
	if pgStep == nil {
		return fmt.Errorf("join step %q: no matching PARALLEL_GATEWAY found", step.ID)
	}
	expectedBranches := len(pgStep.ParallelNextSteps)

	// Count completed branch leaf steps using the transaction so that the
	// current branch's step_execution — marked COMPLETED earlier in this same
	// tx by CompleteJobAndAdvance — is visible without a compensating increment.
	arrivedBranches, err := s.store.CountCompletedBranchLeafs(ctx, tx, inst.ID, branchLeafsFor(pgStep))
	if err != nil {
		return fmt.Errorf("join count branches: %w", err)
	}

	if err := s.store.CompleteStepExecutionByID(ctx, tx, seID, nil); err != nil {
		return err
	}

	if arrivedBranches < expectedBranches {
		return nil // not all branches done yet
	}

	removeFromCurrentSteps(inst, step.ID)
	return s.dispatchStep(ctx, tx, inst, def, step.NextStep)
}

func (s *Service) handleEnd(ctx context.Context, tx db.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep, seID string) error {
	if err := s.store.CompleteStepExecutionByID(ctx, tx, seID, nil); err != nil {
		return err
	}
	newIDs := withoutStep(inst.CurrentStepIDs, step.ID)
	if err := s.store.CompleteInstance(ctx, tx, inst.ID, newIDs); err != nil {
		return err
	}
	inst.CurrentStepIDs = newIDs
	inst.Status = InstanceStatusCompleted

	if def.AutoStartNextWorkflow && def.NextWorkflowId != "" {
		nextID := def.NextWorkflowId
		vars, err := variablesToMap(inst.Variables)
		if err != nil {
			slog.Error("autoStartNextWorkflow: corrupt instance variables, skipping chain",
				"instance_id", inst.ID, "next_workflow_id", nextID, "err", err)
			return nil
		}
		var bk string
		if inst.BusinessKey != nil {
			bk = *inst.BusinessKey
		}
		go func() {
			tCtx, cancel := context.WithTimeout(s.rootCtx, 30*time.Second)
			defer cancel()
			if _, err := s.Start(tCtx, nextID, 0, vars, bk); err != nil {
				slog.Error("autoStartNextWorkflow: failed to start chained workflow",
					"next_workflow_id", nextID, "business_key", bk, "err", err)
			}
		}()
	}
	return nil
}

func (s *Service) failInstance(ctx context.Context, tx db.Tx, inst *WorkflowInstance, stepID, reason string) error {
	if err := s.store.FailStepExecutionByStep(ctx, tx, inst.ID, stepID, reason); err != nil {
		return err
	}
	if err := s.store.FailInstance(ctx, tx, inst.ID, reason); err != nil {
		return err
	}
	inst.Status = InstanceStatusFailed
	return nil
}

// scheduleBoundaryEvents creates boundary_event_schedule rows for any TIMER
// events attached to step.
func (s *Service) scheduleBoundaryEvents(ctx context.Context, tx db.Tx, inst *WorkflowInstance, step *definition.WorkflowStep, seID string) error {
	for _, be := range step.BoundaryEvents {
		if be.Type != definition.BoundaryEventTypeTimer {
			continue
		}
		dur, err := parseDuration(be.Duration)
		if err != nil {
			return fmt.Errorf("boundary event duration %q: %w", be.Duration, err)
		}
		fireAt := time.Now().Add(dur)
		besID := id.NewBoundaryEvent()
		if err := s.store.InsertBoundaryEventSchedule(ctx, tx, besID, inst.ID, seID, be.TargetStepId, fireAt, be.Interrupting); err != nil {
			return fmt.Errorf("create boundary_event_schedule: %w", err)
		}
	}
	return nil
}
