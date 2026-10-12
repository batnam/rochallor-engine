package rest

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
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
		field           string
		status, seconds int
	}{
		{"", 200, 30}, {`,"leaseSeconds":0`, 200, 30}, {`,"leaseSeconds":5`, 200, 5},
		{`,"leaseSeconds":60`, 200, 60}, {`,"leaseSeconds":3600`, 200, 3600},
		{`,"leaseSeconds":-1`, 400, 0}, {`,"leaseSeconds":4`, 400, 0}, {`,"leaseSeconds":3601`, 400, 0},
		{`,"leaseSeconds":1.5`, 400, 0}, {`,"leaseSeconds":"30"`, 400, 0},
	} {
		t.Run(tc.field, func(t *testing.T) {
			store := &leasePollStore{}
			h := NewJobHandlers(nil, store, nil, nil)
			request := httptest.NewRequest("POST", "/v1/jobs/poll", strings.NewReader(`{"workerId":"w","jobTypes":["work"]`+tc.field+`}`))
			response := httptest.NewRecorder()
			h.Poll(response, request)
			if response.Code != tc.status || store.seconds != tc.seconds {
				t.Fatalf("status=%d lease=%d body=%s", response.Code, store.seconds, response.Body)
			}
		})
	}
}
