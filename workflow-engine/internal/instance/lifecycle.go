// Package instance manages the runtime lifecycle of workflow instances.
// All state mutations happen inside PostgreSQL transactions so observers never see partial state.
package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	defrepo "github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/id"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
	pgstore "github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

// Service manages workflow instance lifecycle.
type Service struct {
	pool       *pgxpool.Pool
	defRepo    *defrepo.Repository
	dispatcher dispatch.Dispatcher
}

// Dispatcher returns the configured dispatcher.
func (s *Service) Dispatcher() dispatch.Dispatcher {
	return s.dispatcher
}

// NewService creates a Service backed by pool, defRepo, and dispatcher.
// The dispatcher is invoked on every SERVICE_TASK job insert inside the same transaction.
// In polling mode it is a no-op; in kafka_outbox
// mode it writes a dispatch_outbox row.
func NewService(pool *pgxpool.Pool, defRepo *defrepo.Repository, dispatcher dispatch.Dispatcher) *Service {
	return &Service{pool: pool, defRepo: defRepo, dispatcher: dispatcher}
}

// Start creates a new workflow instance for the given definition, seeds
// variables, and dispatches the first step.
func (s *Service) Start(ctx context.Context, definitionID string, definitionVersion int, variables map[string]any, businessKey string) (*WorkflowInstance, error) {
	// Load the definition
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
	// later partial updates (US2) would fail with SQLSTATE 22023.
	if variables == nil {
		variables = map[string]any{}
	}
	varJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, fmt.Errorf("start: marshal variables: %w", err)
	}

	instanceID := id.NewInstance()
	firstStep := def.Steps[0].ID

	var inst WorkflowInstance
	err = pgstore.RunInTx(ctx, s.pool, "instance.start", pgx.TxOptions{}, func(tx pgx.Tx) error {
		var bk any
		if businessKey != "" {
			bk = businessKey
		}

		var startedAt time.Time
		err = tx.QueryRow(ctx,
			`INSERT INTO workflow_instance
			  (id, definition_id, definition_version, status, current_step_ids, variables, business_key)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING id, definition_id, definition_version, status, current_step_ids, variables, started_at, business_key`,
			instanceID, def.ID, def.Version, string(InstanceStatusActive),
			[]string{firstStep}, varJSON, bk,
		).Scan(
			&inst.ID, &inst.DefinitionID, &inst.DefinitionVersion, &inst.Status,
			&inst.CurrentStepIDs, &inst.Variables, &startedAt, &inst.BusinessKey,
		)
		inst.StartedAt = startedAt
		if err != nil {
			return fmt.Errorf("insert instance: %w", err)
		}

		// Dispatch the first step
		return s.dispatchStep(ctx, tx, &inst, def, firstStep)
	})
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	return &inst, nil
}

// CompleteJobAndAdvance marks a SERVICE_TASK job as COMPLETED, merges variablesToSet
// into the instance variables, and dispatches the step's nextStep.
// This is the normal execution path when a worker calls CompleteJob via REST/gRPC.
func (s *Service) CompleteJobAndAdvance(ctx context.Context, jobID, workerID string, variablesToSet map[string]any) error {
	// Load the job row to resolve instance + step information
	var instanceID, stepExecID string
	if err := s.pool.QueryRow(ctx,
		`SELECT instance_id, step_execution_id FROM job WHERE id = $1`,
		jobID,
	).Scan(&instanceID, &stepExecID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("job %q not found", jobID)
		}
		return fmt.Errorf("load job: %w", err)
	}

	// Load the completed step's ID from step_execution
	var completedStepID string
	if err := s.pool.QueryRow(ctx,
		`SELECT step_id FROM step_execution WHERE id = $1`,
		stepExecID,
	).Scan(&completedStepID); err != nil {
		return fmt.Errorf("load step_execution: %w", err)
	}

	// Pre-tx: peek the instance's definition_version (immutable per-instance) and
	// load the definition + resolve the completed step — all outside the
	// FOR UPDATE window, so the hot-path lock is held strictly for the state
	// transition write (US1 narrowing).
	var instDefID string
	var instDefVersion int
	if err := s.pool.QueryRow(ctx,
		`SELECT definition_id, definition_version FROM workflow_instance WHERE id = $1`,
		instanceID,
	).Scan(&instDefID, &instDefVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("instance %q not found", instanceID)
		}
		return fmt.Errorf("peek instance: %w", err)
	}
	def, err := s.defRepo.GetVersion(ctx, instDefID, instDefVersion)
	if err != nil {
		return fmt.Errorf("load def: %w", err)
	}
	completedStep := findStep(def, completedStepID)
	if completedStep == nil {
		return fmt.Errorf("step %q not found in definition", completedStepID)
	}

	return pgstore.RunInTx(ctx, s.pool, "instance.complete_job", pgx.TxOptions{}, func(tx pgx.Tx) error {
		// Check idempotency
		var status string
		_ = tx.QueryRow(ctx, `SELECT status FROM job WHERE id = $1`, jobID).Scan(&status)
		if status == "COMPLETED" {
			return nil
		}

		// Mark job and step_execution COMPLETED
		if _, err := tx.Exec(ctx,
			`UPDATE job SET status = 'COMPLETED', worker_id = $1 WHERE id = $2`,
			workerID, jobID,
		); err != nil {
			return fmt.Errorf("mark job completed: %w", err)
		}
		outputJSON, _ := json.Marshal(variablesToSet)
		if _, err := tx.Exec(ctx,
			`UPDATE step_execution SET status = 'COMPLETED', ended_at = now(), output_snapshot = $1 WHERE id = $2`,
			outputJSON, stepExecID,
		); err != nil {
			return fmt.Errorf("mark step_execution completed: %w", err)
		}

		// Load instance
		var inst WorkflowInstance
		if err := pgstore.ObserveLockWait("instance.for_update", func() error {
			return tx.QueryRow(ctx,
				`SELECT id, definition_id, definition_version, status, current_step_ids, variables, started_at
				  FROM workflow_instance WHERE id = $1 FOR UPDATE`,
				instanceID,
			).Scan(&inst.ID, &inst.DefinitionID, &inst.DefinitionVersion, &inst.Status,
				&inst.CurrentStepIDs, &inst.Variables, &inst.StartedAt)
		}); err != nil {
			return fmt.Errorf("load instance: %w", err)
		}
		if inst.Status == InstanceStatusCompleted || inst.Status == InstanceStatusFailed || inst.Status == InstanceStatusCancelled {
			return nil // already terminal
		}

		// Merge variables in memory + partial jsonb_set on the DB (US2).
		if len(variablesToSet) > 0 {
			merged, err := mergeVariables(inst.Variables, variablesToSet)
			if err != nil {
				return err
			}
			inst.Variables = merged
			if err := updateInstanceVariablesPartial(ctx, tx, inst.ID, variablesToSet); err != nil {
				return err
			}
		}

		// Backward-compat: a SERVICE_TASK with no nextStep and no conditional
		// branches ends the local branch without cleanup — preserve the
		// pre-refactor behavior of CompleteJobAndAdvance for such workflows.
		if completedStep.NextStep == "" && len(completedStep.ConditionalNextSteps) == 0 {
			return nil
		}
		return s.advancePastStep(ctx, tx, &inst, def, completedStep)
	})
}

// DispatchBoundaryStep routes an instance to targetStepID from a non-interrupting
// TIMER boundary event (called by the boundary sweeper).
func (s *Service) DispatchBoundaryStep(ctx context.Context, instanceID, targetStepID string) error {
	return pgstore.RunInTx(ctx, s.pool, "instance.dispatch_boundary", pgx.TxOptions{}, func(tx pgx.Tx) error {
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
			return fmt.Errorf("dispatch boundary: load instance: %w", err)
		}
		def, err := s.defRepo.GetVersion(ctx, inst.DefinitionID, inst.DefinitionVersion)
		if err != nil {
			return fmt.Errorf("dispatch boundary: load def: %w", err)
		}
		// Non-interrupting: spawn the target step alongside current work
		return s.dispatchStep(ctx, tx, &inst, def, targetStepID)
	})
}

// Get returns the current state of an instance.
func (s *Service) Get(ctx context.Context, instanceID string) (*WorkflowInstance, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, definition_id, definition_version, status, current_step_ids, variables,
		        started_at, completed_at, failure_reason, business_key
		  FROM workflow_instance WHERE id = $1`,
		instanceID,
	)
	var inst WorkflowInstance
	if err := row.Scan(
		&inst.ID, &inst.DefinitionID, &inst.DefinitionVersion, &inst.Status,
		&inst.CurrentStepIDs, &inst.Variables, &inst.StartedAt, &inst.CompletedAt,
		&inst.FailureReason, &inst.BusinessKey,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("instance %q not found", instanceID)
		}
		return nil, fmt.Errorf("get instance: %w", err)
	}
	return &inst, nil
}

// Cancel transitions an instance to CANCELLED.
func (s *Service) Cancel(ctx context.Context, instanceID, reason string) (*WorkflowInstance, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE workflow_instance
		  SET status = $1, failure_reason = $2, completed_at = now()
		  WHERE id = $3 AND status IN ('ACTIVE','WAITING')
		  RETURNING id, definition_id, definition_version, status, current_step_ids, variables, started_at, failure_reason`,
		string(InstanceStatusCancelled), reason, instanceID,
	)
	var inst WorkflowInstance
	if err := row.Scan(
		&inst.ID, &inst.DefinitionID, &inst.DefinitionVersion, &inst.Status,
		&inst.CurrentStepIDs, &inst.Variables, &inst.StartedAt, &inst.FailureReason,
	); err != nil {
		return nil, fmt.Errorf("cancel: %w", err)
	}
	return &inst, nil
}

// GetHistory returns all step executions for an instance ordered by start time.
func (s *Service) GetHistory(ctx context.Context, instanceID string) ([]StepExecution, error) {
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

	var execs []StepExecution
	for rows.Next() {
		var se StepExecution
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

// ── internal step dispatch ────────────────────────────────────────────────────

// dispatchStep creates a step_execution row and routes to the appropriate handler.
// Called within a transaction.
func (s *Service) dispatchStep(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, stepID string) error {
	step := findStep(def, stepID)
	if step == nil {
		return fmt.Errorf("step %q not found in definition", stepID)
	}

	// Attempt number: count existing executions for this step
	var prevAttempts int
	_ = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM step_execution WHERE instance_id = $1 AND step_id = $2`,
		inst.ID, stepID,
	).Scan(&prevAttempts)
	attempt := prevAttempts + 1

	seID := id.NewStepExecution()
	if _, err := tx.Exec(ctx,
		`INSERT INTO step_execution (id, instance_id, step_id, step_type, attempt_number, status, input_snapshot)
		 VALUES ($1, $2, $3, $4, $5, 'RUNNING', $6)`,
		seID, inst.ID, stepID, string(step.Type), attempt, inst.Variables,
	); err != nil {
		return fmt.Errorf("create step_execution for %q: %w", stepID, err)
	}

	// Add to current_step_ids if not already present
	addToCurrentSteps(inst, stepID)
	if _, err := tx.Exec(ctx,
		`UPDATE workflow_instance SET current_step_ids = $1 WHERE id = $2`,
		inst.CurrentStepIDs, inst.ID,
	); err != nil {
		return fmt.Errorf("update current steps: %w", err)
	}

	// One log line per step entry — single chokepoint covers all workflow
	// activity (service tasks, user tasks, waits, decisions, gateways, end).
	obs.FromContext(ctx).LogAttrs(ctx, slog.LevelInfo, "step dispatched",
		slog.String("instance_id", inst.ID),
		slog.String("definition_id", def.ID),
		slog.Int("definition_version", def.Version),
		slog.String("step_id", stepID),
		slog.String("step_type", string(step.Type)),
		slog.String("step_execution_id", seID),
		slog.Int("attempt", attempt),
	)

	// Route to step-type handler
	return s.routeStep(ctx, tx, inst, def, step, seID, attempt)
}

// routeStep dispatches the step to its type handler.
func (s *Service) routeStep(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep, seID string, attempt int) error {
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

	default:
		return fmt.Errorf("unsupported step type %q", step.Type)
	}
}

// ── step type handlers ────────────────────────────────────────────────────────

func (s *Service) handleServiceTask(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, step *definition.WorkflowStep, seID string, attempt int) error {
	jobType := step.JobType
	if jobType == "" {
		jobType = step.ID // R-010
	}
	retryCount := step.RetryCount
	jobID := id.NewJob()
	_, err := tx.Exec(ctx,
		`INSERT INTO job (id, instance_id, step_execution_id, job_type, status, retries_remaining, payload)
		 VALUES ($1, $2, $3, $4, 'UNLOCKED', $5, $6)`,
		jobID, inst.ID, seID, jobType, retryCount, inst.Variables,
	)
	if err != nil {
		return fmt.Errorf("create job for step %q: %w", step.ID, err)
	}
	// Hand the new job to the configured dispatcher inside the same tx
	// (FR-002, INV-1). Polling mode: no-op. kafka_outbox mode: writes a
	// dispatch_outbox row so the relay can publish it once the tx commits.
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
	// Schedule boundary events (TIMER)
	return s.scheduleBoundaryEvents(ctx, tx, inst, step, seID)
}

func (s *Service) handleUserTask(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, step *definition.WorkflowStep, seID string) error {
	utID := id.NewUserTask()
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_task (id, instance_id, step_execution_id, step_id, status, payload)
		 VALUES ($1, $2, $3, $4, 'OPEN', $5)`,
		utID, inst.ID, seID, step.ID, inst.Variables,
	); err != nil {
		return fmt.Errorf("create user_task for step %q: %w", step.ID, err)
	}
	// Transition instance to WAITING
	if _, err := tx.Exec(ctx,
		`UPDATE workflow_instance SET status = 'WAITING' WHERE id = $1`,
		inst.ID,
	); err != nil {
		return err
	}
	inst.Status = InstanceStatusWaiting
	return s.scheduleBoundaryEvents(ctx, tx, inst, step, seID)
}

func (s *Service) handleDecision(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep) error {
	vars := variablesToMap(inst.Variables)

	// Evaluate in declaration order
	for expr, target := range step.ConditionalNextSteps {
		result, err := evaluateExpr(expr, vars)
		if err != nil {
			return s.failInstance(ctx, tx, inst, step.ID, fmt.Sprintf("expression eval error: %v", err))
		}
		matched, ok := result.(bool)
		if !ok {
			return s.failInstance(ctx, tx, inst, step.ID, fmt.Sprintf("expression %q: result is %T, not bool", expr, result))
		}
		if matched {
			// Complete current step execution
			if _, err := tx.Exec(ctx,
				`UPDATE step_execution SET status = 'COMPLETED', ended_at = now()
				  WHERE instance_id = $1 AND step_id = $2 AND status = 'RUNNING'`,
				inst.ID, step.ID,
			); err != nil {
				return err
			}
			removeFromCurrentSteps(inst, step.ID)
			return s.dispatchStep(ctx, tx, inst, def, target)
		}
	}
	return s.failInstance(ctx, tx, inst, step.ID, "no conditionalNextSteps branch matched (DecisionNoBranchMatched)")
}

func (s *Service) handleTransformation(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep, seID string) error {
	vars := variablesToMap(inst.Variables)
	delta := make(map[string]any, len(step.Transformations))

	for k, rawVal := range step.Transformations {
		var v any
		if err := json.Unmarshal(rawVal, &v); err != nil {
			return fmt.Errorf("transformation %q: unmarshal value: %w", k, err)
		}
		// Resolve ${...} expressions
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

	// Persist only the changed keys via jsonb_set (US2). step_execution still
	// receives the full merged snapshot for audit fidelity.
	if err := updateInstanceVariablesPartial(ctx, tx, inst.ID, delta); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE step_execution SET status = 'COMPLETED', ended_at = now(), output_snapshot = $1
		  WHERE id = $2`,
		merged, seID,
	); err != nil {
		return err
	}

	removeFromCurrentSteps(inst, step.ID)
	return s.dispatchStep(ctx, tx, inst, def, step.NextStep)
}

func (s *Service) handleWait(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, step *definition.WorkflowStep, seID string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE workflow_instance SET status = 'WAITING' WHERE id = $1`,
		inst.ID,
	); err != nil {
		return err
	}
	inst.Status = InstanceStatusWaiting
	return s.scheduleBoundaryEvents(ctx, tx, inst, step, seID)
}

func (s *Service) handleParallelGateway(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep) error {
	// Complete the gateway step execution
	if _, err := tx.Exec(ctx,
		`UPDATE step_execution SET status = 'COMPLETED', ended_at = now()
		  WHERE instance_id = $1 AND step_id = $2 AND status = 'RUNNING'`,
		inst.ID, step.ID,
	); err != nil {
		return err
	}
	removeFromCurrentSteps(inst, step.ID)

	// Spawn each branch
	for _, branchID := range step.ParallelNextSteps {
		if err := s.dispatchStep(ctx, tx, inst, def, branchID); err != nil {
			return fmt.Errorf("parallel branch %q: %w", branchID, err)
		}
	}
	return nil
}

func (s *Service) handleJoinGateway(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep, seID string) error {
	// Find the matching PARALLEL_GATEWAY to determine expected branch count
	pgStep := findParallelGatewayFor(def, step.ID)
	if pgStep == nil {
		return fmt.Errorf("join step %q: no matching PARALLEL_GATEWAY found", step.ID)
	}
	expectedBranches := len(pgStep.ParallelNextSteps)

	// Count completed branches that have reached this join step
	var arrivedBranches int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT step_id) FROM step_execution
		  WHERE instance_id = $1 AND step_id = ANY($2) AND status = 'COMPLETED'`,
		inst.ID, branchLeafsFor(pgStep),
	).Scan(&arrivedBranches); err != nil {
		return fmt.Errorf("join count branches: %w", err)
	}

	// Mark this arrival
	if _, err := tx.Exec(ctx,
		`UPDATE step_execution SET status = 'COMPLETED', ended_at = now()
		  WHERE id = $1`,
		seID,
	); err != nil {
		return err
	}
	arrivedBranches++ // count this arrival too

	if arrivedBranches < expectedBranches {
		// Not all branches done yet; stay waiting
		return nil
	}

	// All branches arrived — advance past the join
	removeFromCurrentSteps(inst, step.ID)
	return s.dispatchStep(ctx, tx, inst, def, step.NextStep)
}

func (s *Service) handleEnd(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep, seID string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE step_execution SET status = 'COMPLETED', ended_at = now() WHERE id = $1`,
		seID,
	); err != nil {
		return err
	}
	removeFromCurrentSteps(inst, step.ID)

	// Mark instance COMPLETED
	if _, err := tx.Exec(ctx,
		`UPDATE workflow_instance SET status = 'COMPLETED', completed_at = now(), current_step_ids = $1 WHERE id = $2`,
		inst.CurrentStepIDs, inst.ID,
	); err != nil {
		return err
	}
	inst.Status = InstanceStatusCompleted

	// autoStartNextWorkflow
	if def.AutoStartNextWorkflow && def.NextWorkflowId != "" {
		go func() {
			bgCtx := context.Background()
			vars := variablesToMap(inst.Variables)
			_, _ = s.Start(bgCtx, def.NextWorkflowId, 0, vars, "")
		}()
	}
	return nil
}

func (s *Service) failInstance(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, stepID, reason string) error {
	if _, err := tx.Exec(ctx,
		`UPDATE step_execution SET status = 'FAILED', ended_at = now(), failure_reason = $1
		  WHERE instance_id = $2 AND step_id = $3 AND status = 'RUNNING'`,
		reason, inst.ID, stepID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE workflow_instance SET status = 'FAILED', failure_reason = $1, completed_at = now() WHERE id = $2`,
		reason, inst.ID,
	); err != nil {
		return err
	}
	inst.Status = InstanceStatusFailed
	return nil
}

// scheduleBoundaryEvents creates boundary_event_schedule rows for any TIMER
// events attached to step.
func (s *Service) scheduleBoundaryEvents(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, step *definition.WorkflowStep, seID string) error {
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
		if _, err := tx.Exec(ctx,
			`INSERT INTO boundary_event_schedule (id, instance_id, step_execution_id, target_step_id, fire_at, interrupting)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			besID, inst.ID, seID, be.TargetStepId, fireAt, be.Interrupting,
		); err != nil {
			return fmt.Errorf("create boundary_event_schedule: %w", err)
		}
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func findStep(def *definition.WorkflowDefinition, id string) *definition.WorkflowStep {
	for i := range def.Steps {
		if def.Steps[i].ID == id {
			return &def.Steps[i]
		}
	}
	return nil
}

func findParallelGatewayFor(def *definition.WorkflowDefinition, joinStepID string) *definition.WorkflowStep {
	for i := range def.Steps {
		if def.Steps[i].Type == definition.StepTypeParallelGateway && def.Steps[i].JoinStep == joinStepID {
			return &def.Steps[i]
		}
	}
	return nil
}

// branchLeafsFor returns the IDs of the steps immediately before the join
// (the last step in each parallel branch chain before reaching joinStep).
func branchLeafsFor(pg *definition.WorkflowStep) []string {
	return pg.ParallelNextSteps
}

func addToCurrentSteps(inst *WorkflowInstance, stepID string) {
	for _, s := range inst.CurrentStepIDs {
		if s == stepID {
			return
		}
	}
	inst.CurrentStepIDs = append(inst.CurrentStepIDs, stepID)
}

func removeFromCurrentSteps(inst *WorkflowInstance, stepID string) {
	updated := make([]string, 0, len(inst.CurrentStepIDs))
	for _, s := range inst.CurrentStepIDs {
		if s != stepID {
			updated = append(updated, s)
		}
	}
	inst.CurrentStepIDs = updated
}

// recomputeInstanceStatus inspects the remaining current_step_ids and returns
// WAITING when any remaining step is USER_TASK or WAIT (still parked awaiting
// external resume), otherwise ACTIVE.
// Callers should not invoke this when current_step_ids is empty — in that case
// the instance will either be COMPLETED via handleEnd or about to receive a
// new step via dispatchStep.
func recomputeInstanceStatus(inst *WorkflowInstance, def *definition.WorkflowDefinition) InstanceStatus {
	for _, sid := range inst.CurrentStepIDs {
		st := findStep(def, sid)
		if st == nil {
			continue
		}
		if st.Type == definition.StepTypeUserTask || st.Type == definition.StepTypeWait {
			return InstanceStatusWaiting
		}
	}
	return InstanceStatusActive
}

// advancePastStep is the shared tail used by CompleteJobAndAdvance,
// CompleteUserTaskAndAdvance, and SignalWaitAndAdvance after they have
// closed the completed step's records. It:
//  1. removes completedStepID from inst.CurrentStepIDs,
//  2. recomputes status (WAITING ↔ ACTIVE) based on remaining parked steps,
//  3. persists both columns on workflow_instance, and
//  4. dispatches the next step: conditionalNextSteps take precedence; else
//     step.NextStep; else the branch ends (no dispatch).
//
// If conditionalNextSteps is set but no branch matches, the instance is
// failed with reason DecisionNoBranchMatched — consistent with handleDecision.
func (s *Service) advancePastStep(ctx context.Context, tx pgx.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, completedStep *definition.WorkflowStep) error {
	removeFromCurrentSteps(inst, completedStep.ID)
	inst.Status = recomputeInstanceStatus(inst, def)

	if _, err := tx.Exec(ctx,
		`UPDATE workflow_instance SET current_step_ids = $1, status = $2 WHERE id = $3`,
		inst.CurrentStepIDs, string(inst.Status), inst.ID,
	); err != nil {
		return fmt.Errorf("advance: update instance: %w", err)
	}

	if len(completedStep.ConditionalNextSteps) > 0 {
		vars := variablesToMap(inst.Variables)
		for expr, target := range completedStep.ConditionalNextSteps {
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

// updateInstanceVariablesPartial writes a WAL-efficient partial update to
// workflow_instance.variables, emitting chained jsonb_set(…) calls — one per
// top-level key in patch — so only the patch values travel over the wire
// instead of the full `variables` payload.
//
// Keys are sorted for deterministic SQL output (stable prepared-statement
// cache keys). Caller is responsible for the in-memory merge if they need an
// up-to-date WorkflowInstance.Variables (typically via mergeVariables).
// Safe to call with an empty patch — returns nil without issuing any SQL.
func updateInstanceVariablesPartial(ctx context.Context, tx pgx.Tx, instanceID string, patch map[string]any) error {
	if len(patch) == 0 {
		return nil
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// The innermost operand must be a JSONB object — jsonb_set raises
	// "cannot set path in scalar" (SQLSTATE 22023) on scalars such as the
	// JSONB `null` that older instances may carry. Coerce anything that is
	// not an object to {} before applying the chained jsonb_set calls.
	expr := "CASE WHEN jsonb_typeof(variables) = 'object' THEN variables ELSE '{}'::jsonb END"
	args := make([]any, 0, len(patch)*2+1)
	for _, k := range keys {
		valJSON, err := json.Marshal(patch[k])
		if err != nil {
			return fmt.Errorf("marshal variable %q: %w", k, err)
		}
		pathIdx := len(args) + 1
		valIdx := pathIdx + 1
		expr = fmt.Sprintf("jsonb_set(%s, $%d, $%d::jsonb, true)", expr, pathIdx, valIdx)
		args = append(args, []string{k}, string(valJSON))
	}
	idIdx := len(args) + 1
	args = append(args, instanceID)

	sql := fmt.Sprintf("UPDATE workflow_instance SET variables = %s WHERE id = $%d", expr, idIdx)
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("update variables (partial): %w", err)
	}
	return nil
}

func mergeVariables(existing json.RawMessage, delta map[string]any) (json.RawMessage, error) {
	base := make(map[string]any)
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &base); err != nil {
			return nil, fmt.Errorf("merge vars: unmarshal existing: %w", err)
		}
		if base == nil {
			base = make(map[string]any)
		}
	}
	for k, v := range delta {
		base[k] = v
	}
	return json.Marshal(base)
}

func variablesToMap(raw json.RawMessage) map[string]any {
	m := make(map[string]any)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

// evaluateExpr is a thin adapter to the expression package to avoid import cycle.
// The import is done via the top-level package.
var evaluateExpr func(expr string, vars map[string]any) (any, error)

// SetExpressionEvaluator injects the expression evaluator (called from main).
func SetExpressionEvaluator(fn func(expr string, vars map[string]any) (any, error)) {
	evaluateExpr = fn
}

// parseDuration parses an ISO-8601 duration string (PT30S, PT5M, PT2H) into a time.Duration.
func parseDuration(iso string) (time.Duration, error) {
	if len(iso) < 3 || iso[0] != 'P' {
		return 0, fmt.Errorf("invalid ISO-8601 duration: %q", iso)
	}
	// Simplified parser: only handles PT<n>S, PT<n>M, PT<n>H
	rest := iso[1:] // strip 'P'
	if len(rest) > 0 && rest[0] == 'T' {
		rest = rest[1:] // strip 'T'
	}
	var total time.Duration
	i := 0
	for i < len(rest) {
		j := i
		for j < len(rest) && (rest[j] >= '0' && rest[j] <= '9' || rest[j] == '.') {
			j++
		}
		if j >= len(rest) {
			break
		}
		unit := rest[j]
		numStr := rest[i:j]
		var n float64
		fmt.Sscanf(numStr, "%f", &n)
		switch unit {
		case 'H':
			total += time.Duration(n * float64(time.Hour))
		case 'M':
			total += time.Duration(n * float64(time.Minute))
		case 'S':
			total += time.Duration(n * float64(time.Second))
		}
		i = j + 1
	}
	if total == 0 {
		return 0, fmt.Errorf("invalid or zero ISO-8601 duration: %q", iso)
	}
	return total, nil
}
