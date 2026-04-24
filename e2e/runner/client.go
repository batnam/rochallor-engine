package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/batnam/e2e/runner/scenarios"
)

// Client implements scenarios.ClientIface via the engine REST API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Client targeting baseURL.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		// Set a default timeout to prevent the test runner from hanging indefinitely
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// =====================================================================
// API METHODS
// =====================================================================

// UploadDefinition uploads a workflow definition JSON to POST /v1/definitions.
func (c *Client) UploadDefinition(ctx context.Context, defJSON []byte) error {
	req, err := c.newRequest(ctx, http.MethodPost, "v1/definitions", bytes.NewReader(defJSON))
	if err != nil {
		return fmt.Errorf("upload definition: %w", err)
	}
	return c.doRequest(req, nil)
}

// StartInstance creates a new instance for defID and returns the instance ID.
func (c *Client) StartInstance(ctx context.Context, defID string, vars map[string]any) (string, error) {
	body, err := json.Marshal(map[string]any{
		"definitionId": defID,
		"variables":    vars,
	})
	if err != nil {
		return "", fmt.Errorf("start instance: marshal request: %w", err)
	}

	req, err := c.newRequest(ctx, http.MethodPost, "v1/instances", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("start instance: %w", err)
	}

	var inst scenarios.Instance
	if err := c.doRequest(req, &inst); err != nil {
		return "", fmt.Errorf("start instance: %w", err)
	}
	return inst.ID, nil
}

// GetInstance fetches the current state of an instance.
func (c *Client) GetInstance(ctx context.Context, id string) (scenarios.Instance, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "v1/instances/"+id, nil)
	if err != nil {
		return scenarios.Instance{}, fmt.Errorf("get instance: %w", err)
	}

	var inst scenarios.Instance
	if err := c.doRequest(req, &inst); err != nil {
		return scenarios.Instance{}, fmt.Errorf("get instance: %w", err)
	}
	return inst, nil
}

// GetHistory returns the step execution history for an instance.
func (c *Client) GetHistory(ctx context.Context, id string) ([]scenarios.StepExecution, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "v1/instances/"+id+"/history", nil)
	if err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}

	var envelope struct {
		Items []scenarios.StepExecution `json:"items"`
	}
	if err := c.doRequest(req, &envelope); err != nil {
		return nil, fmt.Errorf("get history: %w", err)
	}
	return envelope.Items, nil
}

// CompleteUserTaskByStableID calls the new stable-id route
// POST /v1/instances/{instanceId}/user-tasks/{userTaskStepId}/complete.
// userTaskStepID is the stable step id from the workflow definition.
func (c *Client) CompleteUserTaskByStableID(ctx context.Context, instanceID, userTaskStepID string, vars map[string]any) error {
	body, err := json.Marshal(map[string]any{"variables": vars})
	if err != nil {
		return fmt.Errorf("complete user task by stable id: marshal request: %w", err)
	}

	path := "v1/instances/" + instanceID + "/user-tasks/" + userTaskStepID + "/complete"
	req, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("complete user task by stable id: %w", err)
	}

	return c.doRequest(req, nil)
}

// SignalWait calls POST /v1/instances/{instanceId}/signals/{waitStepId}.
// The body IS the variable map (not wrapped in {"variables": …}); an empty map
// is valid — the signal itself is the event.
func (c *Client) SignalWait(ctx context.Context, instanceID, waitStepID string, vars map[string]any) error {
	body, err := json.Marshal(vars)
	if err != nil {
		return fmt.Errorf("signal wait: marshal request: %w", err)
	}

	path := "v1/instances/" + instanceID + "/signals/" + waitStepID
	req, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("signal wait: %w", err)
	}

	return c.doRequest(req, nil)
}

// =====================================================================
// HELPER METHODS
// =====================================================================

// newRequest safely constructs an HTTP request with a properly joined URL and sets the Content-Type.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	u, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return nil, fmt.Errorf("build url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// doRequest executes the HTTP request, validates the status code (>= 200 and < 300 are successful),
// and decodes the JSON response into the provided 'out' interface if applicable.
func (c *Client) doRequest(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	// Treat any status code outside the 2xx range as an error
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		errMsg := strings.TrimSpace(string(b))
		return fmt.Errorf("engine returned %d: %s", resp.StatusCode, errMsg)
	}

	// If the caller provided a target struct, decode the response body
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}
