package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-sdk-go/client"
)

func TestRestClientPollHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/jobs/poll" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jobs": []map[string]any{
				{"id": "j1", "jobType": "my-job", "instanceId": "i1", "retriesRemaining": 2},
			},
		})
	}))
	defer srv.Close()

	c := client.NewRest(srv.URL, "w1")
	jobs, err := c.PollJobs(context.Background(), client.PollJobsRequest{
		WorkerID: "w1",
		JobTypes: []string{"my-job"},
		MaxJobs:  1,
	})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "j1" {
		t.Errorf("want 1 job with id=j1, got %+v", jobs)
	}
}

func TestRestClientPollJobVariablesNotNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jobs": []map[string]any{
				{
					"id":               "j2",
					"jobType":          "loan-check",
					"instanceId":       "i2",
					"retriesRemaining": 3,
					"variables":        map[string]any{"loanId": "L-001", "amount": 5000},
				},
			},
		})
	}))
	defer srv.Close()

	c := client.NewRest(srv.URL, "w1")
	jobs, err := c.PollJobs(context.Background(), client.PollJobsRequest{
		WorkerID: "w1",
		JobTypes: []string{"loan-check"},
		MaxJobs:  1,
	})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if jobs[0].Variables == nil {
		t.Fatal("job.Variables must not be nil when server returns variables")
	}
	var vars map[string]any
	if err := json.Unmarshal(jobs[0].Variables, &vars); err != nil {
		t.Fatalf("unmarshal Variables: %v", err)
	}
	if vars["loanId"] != "L-001" {
		t.Errorf("want loanId=L-001, got %v", vars["loanId"])
	}
}

func TestRestClientPoll5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "oops", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := client.NewRest(srv.URL, "w1")
	_, err := c.PollJobs(context.Background(), client.PollJobsRequest{
		WorkerID: "w1",
		JobTypes: []string{"x"},
		MaxJobs:  1,
	})
	if err == nil {
		t.Fatal("want error for 500 response")
	}
}

func TestRestClientCompleteJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := client.NewRest(srv.URL, "w1")
	if err := c.CompleteJob(context.Background(), "j1", "w1", nil); err != nil {
		t.Errorf("complete: %v", err)
	}
}

func TestRestClientFailJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := client.NewRest(srv.URL, "w1")
	if err := c.FailJob(context.Background(), "j1", "w1", "boom", true); err != nil {
		t.Errorf("fail: %v", err)
	}
}
