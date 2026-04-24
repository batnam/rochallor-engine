package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// RestClient implements EngineClient via HTTP/REST.
type RestClient struct {
	baseURL    string
	workerID   string
	httpClient *http.Client
}

// NewRest creates a RestClient targeting baseURL.
func NewRest(baseURL, workerID string) *RestClient {
	return &RestClient{
		baseURL:    baseURL,
		workerID:   workerID,
		httpClient: &http.Client{},
	}
}

// PollJobs implements EngineClient.
func (c *RestClient) PollJobs(ctx context.Context, req PollJobsRequest) ([]Job, error) {
	body, _ := json.Marshal(map[string]any{
		"workerId": req.WorkerID,
		"jobTypes": req.JobTypes,
		"maxJobs":  req.MaxJobs,
	})
	resp, err := c.post(ctx, "/v1/jobs/poll", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("poll: engine returned %d", resp.StatusCode)
	}
	var envelope struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("poll: decode response: %w", err)
	}
	return envelope.Jobs, nil
}

// CompleteJob implements EngineClient.
func (c *RestClient) CompleteJob(ctx context.Context, jobID, workerID string, variablesToSet map[string]any) error {
	body, _ := json.Marshal(map[string]any{
		"workerId":  workerID,
		"variables": variablesToSet,
	})
	resp, err := c.post(ctx, "/v1/jobs/"+jobID+"/complete", body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("completeJob %q: engine returned %d", jobID, resp.StatusCode)
	}
	return nil
}

// FailJob implements EngineClient.
func (c *RestClient) FailJob(ctx context.Context, jobID, workerID, errorMessage string, retryable bool) error {
	body, _ := json.Marshal(map[string]any{
		"workerId":     workerID,
		"errorMessage": errorMessage,
		"retryable":    retryable,
	})
	resp, err := c.post(ctx, "/v1/jobs/"+jobID+"/fail", body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failJob %q: engine returned %d", jobID, resp.StatusCode)
	}
	return nil
}

func (c *RestClient) post(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}
