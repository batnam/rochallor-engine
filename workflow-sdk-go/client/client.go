// Package client provides the EngineClient interface and shared types for the
// workflow SDK's REST and gRPC transport implementations.
package client

import (
	"context"
	"encoding/json"
	"time"
)

// EngineClient is the interface satisfied by both the REST and gRPC client
// implementations. The runner uses this interface to poll, complete, and fail
// jobs without coupling to a specific transport.
type EngineClient interface {
	// PollJobs claims up to maxJobs jobs of the given jobTypes for workerID.
	PollJobs(ctx context.Context, req PollJobsRequest) ([]Job, error)
	// CompleteJob marks a job completed and merges variables.
	CompleteJob(ctx context.Context, jobID, workerID string, variablesToSet map[string]any) error
	// FailJob records a job failure; retryable controls re-enqueue.
	FailJob(ctx context.Context, jobID, workerID, errorMessage string, retryable bool) error
}

// PollJobsRequest is the input to PollJobs.
type PollJobsRequest struct {
	WorkerID string
	JobTypes []string
	MaxJobs  int
}

// Job is a unit of work returned by PollJobs.
type Job struct {
	ID               string          `json:"id"`
	JobType          string          `json:"jobType"`
	InstanceID       string          `json:"instanceId"`
	StepExecutionID  string          `json:"stepExecutionId"`
	RetriesRemaining int             `json:"retriesRemaining"`
	Variables        json.RawMessage `json:"variables,omitempty"`
	LockExpiresAt    *time.Time      `json:"lockExpiresAt,omitempty"`
}
