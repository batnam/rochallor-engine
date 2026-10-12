package grpc

import (
	"context"
	"testing"

	workflowv1 "github.com/batnam/rochallor-engine/workflow-engine/api/gen/workflow/v1"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type leasePollStore struct {
	job.JobStore
	seconds int
}

func (s *leasePollStore) PollJobs(_ context.Context, _ string, _ []string, _ int, seconds int) ([]instance.Job, error) {
	s.seconds = seconds
	return nil, nil
}

func TestPollLeaseContract(t *testing.T) {
	for _, tc := range []struct {
		seconds, expected int
		code              codes.Code
	}{
		{0, 30, codes.OK}, {5, 5, codes.OK}, {60, 60, codes.OK}, {3600, 3600, codes.OK},
		{-1, 0, codes.InvalidArgument}, {4, 0, codes.InvalidArgument}, {3601, 0, codes.InvalidArgument},
	} {
		store := &leasePollStore{}
		server := NewEngineServer(nil, nil, nil, store, nil, "polling")
		_, err := server.PollJobs(t.Context(), &workflowv1.PollJobsRequest{WorkerId: "w", JobTypes: []string{"work"}, LeaseSeconds: int32(tc.seconds)})
		if status.Code(err) != tc.code || store.seconds != tc.expected {
			t.Fatalf("lease=%d: code=%s duration=%d", tc.seconds, status.Code(err), store.seconds)
		}
	}
}
