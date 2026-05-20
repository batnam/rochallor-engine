// Package instance manages the runtime lifecycle of workflow instances.
// All state mutations happen inside transactions so observers never see partial state.
package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	defrepo "github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/id"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
)

// Service manages workflow instance lifecycle.
type Service struct {
	rootCtx    context.Context
	db         db.DB
	store      Store
	defRepo    defrepo.DefinitionRepository
	dispatcher dispatch.Dispatcher
}

// Dispatcher returns the configured dispatcher.
func (s *Service) Dispatcher() dispatch.Dispatcher { return s.dispatcher }

// NewService creates a Service backed by the supplied dependencies.
//
// rootCtx is the engine's root context; it is used as the parent for any
// goroutines spawned by Service (e.g. autoStartNextWorkflow) so they are
// cancelled when the engine shuts down.
//
// The dispatcher is invoked on every SERVICE_TASK job insert inside the same
// transaction. In polling mode it is a no-op; in kafka_outbox mode it writes
// a dispatch_outbox row.
func NewService(
	rootCtx context.Context,
	dbConn db.DB,
	store Store,
	defRepo defrepo.DefinitionRepository,
	dispatcher dispatch.Dispatcher,
) *Service {
	return &Service{
		rootCtx:    rootCtx,
		db:         dbConn,
		store:      store,
		defRepo:    defRepo,
		dispatcher: dispatcher,
	}
}

// Start creates a new workflow instance for the given definition, seeds
// variables, and dispatches the first step.
func (s *Service) Start(ctx context.Context, definitionID string, definitionVersion int, variables map[string]any, businessKey string) (*WorkflowInstance, error) {
	var def *definition.WorkflowDefinition
	var err error
	if definitionVersion <= 0 {
		def, err = s.defRepo.GetLatest(ctx, definitionID)
	} else {
		def, err = s.defRepo.GetVersion(ctx, definitionID, definitionVersion)
	}
	if err != nil {
		return nil, fmt.Errorf("start: load definition %q: %w", definitionID, err)
	}
	if len(def.Steps) == 0 {
		return nil, errors.New("start: definition has no steps")
	}

	// Normalize nil/empty variables to {} so the JSONB column never holds the
	// scalar `null` — jsonb_set() refuses to set a path in a scalar and all
	// later partial updates would fail with SQLSTATE 22023.
	if variables == nil {
		variables = map[string]any{}
	}
	varJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, fmt.Errorf("start: marshal variables: %w", err)
	}

	instanceID := id.NewInstance()
	firstStep := def.Steps[0].ID

	var inst *WorkflowInstance
	err = s.db.RunInTx(ctx, "instance.start", func(tx db.Tx) error {
		var bkPtr *string
		if businessKey != "" {
			bk := businessKey
			bkPtr = &bk
		}
		var insErr error
		inst, insErr = s.store.InsertInstance(ctx, tx, instanceID, def.ID, def.Version,
			InstanceStatusActive, []string{firstStep}, varJSON, bkPtr)
		if insErr != nil {
			return insErr
		}
		return s.dispatchStep(ctx, tx, inst, def, firstStep)
	})
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	return inst, nil
}

// CompleteJobAndAdvance marks a SERVICE_TASK job COMPLETED, merges
// variablesToSet into the instance variables, and dispatches the step's
// nextStep. It is the normal execution path when a worker calls CompleteJob
// via REST/gRPC.
func (s *Service) CompleteJobAndAdvance(ctx context.Context, jobID, workerID string, variablesToSet map[string]any) error {
	instanceID, stepExecID, err := s.store.GetJobInstanceAndStepExec(ctx, jobID)
	if err != nil {
		return err
	}
	completedStepID, err := s.store.GetStepExecutionStepIDByID(ctx, stepExecID)
	if err != nil {
		return err
	}

	// Pre-tx: peek the instance's definition_version (immutable per-instance) and
	// load the definition + resolve the completed step — all outside the
	// FOR UPDATE window, so the hot-path lock is held strictly for the state
	// transition write.
	instDefID, instDefVersion, err := s.store.GetInstanceDefinitionInfo(ctx, instanceID)
	if err != nil {
		return err
	}
	def, err := s.defRepo.GetVersion(ctx, instDefID, instDefVersion)
	if err != nil {
		return fmt.Errorf("load def: %w", err)
	}
	completedStep := findStep(def, completedStepID)
	if completedStep == nil {
		return fmt.Errorf("step %q not found in definition", completedStepID)
	}

	return s.db.RunInTx(ctx, "instance.complete_job", func(tx db.Tx) error {
		// Lock the job row and check idempotency / cancellation. FOR UPDATE
		// serialises concurrent CompleteJobAndAdvance calls for the same job:
		// the second caller blocks here until the first commits, then reads
		// status=COMPLETED and short-circuits — preventing double-advance.
		status, err := s.store.GetJobStatusForUpdate(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if status == "COMPLETED" || status == "CANCELLED" {
			return nil
		}

		if err := s.store.MarkJobCompleted(ctx, tx, jobID, workerID); err != nil {
			return err
		}
		outputJSON, _ := json.Marshal(variablesToSet)
		if err := s.store.CompleteStepExecutionByID(ctx, tx, stepExecID, outputJSON); err != nil {
			return err
		}

		inst, err := s.store.GetInstanceForUpdate(ctx, tx, instanceID)
		if err != nil {
			return err
		}
		if inst.Status == InstanceStatusCompleted || inst.Status == InstanceStatusFailed || inst.Status == InstanceStatusCancelled {
			return nil // already terminal
		}

		// Merge variables in memory + partial jsonb_set on the DB.
		if len(variablesToSet) > 0 {
			merged, err := mergeVariables(inst.Variables, variablesToSet)
			if err != nil {
				return err
			}
			inst.Variables = merged
			if err := s.store.UpdateInstanceVariablesPartial(ctx, tx, inst.ID, variablesToSet); err != nil {
				return err
			}
		}

		// Backward-compat: a SERVICE_TASK with no nextStep and no conditional
		// branches ends the local branch without cleanup — preserve the
		// pre-refactor behavior of CompleteJobAndAdvance for such workflows.
		if completedStep.NextStep == "" && completedStep.ConditionalNextSteps.Len() == 0 {
			return nil
		}
		return s.advancePastStep(ctx, tx, inst, def, completedStep)
	})
}

// DispatchBoundaryStep routes an instance to targetStepID from a
// non-interrupting TIMER boundary event (called by the boundary sweeper).
func (s *Service) DispatchBoundaryStep(ctx context.Context, instanceID, stepExecutionID, targetStepID string) error {
	return s.db.RunInTx(ctx, "instance.dispatch_boundary", func(tx db.Tx) error {
		inst, err := s.store.GetInstanceForUpdate(ctx, tx, instanceID)
		if err != nil {
			return fmt.Errorf("dispatch boundary: %w", err)
		}
		if inst.Status == InstanceStatusCompleted || inst.Status == InstanceStatusFailed || inst.Status == InstanceStatusCancelled {
			return nil // already terminal — boundary event is a no-op
		}
		// Suppress firing when the parent step has already left RUNNING
		// (COMPLETED/FAILED/SKIPPED). The instance FOR UPDATE serialises this
		// read against CompleteJobAndAdvance / FailStep paths, so the status
		// reflects the committed transition.
		stepStatus, err := s.store.GetStepExecutionStatusByID(ctx, tx, stepExecutionID)
		if err != nil {
			return fmt.Errorf("dispatch boundary: %w", err)
		}
		if stepStatus != StepExecutionStatusRunning {
			return nil
		}
		def, err := s.defRepo.GetVersion(ctx, inst.DefinitionID, inst.DefinitionVersion)
		if err != nil {
			return fmt.Errorf("dispatch boundary: load def: %w", err)
		}
		// Non-interrupting: spawn the target step alongside current work.
		return s.dispatchStep(ctx, tx, inst, def, targetStepID)
	})
}

// InterruptStepAndDispatchBoundary cancels the running step (and its job),
// then dispatches targetStepID. Called by the boundary sweeper for
// interrupting=true timers.
func (s *Service) InterruptStepAndDispatchBoundary(ctx context.Context, instanceID, stepExecutionID, targetStepID string) error {
	return s.db.RunInTx(ctx, "instance.interrupt_boundary", func(tx db.Tx) error {
		inst, err := s.store.GetInstanceForUpdate(ctx, tx, instanceID)
		if err != nil {
			return fmt.Errorf("interrupt boundary: %w", err)
		}
		if inst.Status == InstanceStatusCompleted || inst.Status == InstanceStatusFailed || inst.Status == InstanceStatusCancelled {
			return nil // already terminal — boundary event is a no-op
		}

		// Suppress when the parent step has already left RUNNING — otherwise
		// the interrupting timer would FAIL a step_execution that has already
		// transitioned to COMPLETED/FAILED/SKIPPED.
		stepStatus, err := s.store.GetStepExecutionStatusByID(ctx, tx, stepExecutionID)
		if err != nil {
			return fmt.Errorf("interrupt boundary: %w", err)
		}
		if stepStatus != StepExecutionStatusRunning {
			return nil
		}

		interruptedStepID, err := s.store.GetStepExecutionStepID(ctx, tx, stepExecutionID)
		if err != nil {
			return fmt.Errorf("interrupt boundary: %w", err)
		}

		if err := s.store.FailStepExecutionByID(ctx, tx, stepExecutionID, "interrupted by boundary timer"); err != nil {
			return fmt.Errorf("interrupt boundary: cancel step_execution: %w", err)
		}

		// Cancel the pending/locked job for this step_execution so the
		// worker's eventual completeJob call is a no-op.
		if err := s.store.CancelJobByStepExecution(ctx, tx, stepExecutionID); err != nil {
			return fmt.Errorf("interrupt boundary: cancel job: %w", err)
		}

		removeFromCurrentSteps(inst, interruptedStepID)

		def, err := s.defRepo.GetVersion(ctx, inst.DefinitionID, inst.DefinitionVersion)
		if err != nil {
			return fmt.Errorf("interrupt boundary: load def: %w", err)
		}
		return s.dispatchStep(ctx, tx, inst, def, targetStepID)
	})
}

// Get returns the current state of an instance.
func (s *Service) Get(ctx context.Context, instanceID string) (*WorkflowInstance, error) {
	return s.store.GetInstance(ctx, instanceID)
}

// Cancel transitions an instance to CANCELLED.
func (s *Service) Cancel(ctx context.Context, instanceID, reason string) (*WorkflowInstance, error) {
	return s.store.CancelInstance(ctx, instanceID, reason)
}

// GetHistory returns all step executions for an instance ordered by start time.
func (s *Service) GetHistory(ctx context.Context, instanceID string) ([]StepExecution, error) {
	return s.store.GetHistory(ctx, instanceID)
}

// ListResult is the page returned by List.
type ListResult struct {
	Items []WorkflowInstance `json:"items"`
	Total int                `json:"total"`
}

// List returns a page of instances, optionally filtered by definitionId, status, and businessKey.
func (s *Service) List(ctx context.Context, definitionID, status, businessKey string, page, pageSize int) (ListResult, error) {
	return s.store.ListInstances(ctx, definitionID, status, businessKey, page, pageSize)
}

// ─── internal step dispatch ───────────────────────────────────────────────────

// dispatchStep creates a step_execution row and routes to the appropriate
// handler. Called within a transaction.
func (s *Service) dispatchStep(ctx context.Context, tx db.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, stepID string) error {
	step := findStep(def, stepID)
	if step == nil {
		return fmt.Errorf("step %q not found in definition", stepID)
	}

	prevAttempts, err := s.store.CountStepAttempts(ctx, tx, inst.ID, stepID)
	if err != nil {
		return err
	}
	attempt := prevAttempts + 1

	seID := id.NewStepExecution()
	if err := s.store.InsertStepExecution(ctx, tx, seID, inst.ID, stepID, string(step.Type), attempt, inst.Variables); err != nil {
		return fmt.Errorf("create step_execution for %q: %w", stepID, err)
	}

	// Compute the new step list, write to DB, then update in-memory only on success.
	newIDs := withStep(inst.CurrentStepIDs, stepID)
	if err := s.store.UpdateInstanceCurrentSteps(ctx, tx, inst.ID, newIDs); err != nil {
		return err
	}
	inst.CurrentStepIDs = newIDs

	// One log line per step entry — single chokepoint covers all workflow activity.
	obs.FromContext(ctx).LogAttrs(ctx, slog.LevelInfo, "step dispatched",
		slog.String("instance_id", inst.ID),
		slog.String("definition_id", def.ID),
		slog.Int("definition_version", def.Version),
		slog.String("step_id", stepID),
		slog.String("step_type", string(step.Type)),
		slog.String("step_execution_id", seID),
		slog.Int("attempt", attempt),
	)

	return s.routeStep(ctx, tx, inst, def, step, seID, attempt)
}

// routeStep dispatches the step to its type handler.
func (s *Service) routeStep(ctx context.Context, tx db.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep, seID string, attempt int) error {
	switch step.Type {
	case definition.StepTypeServiceTask:
		return s.handleServiceTask(ctx, tx, inst, step, seID, attempt)
	case definition.StepTypeUserTask:
		return s.handleUserTask(ctx, tx, inst, step, seID)
	case definition.StepTypeDecision:
		return s.handleDecision(ctx, tx, inst, def, step)
	case definition.StepTypeTransformation:
		return s.handleTransformation(ctx, tx, inst, def, step, seID)
	case definition.StepTypeWait:
		return s.handleWait(ctx, tx, inst, step, seID)
	case definition.StepTypeParallelGateway:
		return s.handleParallelGateway(ctx, tx, inst, def, step)
	case definition.StepTypeJoinGateway:
		return s.handleJoinGateway(ctx, tx, inst, def, step, seID)
	case definition.StepTypeEnd:
		return s.handleEnd(ctx, tx, inst, def, step, seID)
	case definition.StepTypeDecisionTable:
		return s.handleDecisionTable(ctx, tx, inst, def, step)
	default:
		return fmt.Errorf("unsupported step type %q", step.Type)
	}
}

// advancePastStep is the shared tail used by CompleteJobAndAdvance,
// CompleteUserTaskAndAdvance, and SignalWaitAndAdvance after they have closed
// the completed step's records.
func (s *Service) advancePastStep(ctx context.Context, tx db.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, completedStep *definition.WorkflowStep) error {
	newIDs := withoutStep(inst.CurrentStepIDs, completedStep.ID)
	newStatus := recomputeInstanceStatus(&WorkflowInstance{CurrentStepIDs: newIDs}, def)

	if err := s.store.UpdateInstanceStatusAndSteps(ctx, tx, inst.ID, newStatus, newIDs); err != nil {
		return fmt.Errorf("advance: %w", err)
	}
	inst.CurrentStepIDs = newIDs
	inst.Status = newStatus

	if completedStep.ConditionalNextSteps.Len() > 0 {
		vars, err := variablesToMap(inst.Variables)
		if err != nil {
			return s.failInstance(ctx, tx, inst, completedStep.ID, fmt.Sprintf("corrupt instance variables: %v", err))
		}
		for _, expr := range completedStep.ConditionalNextSteps.Exprs {
			target := completedStep.ConditionalNextSteps.Targets[expr]
			result, err := evaluateExpr(expr, vars)
			if err != nil {
				return s.failInstance(ctx, tx, inst, completedStep.ID, fmt.Sprintf("expression eval error: %v", err))
			}
			matched, ok := result.(bool)
			if !ok {
				return s.failInstance(ctx, tx, inst, completedStep.ID, fmt.Sprintf("expression %q: result is %T, not bool", expr, result))
			}
			if matched {
				return s.dispatchStep(ctx, tx, inst, def, target)
			}
		}
		return s.failInstance(ctx, tx, inst, completedStep.ID, "no conditionalNextSteps branch matched (DecisionNoBranchMatched)")
	}

	if completedStep.NextStep != "" {
		return s.dispatchStep(ctx, tx, inst, def, completedStep.NextStep)
	}
	return nil
}
