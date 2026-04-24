package client

import (
	"context"
	"encoding/json"
	"fmt"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-sdk-go/internal/gen/workflow/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// GrpcClient implements EngineClient via gRPC.
// Proto stubs are vendored into internal/gen/workflow/v1 to avoid a circular
// dependency with the engine module. Run make proto-sync in the engine to
// regenerate and copy updated stubs.
type GrpcClient struct {
	conn     *grpc.ClientConn
	workerID string
}

// NewGrpc dials target and returns a GrpcClient.
func NewGrpc(target, workerID string, opts ...grpc.DialOption) (*GrpcClient, error) {
	if len(opts) == 0 {
		opts = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %q: %w", target, err)
	}
	return &GrpcClient{conn: conn, workerID: workerID}, nil
}

// Close closes the underlying gRPC connection.
func (c *GrpcClient) Close() error { return c.conn.Close() }

// PollJobs implements EngineClient via the WorkflowEngine.PollJobs RPC.
func (c *GrpcClient) PollJobs(ctx context.Context, req PollJobsRequest) ([]Job, error) {
	stub := workflowv1.NewWorkflowEngineClient(c.conn)
	resp, err := stub.PollJobs(ctx, &workflowv1.PollJobsRequest{
		WorkerId: req.WorkerID,
		JobTypes: req.JobTypes,
		MaxJobs:  int32(req.MaxJobs),
	})
	if err != nil {
		return nil, fmt.Errorf("grpc PollJobs: %w", err)
	}
	return protoJobsToSDK(resp.Jobs), nil
}

// CompleteJob implements EngineClient via the WorkflowEngine.CompleteJob RPC.
func (c *GrpcClient) CompleteJob(ctx context.Context, jobID, workerID string, variablesToSet map[string]any) error {
	var vars *structpb.Struct
	if len(variablesToSet) > 0 {
		var err error
		vars, err = marshalStruct(variablesToSet)
		if err != nil {
			return fmt.Errorf("grpc CompleteJob: marshal variables: %w", err)
		}
	}
	stub := workflowv1.NewWorkflowEngineClient(c.conn)
	_, err := stub.CompleteJob(ctx, &workflowv1.CompleteJobRequest{
		JobId:          jobID,
		WorkerId:       workerID,
		VariablesToSet: vars,
	})
	if err != nil {
		return fmt.Errorf("grpc CompleteJob: %w", err)
	}
	return nil
}

// FailJob implements EngineClient via the WorkflowEngine.FailJob RPC.
func (c *GrpcClient) FailJob(ctx context.Context, jobID, workerID, errorMessage string, retryable bool) error {
	stub := workflowv1.NewWorkflowEngineClient(c.conn)
	_, err := stub.FailJob(ctx, &workflowv1.FailJobRequest{
		JobId:        jobID,
		WorkerId:     workerID,
		ErrorMessage: errorMessage,
		Retryable:    retryable,
	})
	if err != nil {
		return fmt.Errorf("grpc FailJob: %w", err)
	}
	return nil
}

// protoJobsToSDK maps a slice of proto Job messages to SDK Job structs.
func protoJobsToSDK(jobs []*workflowv1.Job) []Job {
	out := make([]Job, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, protoJobToSDK(j))
	}
	return out
}

// protoJobToSDK maps a single proto Job to an SDK Job.
func protoJobToSDK(j *workflowv1.Job) Job {
	sdkJob := Job{
		ID:               j.Id,
		JobType:          j.JobType,
		InstanceID:       j.InstanceId,
		StepExecutionID:  j.StepExecutionId,
		RetriesRemaining: int(j.RetriesRemaining),
	}
	if j.Variables != nil {
		raw, err := protojson.Marshal(j.Variables)
		if err == nil {
			sdkJob.Variables = json.RawMessage(raw)
		}
	}
	if j.LockExpiresAt != nil {
		t := j.LockExpiresAt.AsTime()
		sdkJob.LockExpiresAt = &t
	}
	return sdkJob
}

// marshalStruct converts a map to a structpb.Struct.
func marshalStruct(m map[string]any) (*structpb.Struct, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	return structpb.NewStruct(raw)
}
