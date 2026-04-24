package client_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
	workflowv1 "github.com/batnam/rochallor-engine/workflow-sdk-go/internal/gen/workflow/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

const bufSize = 128 * 1024

// testServer is a minimal in-process WorkflowEngine gRPC server for tests.
type testServer struct {
	workflowv1.UnimplementedWorkflowEngineServer
	// recordedComplete captures the last CompleteJobRequest received.
	recordedComplete *workflowv1.CompleteJobRequest
	// recordedFail captures the last FailJobRequest received.
	recordedFail *workflowv1.FailJobRequest
	// jobsToReturn is returned by PollJobs.
	jobsToReturn []*workflowv1.Job
}

func (s *testServer) PollJobs(_ context.Context, _ *workflowv1.PollJobsRequest) (*workflowv1.PollJobsResponse, error) {
	return &workflowv1.PollJobsResponse{Jobs: s.jobsToReturn}, nil
}

func (s *testServer) CompleteJob(_ context.Context, req *workflowv1.CompleteJobRequest) (*workflowv1.CompleteJobResponse, error) {
	s.recordedComplete = req
	return &workflowv1.CompleteJobResponse{}, nil
}

func (s *testServer) FailJob(_ context.Context, req *workflowv1.FailJobRequest) (*workflowv1.FailJobResponse, error) {
	s.recordedFail = req
	return &workflowv1.FailJobResponse{}, nil
}

// startBufconn starts an in-process gRPC server and returns a dialer and cleanup func.
func startBufconn(t *testing.T, srv *testServer) (func(context.Context, string) (net.Conn, error), func()) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()
	workflowv1.RegisterWorkflowEngineServer(s, srv)
	go func() { _ = s.Serve(lis) }()
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
	cleanup := func() {
		s.Stop()
		lis.Close()
	}
	return dialer, cleanup
}

// newBufconnClient returns a GrpcClient connected via bufconn.
// "passthrough:///" scheme tells gRPC to skip DNS resolution and use the
// custom ContextDialer directly.
func newBufconnClient(t *testing.T, srv *testServer) *client.GrpcClient {
	t.Helper()
	dialer, cleanup := startBufconn(t, srv)
	t.Cleanup(cleanup)
	c, err := client.NewGrpc(
		"passthrough:///bufnet",
		"w1",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("NewGrpc: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestGrpcClientDial(t *testing.T) {
	// Dialing does not establish a real connection until the first RPC.
	c, err := client.NewGrpc("localhost:19999", "w1")
	if err != nil {
		t.Fatalf("NewGrpc: %v", err)
	}
	defer c.Close()
}

func TestGrpcClientPollHappyPath(t *testing.T) {
	srv := &testServer{
		jobsToReturn: []*workflowv1.Job{
			{Id: "j1", JobType: "my-job", InstanceId: "i1", RetriesRemaining: 2},
		},
	}
	c := newBufconnClient(t, srv)

	jobs, err := c.PollJobs(context.Background(), client.PollJobsRequest{
		WorkerID: "w1",
		JobTypes: []string{"my-job"},
		MaxJobs:  1,
	})
	if err != nil {
		t.Fatalf("PollJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if jobs[0].ID != "j1" {
		t.Errorf("want ID=j1, got %q", jobs[0].ID)
	}
	if jobs[0].JobType != "my-job" {
		t.Errorf("want JobType=my-job, got %q", jobs[0].JobType)
	}
}

func TestGrpcClientPollVariablesPopulated(t *testing.T) {
	vars, _ := structpb.NewStruct(map[string]any{"loanId": "L-001", "amount": float64(5000)})
	srv := &testServer{
		jobsToReturn: []*workflowv1.Job{
			{Id: "j2", JobType: "loan-check", Variables: vars},
		},
	}
	c := newBufconnClient(t, srv)

	jobs, err := c.PollJobs(context.Background(), client.PollJobsRequest{
		WorkerID: "w1",
		JobTypes: []string{"loan-check"},
		MaxJobs:  1,
	})
	if err != nil {
		t.Fatalf("PollJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if jobs[0].Variables == nil {
		t.Fatal("Variables must not be nil when server returns variables")
	}
	var decoded map[string]any
	if err := json.Unmarshal(jobs[0].Variables, &decoded); err != nil {
		t.Fatalf("unmarshal Variables: %v", err)
	}
	if decoded["loanId"] != "L-001" {
		t.Errorf("want loanId=L-001, got %v", decoded["loanId"])
	}
}

func TestGrpcClientCompleteJob(t *testing.T) {
	srv := &testServer{}
	c := newBufconnClient(t, srv)

	if err := c.CompleteJob(context.Background(), "j1", "w1", map[string]any{"result": "ok"}); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	if srv.recordedComplete == nil {
		t.Fatal("server did not receive CompleteJob request")
	}
	if srv.recordedComplete.JobId != "j1" {
		t.Errorf("want job_id=j1, got %q", srv.recordedComplete.JobId)
	}
	if srv.recordedComplete.WorkerId != "w1" {
		t.Errorf("want worker_id=w1, got %q", srv.recordedComplete.WorkerId)
	}
}

func TestGrpcClientFailJob(t *testing.T) {
	srv := &testServer{}
	c := newBufconnClient(t, srv)

	if err := c.FailJob(context.Background(), "j1", "w1", "boom", true); err != nil {
		t.Fatalf("FailJob: %v", err)
	}
	if srv.recordedFail == nil {
		t.Fatal("server did not receive FailJob request")
	}
	if srv.recordedFail.JobId != "j1" {
		t.Errorf("want job_id=j1, got %q", srv.recordedFail.JobId)
	}
	if !srv.recordedFail.Retryable {
		t.Error("want retryable=true")
	}
	if srv.recordedFail.ErrorMessage != "boom" {
		t.Errorf("want error_message=boom, got %q", srv.recordedFail.ErrorMessage)
	}
}
