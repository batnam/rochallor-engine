// Package grpc implements the gRPC WorkflowEngine service, delegating every
// RPC to the same internal packages used by the REST handlers.
package grpc

import (
	"context"
	"encoding/json"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	engineapi "github.com/batnam/rochallor-engine/workflow-engine/internal/api"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EngineServer implements workflowv1.WorkflowEngineServer.
type EngineServer struct {
	workflowv1.UnimplementedWorkflowEngineServer
	defRepo      *definition.Repository
	instSvc      *instance.Service
	pool         *pgxpool.Pool
	dispatchMode string
}

// NewEngineServer creates an EngineServer. dispatchMode is the current
// engine dispatch mode (polling / kafka_outbox); when set to kafka_outbox
// the PollJobs RPC is disabled per FR-004.
func NewEngineServer(defRepo *definition.Repository, instSvc *instance.Service, pool *pgxpool.Pool, dispatchMode string) *EngineServer {
	return &EngineServer{defRepo: defRepo, instSvc: instSvc, pool: pool, dispatchMode: dispatchMode}
}

// Register registers the server with a gRPC server instance.
func (s *EngineServer) Register(srv *grpc.Server) {
	workflowv1.RegisterWorkflowEngineServer(srv, s)
}

// ── Definitions ─────────────────────────���─────────────────────────────────────

func (s *EngineServer) UploadDefinition(ctx context.Context, req *workflowv1.UploadDefinitionRequest) (*workflowv1.UploadDefinitionResponse, error) {
	if req.Definition == nil {
		return nil, engineapi.GRPCInvalidArgument("definition is required")
	}
	def := protoDefToInternal(req.Definition)
	if err := definition.Validate(def); err != nil {
		return nil, engineapi.GRPCInvalidArgument(err.Error())
	}
	sum, err := s.defRepo.Upload(ctx, def)
	if err != nil {
		return nil, engineapi.GRPCInternal(err)
	}
	return &workflowv1.UploadDefinitionResponse{
		Id:         sum.ID,
		Version:    int32(sum.Version),
		UploadedAt: timestamppb.New(sum.UploadedAt),
	}, nil
}

func (s *EngineServer) GetDefinition(ctx context.Context, req *workflowv1.GetDefinitionRequest) (*workflowv1.GetDefinitionResponse, error) {
	var def *definition.WorkflowDefinition
	var err error
	if req.Version == 0 {
		def, err = s.defRepo.GetLatest(ctx, req.Id)
	} else {
		def, err = s.defRepo.GetVersion(ctx, req.Id, int(req.Version))
	}
	if err != nil {
		return nil, engineapi.GRPCNotFound(err.Error())
	}
	return &workflowv1.GetDefinitionResponse{Definition: internalDefToProto(def)}, nil
}

func (s *EngineServer) ListDefinitions(ctx context.Context, req *workflowv1.ListDefinitionsRequest) (*workflowv1.ListDefinitionsResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize == 0 {
		pageSize = 20
	}
	result, err := s.defRepo.List(ctx, req.Keyword, int(req.Page), pageSize)
	if err != nil {
		return nil, engineapi.GRPCInternal(err)
	}
	var defs []*workflowv1.WorkflowDefinition
	for _, sum := range result.Items {
		defs = append(defs, &workflowv1.WorkflowDefinition{
			Id:      sum.ID,
			Version: int32(sum.Version),
			Name:    sum.Name,
		})
	}
	return &workflowv1.ListDefinitionsResponse{
		Definitions: defs,
		Total:       int32(result.Total),
	}, nil
}

// ── Instances ─────────────────────────────────────────────────────────────────

func (s *EngineServer) StartInstance(ctx context.Context, req *workflowv1.StartInstanceRequest) (*workflowv1.StartInstanceResponse, error) {
	vars := structToMap(req.Variables)
	inst, err := s.instSvc.Start(ctx, req.DefinitionId, int(req.DefinitionVersion), vars, req.BusinessKey)
	if err != nil {
		return nil, engineapi.GRPCInternal(err)
	}
	return &workflowv1.StartInstanceResponse{Instance: internalInstToProto(inst)}, nil
}

func (s *EngineServer) GetInstance(ctx context.Context, req *workflowv1.GetInstanceRequest) (*workflowv1.GetInstanceResponse, error) {
	inst, err := s.instSvc.Get(ctx, req.Id)
	if err != nil {
		return nil, engineapi.GRPCNotFound(err.Error())
	}
	return &workflowv1.GetInstanceResponse{Instance: internalInstToProto(inst)}, nil
}

func (s *EngineServer) GetInstanceHistory(ctx context.Context, req *workflowv1.GetInstanceHistoryRequest) (*workflowv1.GetInstanceHistoryResponse, error) {
	hist, err := s.instSvc.GetHistory(ctx, req.Id)
	if err != nil {
		return nil, engineapi.GRPCInternal(err)
	}
	var execs []*workflowv1.StepExecution
	for _, se := range hist {
		e := &workflowv1.StepExecution{
			Id:            se.ID,
			InstanceId:    se.InstanceID,
			StepId:        se.StepID,
			StepType:      workflowv1.StepType(workflowv1.StepType_value["STEP_TYPE_"+se.StepType]),
			AttemptNumber: int32(se.AttemptNumber),
			Status:        workflowv1.StepExecutionStatus(workflowv1.StepExecutionStatus_value["STEP_EXECUTION_STATUS_"+string(se.Status)]),
			StartedAt:     timestamppb.New(se.StartedAt),
		}
		if se.EndedAt != nil {
			e.EndedAt = timestamppb.New(*se.EndedAt)
		}
		if se.FailureReason != nil {
			e.FailureReason = *se.FailureReason
		}
		execs = append(execs, e)
	}
	return &workflowv1.GetInstanceHistoryResponse{Executions: execs}, nil
}

func (s *EngineServer) CancelInstance(ctx context.Context, req *workflowv1.CancelInstanceRequest) (*workflowv1.CancelInstanceResponse, error) {
	inst, err := s.instSvc.Cancel(ctx, req.Id, req.Reason)
	if err != nil {
		return nil, engineapi.GRPCInternal(err)
	}
	return &workflowv1.CancelInstanceResponse{Instance: internalInstToProto(inst)}, nil
}

func (s *EngineServer) ListInstances(ctx context.Context, req *workflowv1.ListInstancesRequest) (*workflowv1.ListInstancesResponse, error) {
	pageSize := int(req.PageSize)
	if pageSize == 0 {
		pageSize = 20
	}
	statusStr := ""
	if req.Status != workflowv1.InstanceStatus_INSTANCE_STATUS_UNSPECIFIED {
		statusStr = req.Status.String()[len("INSTANCE_STATUS_"):]
	}
	result, err := s.instSvc.List(ctx, req.DefinitionId, statusStr, req.BusinessKey, int(req.Page), pageSize)
	if err != nil {
		return nil, engineapi.GRPCInternal(err)
	}
	var insts []*workflowv1.WorkflowInstance
	for i := range result.Items {
		insts = append(insts, internalInstToProto(&result.Items[i]))
	}
	return &workflowv1.ListInstancesResponse{
		Instances: insts,
		Total:     int32(result.Total),
	}, nil
}

// ── Jobs ──────────────────────────────────────────────────────────────��───────

func (s *EngineServer) PollJobs(ctx context.Context, req *workflowv1.PollJobsRequest) (*workflowv1.PollJobsResponse, error) {
	// In event-driven mode the poll path is shut off (FR-004, R-005).
	// Returning FailedPrecondition is a loud, actionable signal for operators
	// who flip the switch but forget to update their workers.
	if s.dispatchMode == "kafka_outbox" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"engine runs in kafka_outbox dispatch mode; the poll path is disabled. Workers must consume directly from Kafka.")
	}
	max := int(req.MaxJobs)
	if max <= 0 {
		max = 1
	}
	jobs, err := job.Poll(ctx, s.pool, req.WorkerId, req.JobTypes, max)
	if err != nil {
		return nil, engineapi.GRPCInternal(err)
	}
	var pjobs []*workflowv1.Job
	for _, j := range jobs {
		pj := &workflowv1.Job{
			Id:               j.ID,
			JobType:          j.JobType,
			InstanceId:       j.InstanceID,
			StepExecutionId:  j.StepExecutionID,
			RetriesRemaining: int32(j.RetriesRemaining),
		}
		if j.LockExpiresAt != nil {
			pj.LockExpiresAt = timestamppb.New(*j.LockExpiresAt)
		}
		pjobs = append(pjobs, pj)
	}
	return &workflowv1.PollJobsResponse{Jobs: pjobs}, nil
}

func (s *EngineServer) CompleteJob(ctx context.Context, req *workflowv1.CompleteJobRequest) (*workflowv1.CompleteJobResponse, error) {
	vars := structToMap(req.VariablesToSet)
	if err := s.instSvc.CompleteJobAndAdvance(ctx, req.JobId, req.WorkerId, vars); err != nil {
		return nil, engineapi.GRPCInternal(err)
	}
	return &workflowv1.CompleteJobResponse{}, nil
}

func (s *EngineServer) FailJob(ctx context.Context, req *workflowv1.FailJobRequest) (*workflowv1.FailJobResponse, error) {
	if err := job.Fail(ctx, s.pool, s.instSvc.Dispatcher(), req.JobId, req.WorkerId, req.ErrorMessage, req.Retryable); err != nil {
		return nil, engineapi.GRPCInternal(err)
	}
	return &workflowv1.FailJobResponse{}, nil
}

// ── User tasks ──────────────────────────────────────────────────────────���─────

func (s *EngineServer) CompleteUserTask(ctx context.Context, req *workflowv1.CompleteUserTaskRequest) (*workflowv1.CompleteUserTaskResponse, error) {
	if req.InstanceId == "" || req.UserTaskStepId == "" {
		return nil, engineapi.GRPCInvalidArgument("instance_id and user_task_step_id are required")
	}
	vars := structToMap(req.Result)
	if err := s.instSvc.CompleteUserTaskAndAdvance(ctx, req.InstanceId, req.UserTaskStepId, req.CompletedBy, vars); err != nil {
		return nil, mapResumeErr(err)
	}
	return &workflowv1.CompleteUserTaskResponse{}, nil
}

// SignalWait handles the SignalWait RPC: resumes a WAIT step by stable step id.
func (s *EngineServer) SignalWait(ctx context.Context, req *workflowv1.SignalWaitRequest) (*workflowv1.SignalWaitResponse, error) {
	if req.InstanceId == "" || req.WaitStepId == "" {
		return nil, engineapi.GRPCInvalidArgument("instance_id and wait_step_id are required")
	}
	vars := structToMap(req.Variables)
	if err := s.instSvc.SignalWaitAndAdvance(ctx, req.InstanceId, req.WaitStepId, vars); err != nil {
		return nil, mapResumeErr(err)
	}
	return &workflowv1.SignalWaitResponse{}, nil
}

// mapResumeErr translates instance-layer sentinel errors into gRPC status codes.
func mapResumeErr(err error) error {
	switch {
	case errors.Is(err, instance.ErrInstanceNotFound),
		errors.Is(err, instance.ErrUserTaskNotFound):
		return engineapi.GRPCNotFound(err.Error())
	case errors.Is(err, instance.ErrInstanceTerminal),
		errors.Is(err, instance.ErrWaitStepNotParked):
		return engineapi.GRPCFailedPrecondition(err.Error())
	case errors.Is(err, instance.ErrStepTypeMismatch):
		return engineapi.GRPCInvalidArgument(err.Error())
	default:
		return engineapi.GRPCInternal(err)
	}
}

// ── Conversion helpers ─────────────────────���─────────────────────────────��────

func structToMap(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	m := make(map[string]any, len(s.Fields))
	for k, v := range s.Fields {
		m[k] = v.AsInterface()
	}
	return m
}

func protoDefToInternal(p *workflowv1.WorkflowDefinition) *definition.WorkflowDefinition {
	d := &definition.WorkflowDefinition{
		ID:                    p.Id,
		Name:                  p.Name,
		Description:           p.Description,
		AutoStartNextWorkflow: p.AutoStartNextWorkflow,
		NextWorkflowId:        p.NextWorkflowId,
	}
	for _, ps := range p.Steps {
		s := definition.WorkflowStep{
			ID:                   ps.Id,
			Name:                 ps.Name,
			Type:                 definition.StepType(ps.Type.String()[len("STEP_TYPE_"):]),
			Description:          ps.Description,
			NextStep:             ps.NextStep,
			ParallelNextSteps:    ps.ParallelNextSteps,
			JoinStep:             ps.JoinStep,
			ConditionalNextSteps: ps.ConditionalNextSteps,
			JobType:              ps.JobType,
			DelegateClass:        ps.DelegateClass,
			RetryCount:           int(ps.RetryCount),
		}
		for _, pbe := range ps.BoundaryEvents {
			s.BoundaryEvents = append(s.BoundaryEvents, definition.BoundaryEvent{
				Type:         definition.BoundaryEventType(pbe.Type.String()[len("BOUNDARY_EVENT_TYPE_"):]),
				Duration:     pbe.Duration,
				Interrupting: pbe.Interrupting,
				TargetStepId: pbe.TargetStepId,
			})
		}
		if len(ps.Transformations) > 0 {
			s.Transformations = make(map[string]json.RawMessage, len(ps.Transformations))
			for k, v := range ps.Transformations {
				if b, err := json.Marshal(v.AsInterface()); err == nil {
					s.Transformations[k] = b
				}
			}
		}
		d.Steps = append(d.Steps, s)
	}
	return d
}

func internalDefToProto(d *definition.WorkflowDefinition) *workflowv1.WorkflowDefinition {
	return &workflowv1.WorkflowDefinition{
		Id:          d.ID,
		Version:     int32(d.Version),
		Name:        d.Name,
		Description: d.Description,
	}
}

func internalInstToProto(inst *instance.WorkflowInstance) *workflowv1.WorkflowInstance {
	p := &workflowv1.WorkflowInstance{
		Id:                inst.ID,
		DefinitionId:      inst.DefinitionID,
		DefinitionVersion: int32(inst.DefinitionVersion),
		Status:            workflowv1.InstanceStatus(workflowv1.InstanceStatus_value["INSTANCE_STATUS_"+string(inst.Status)]),
		CurrentStepIds:    inst.CurrentStepIDs,
		StartedAt:         timestamppb.New(inst.StartedAt),
	}
	if inst.CompletedAt != nil {
		p.CompletedAt = timestamppb.New(*inst.CompletedAt)
	}
	if inst.FailureReason != nil {
		p.FailureReason = *inst.FailureReason
	}
	if inst.BusinessKey != nil {
		p.BusinessKey = *inst.BusinessKey
	}

	// Deserialise variables JSONB → structpb.Struct
	if len(inst.Variables) > 0 {
		var m map[string]any
		if json.Unmarshal(inst.Variables, &m) == nil {
			if sv, err := structpb.NewStruct(m); err == nil {
				p.Variables = sv
			}
		}
	}
	return p
}
