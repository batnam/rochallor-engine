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
	db         db.DB
	store      Store
	defRepo    defrepo.DefinitionRepository
	dispatcher dispatch.Dispatcher
}

// Dispatcher returns the configured dispatcher.
func (s *Service) Dispatcher() dispatch.Dispatcher { return s.dispatcher }

// NewService creates a Service backed by the supplied dependencies.
//
// Background workers receive the engine root context explicitly at startup.
//
// The dispatcher is invoked on every SERVICE_TASK job insert inside the same
// transaction. In polling mode it is a no-op; in kafka_outbox mode it writes
// a dispatch_outbox row.
func NewService(
	_ context.Context,
	dbConn db.DB,
	store Store,
	defRepo defrepo.DefinitionRepository,
	dispatcher dispatch.Dispatcher,
) *Service {
	return &Service{
		db:         dbConn,
		store:      store,
		defRepo:    defRepo,
		dispatcher: dispatcher,
	}
}

// Start creates a new workflow instance for the given definition, seeds
// variables, and dispatches the first step.
func (s *Service) Start(ctx context.Context, definitionID string, definitionVersion int, variables map[string]any, businessKey string) (*WorkflowInstance, error) {
	def, varJSON, err := s.prepareStart(ctx, definitionID, definitionVersion, variables)
	if err != nil {
		return nil, err
	}
	var inst *WorkflowInstance
	err = s.db.RunInTx(ctx, "instance.start", func(tx db.Tx) error {
		var err error
		inst, err = s.startInTx(ctx, tx, def, varJSON, businessKey)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	return inst, nil
}

func (s *Service) prepareStart(ctx context.Context, definitionID string, definitionVersion int, variables map[string]any) (*definition.WorkflowDefinition, []byte, error) {
	var def *definition.WorkflowDefinition
	var err error
	if definitionVersion <= 0 {
		def, err = s.defRepo.GetLatest(ctx, definitionID)
	} else {
		def, err = s.defRepo.GetVersion(ctx, definitionID, definitionVersion)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("start: load definition %q: %w", definitionID, err)
	}
	if len(def.Steps) == 0 {
		return nil, nil, errors.New("start: definition has no steps")
	}

	// Normalize nil/empty variables to {} so the JSONB column never holds the
	// scalar `null` — jsonb_set() refuses to set a path in a scalar and all
	// later partial updates would fail with SQLSTATE 22023.
	if variables == nil {
		variables = map[string]any{}
	}
	// If the definition declares an input schema, validate the caller-supplied
	// variables strictly (no coercion) and reject the call before any
	// persistence happens.
	if def.InputSchema != nil {
		if vs := def.InputSchema.Validate(variables); len(vs) > 0 {
			return nil, nil, &definition.SchemaViolationError{Violations: vs}
		}
	}
	varJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, nil, fmt.Errorf("start: marshal variables: %w", err)
	}

	return def, varJSON, nil
}

// startInTx also serves durable chaining so child creation and acknowledgement
// share the transaction, including jobs, timers and Kafka outbox dispatches.
func (s *Service) startInTx(ctx context.Context, tx db.Tx, def *definition.WorkflowDefinition, variables []byte, businessKey string) (*WorkflowInstance, error) {
	var bk *string
	if businessKey != "" {
		bk = &businessKey
	}
	inst, err := s.store.InsertInstance(ctx, tx, id.NewInstance(), def.ID, def.Version, InstanceStatusActive, []string{def.Steps[0].ID}, variables, bk)
	if err != nil {
		return nil, err
	}
	if err := s.dispatchStep(ctx, tx, inst, def, def.Steps[0].ID); err != nil {
		return nil, err
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

	var schemaErr *definition.SchemaViolationError

	txErr := s.db.RunInTx(ctx, "instance.complete_job", func(tx db.Tx) error {
		state, err := s.store.LockJobExecution(ctx, tx, jobID)
		if err != nil {
			return err
		}
		if !state.AcceptsCallback(workerID) {
			return nil
		}

		// If the step declares an outputs schema, validate the worker-supplied
		// variables BEFORE marking the job complete. On violation, cancel the
		// job, fail the step + instance with the violation message, and surface
		// the typed error to the caller. retry_count is intentionally bypassed:
		// schema violations are deterministic.
		if completedStep.Type == definition.StepTypeServiceTask && completedStep.OutputsSchema != nil {
			if vs := completedStep.OutputsSchema.Validate(variablesToSet); len(vs) > 0 {
				schemaErr = &definition.SchemaViolationError{Violations: vs}
				if err := s.store.CancelJobByStepExecution(ctx, tx, stepExecID); err != nil {
					return err
				}
				if err := s.store.FailStepExecutionByID(ctx, tx, stepExecID, schemaErr.Error()); err != nil {
					return err
				}
				if err := s.store.FailInstance(ctx, tx, instanceID, schemaErr.Error()); err != nil {
					return err
				}
				return nil // commit the failure state
			}
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
	if txErr != nil {
		return txErr
	}
	if schemaErr != nil {
		return schemaErr
	}
	return nil
}

// FireBoundaryEvent consumes a timer and applies its effects in one transaction.
// Locking the instance first serializes timers with callbacks and cancellation.
func (s *Service) FireBoundaryEvent(ctx context.Context, eventID, instanceID string) error {
	// Definition identity is immutable. Load it before acquiring a transaction
	// connection, so a saturated pool cannot deadlock on a nested pooled read.
	defID, version, err := s.store.GetInstanceDefinitionInfo(ctx, instanceID)
	if err != nil {
		return err
	}
	def, err := s.defRepo.GetVersion(ctx, defID, version)
	if err != nil {
		return fmt.Errorf("boundary: load definition: %w", err)
	}

	return s.db.RunInTx(ctx, "instance.fire_boundary", func(tx db.Tx) error {
		inst, err := s.store.GetInstanceForUpdate(ctx, tx, instanceID)
		if err != nil {
			return err
		}
		event, err := s.store.ClaimDueBoundaryEvent(ctx, tx, eventID, instanceID)
		if err != nil {
			return err
		}
		if event == nil {
			return nil
		}
		if inst.Status != InstanceStatusActive && inst.Status != InstanceStatusWaiting {
			return nil
		}
		stepStatus, err := s.store.GetStepExecutionStatusByID(ctx, tx, event.StepExecutionID)
		if err != nil {
			return err
		}
		if stepStatus != StepExecutionStatusRunning {
			return nil
		}
		if event.Interrupting {
			stepID, err := s.store.GetStepExecutionStepID(ctx, tx, event.StepExecutionID)
			if err != nil {
				return err
			}
			if err := s.store.FailStepExecutionByID(ctx, tx, event.StepExecutionID, "interrupted by boundary timer"); err != nil {
				return err
			}
			if err := s.store.CancelJobByStepExecution(ctx, tx, event.StepExecutionID); err != nil {
				return err
			}
			if err := s.store.CancelUserTaskByStepExecution(ctx, tx, event.StepExecutionID); err != nil {
				return err
			}
			removeFromCurrentSteps(inst, stepID)
		}
		return s.dispatchStep(ctx, tx, inst, def, event.TargetStepID)
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
