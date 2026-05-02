package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	"github.com/batnam/e2e/runner/scenarios"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"
)

// GrpcClient implements scenarios.ClientIface via the engine gRPC API.
type GrpcClient struct {
	conn *grpc.ClientConn
	stub workflowv1.WorkflowEngineClient
}

// NewGrpcClient dials target using insecure credentials.
func NewGrpcClient(target string) (*GrpcClient, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", target, err)
	}
	return &GrpcClient{conn: conn, stub: workflowv1.NewWorkflowEngineClient(conn)}, nil
}

// Close releases the underlying gRPC connection.
func (c *GrpcClient) Close() { c.conn.Close() }

// =====================================================================
// ClientIface implementation
// =====================================================================

func (c *GrpcClient) UploadDefinition(ctx context.Context, defJSON []byte) error {
	def, err := parseJSONDefinition(defJSON)
	if err != nil {
		return fmt.Errorf("upload definition: %w", err)
	}
	_, err = c.stub.UploadDefinition(ctx, &workflowv1.UploadDefinitionRequest{Definition: def})
	if err != nil {
		return fmt.Errorf("upload definition: %w", err)
	}
	return nil
}

func (c *GrpcClient) StartInstance(ctx context.Context, defID string, vars map[string]any) (string, error) {
	sv, err := toStruct(vars)
	if err != nil {
		return "", fmt.Errorf("start instance: %w", err)
	}
	resp, err := c.stub.StartInstance(ctx, &workflowv1.StartInstanceRequest{
		DefinitionId: defID,
		Variables:    sv,
	})
	if err != nil {
		return "", fmt.Errorf("start instance: %w", err)
	}
	return resp.Instance.Id, nil
}

func (c *GrpcClient) GetInstance(ctx context.Context, id string) (scenarios.Instance, error) {
	resp, err := c.stub.GetInstance(ctx, &workflowv1.GetInstanceRequest{Id: id})
	if err != nil {
		return scenarios.Instance{}, fmt.Errorf("get instance: %w", err)
	}
	return protoInstToScenario(resp.Instance), nil
}

func (c *GrpcClient) GetHistory(ctx context.Context, id string) ([]scenarios.StepExecution, error) {
	resp, err := c.stub.GetInstanceHistory(ctx, &workflowv1.GetInstanceHistoryRequest{Id: id})
	if err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}
	out := make([]scenarios.StepExecution, len(resp.Executions))
	for i, e := range resp.Executions {
		out[i] = scenarios.StepExecution{
			ID:       e.Id,
			StepID:   e.StepId,
			StepType: trimPrefix(e.StepType.String(), "STEP_TYPE_"),
			Status:   trimPrefix(e.Status.String(), "STEP_EXECUTION_STATUS_"),
		}
	}
	return out, nil
}

func (c *GrpcClient) CompleteUserTaskByStableID(ctx context.Context, instanceID, userTaskStepID string, vars map[string]any) error {
	sv, err := toStruct(vars)
	if err != nil {
		return fmt.Errorf("complete user task: %w", err)
	}
	_, err = c.stub.CompleteUserTask(ctx, &workflowv1.CompleteUserTaskRequest{
		InstanceId:     instanceID,
		UserTaskStepId: userTaskStepID,
		Result:         sv,
	})
	if err != nil {
		return fmt.Errorf("complete user task by stable id: %w", err)
	}
	return nil
}

func (c *GrpcClient) SignalWait(ctx context.Context, instanceID, waitStepID string, vars map[string]any) error {
	sv, err := toStruct(vars)
	if err != nil {
		return fmt.Errorf("signal wait: %w", err)
	}
	_, err = c.stub.SignalWait(ctx, &workflowv1.SignalWaitRequest{
		InstanceId: instanceID,
		WaitStepId: waitStepID,
		Variables:  sv,
	})
	if err != nil {
		return fmt.Errorf("signal wait: %w", err)
	}
	return nil
}

// =====================================================================
// Helpers
// =====================================================================

func toStruct(m map[string]any) (*structpb.Struct, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return structpb.NewStruct(m)
}

func trimPrefix(s, prefix string) string {
	return strings.TrimPrefix(s, prefix)
}

func protoInstToScenario(p *workflowv1.WorkflowInstance) scenarios.Instance {
	inst := scenarios.Instance{
		ID:             p.Id,
		Status:         trimPrefix(p.Status.String(), "INSTANCE_STATUS_"),
		CurrentStepIds: p.CurrentStepIds,
		FailureReason:  p.FailureReason,
	}
	if p.Variables != nil {
		inst.Variables = p.Variables.AsMap()
	}
	return inst
}

// =====================================================================
// JSON → proto conversion for UploadDefinition
// =====================================================================

// jsonWorkflowDef mirrors the camelCase JSON format used by the scenario definition files.
type jsonWorkflowDef struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	Description           string     `json:"description"`
	AutoStartNextWorkflow bool       `json:"autoStartNextWorkflow"`
	NextWorkflowId        string     `json:"nextWorkflowId"`
	Steps                 []jsonStep `json:"steps"`
}

type jsonStep struct {
	ID                   string                     `json:"id"`
	Name                 string                     `json:"name"`
	Type                 string                     `json:"type"`
	Description          string                     `json:"description"`
	NextStep             string                     `json:"nextStep"`
	ParallelNextSteps    []string                   `json:"parallelNextSteps"`
	JoinStep             string                     `json:"joinStep"`
	ConditionalNextSteps map[string]string          `json:"conditionalNextSteps"`
	Transformations      map[string]json.RawMessage `json:"transformations"`
	JobType              string                     `json:"jobType"`
	DelegateClass        string                     `json:"delegateClass"`
	RetryCount           int32                      `json:"retryCount"`
	BoundaryEvents       []jsonBoundaryEvent        `json:"boundaryEvents"`
}

type jsonBoundaryEvent struct {
	Type         string `json:"type"`
	Duration     string `json:"duration"`
	Interrupting bool   `json:"interrupting"`
	TargetStepId string `json:"targetStepId"`
}

var stepTypeByName = map[string]workflowv1.StepType{
	"SERVICE_TASK":     workflowv1.StepType_STEP_TYPE_SERVICE_TASK,
	"USER_TASK":        workflowv1.StepType_STEP_TYPE_USER_TASK,
	"DECISION":         workflowv1.StepType_STEP_TYPE_DECISION,
	"TRANSFORMATION":   workflowv1.StepType_STEP_TYPE_TRANSFORMATION,
	"WAIT":             workflowv1.StepType_STEP_TYPE_WAIT,
	"PARALLEL_GATEWAY": workflowv1.StepType_STEP_TYPE_PARALLEL_GATEWAY,
	"JOIN_GATEWAY":     workflowv1.StepType_STEP_TYPE_JOIN_GATEWAY,
	"END":              workflowv1.StepType_STEP_TYPE_END,
}

func parseJSONDefinition(defJSON []byte) (*workflowv1.WorkflowDefinition, error) {
	var jd jsonWorkflowDef
	if err := json.Unmarshal(defJSON, &jd); err != nil {
		return nil, fmt.Errorf("parse definition JSON: %w", err)
	}

	pd := &workflowv1.WorkflowDefinition{
		Id:                    jd.ID,
		Name:                  jd.Name,
		Description:           jd.Description,
		AutoStartNextWorkflow: jd.AutoStartNextWorkflow,
		NextWorkflowId:        jd.NextWorkflowId,
	}

	for _, js := range jd.Steps {
		st, ok := stepTypeByName[js.Type]
		if !ok {
			return nil, fmt.Errorf("unknown step type %q in step %q", js.Type, js.ID)
		}
		ps := &workflowv1.WorkflowStep{
			Id:                   js.ID,
			Name:                 js.Name,
			Type:                 st,
			Description:          js.Description,
			NextStep:             js.NextStep,
			ParallelNextSteps:    js.ParallelNextSteps,
			JoinStep:             js.JoinStep,
			ConditionalNextSteps: js.ConditionalNextSteps,
			JobType:              js.JobType,
			DelegateClass:        js.DelegateClass,
			RetryCount:           js.RetryCount,
		}
		if len(js.Transformations) > 0 {
			ps.Transformations = make(map[string]*structpb.Value, len(js.Transformations))
			for k, raw := range js.Transformations {
				var v any
				if err := json.Unmarshal(raw, &v); err != nil {
					return nil, fmt.Errorf("step %q transformation %q: %w", js.ID, k, err)
				}
				sv, err := structpb.NewValue(v)
				if err != nil {
					return nil, fmt.Errorf("step %q transformation %q: convert to proto: %w", js.ID, k, err)
				}
				ps.Transformations[k] = sv
			}
		}
		for _, jbe := range js.BoundaryEvents {
			ps.BoundaryEvents = append(ps.BoundaryEvents, &workflowv1.BoundaryEvent{
				Type:         workflowv1.BoundaryEventType_BOUNDARY_EVENT_TYPE_TIMER,
				Duration:     jbe.Duration,
				Interrupting: jbe.Interrupting,
				TargetStepId: jbe.TargetStepId,
			})
		}
		pd.Steps = append(pd.Steps, ps)
	}
	return pd, nil
}
