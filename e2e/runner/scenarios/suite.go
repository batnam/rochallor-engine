// Package scenarios contains E2E test functions, one per workflow scenario type.
// Each function is independent: it uploads a definition, starts an instance,
// and asserts the expected final state.
package scenarios

import (
	"context"
	"fmt"
	"time"
)

// TestReporter is a minimal interface for reporting test results, satisfied by
// both *SimpleReporter (used by the runner binary) and *testing.T (unit tests).
type TestReporter interface {
	Errorf(format string, args ...any)
	Logf(format string, args ...any)
	AuditLog(instanceID string, eventType string, message string)
	Failed() bool
}
// Instance mirrors the engine's instance response shape.
type Instance struct {
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	CurrentStepIds []string       `json:"currentStepIds,omitempty"`
	Variables      map[string]any `json:"variables,omitempty"`
	FailureReason  string         `json:"failureReason,omitempty"`
	BusinessKey    string         `json:"businessKey,omitempty"`
}

// StepExecution mirrors one entry from GET /v1/instances/{id}/history.
type StepExecution struct {
	ID       string `json:"ID"`
	StepID   string `json:"StepID"`
	StepType string `json:"StepType"`
	Status   string `json:"Status"`
}

// DefinitionSummary mirrors the engine's definition summary response shape.
type DefinitionSummary struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Name    string `json:"name"`
}

// ClientIface is the minimal engine API surface used by scenario functions.
type ClientIface interface {
	UploadDefinition(ctx context.Context, defJSON []byte) error
	StartInstance(ctx context.Context, defID string, vars map[string]any) (string, error)
	StartInstanceWithBusinessKey(ctx context.Context, defID string, vars map[string]any, businessKey string) (string, error)
	GetInstance(ctx context.Context, id string) (Instance, error)
	GetHistory(ctx context.Context, id string) ([]StepExecution, error)
	CompleteUserTaskByStableID(ctx context.Context, instanceID, userTaskStepID string, vars map[string]any) error
	SignalWait(ctx context.Context, instanceID, waitStepID string, vars map[string]any) error
	CancelInstance(ctx context.Context, id string, reason string) (Instance, error)
	GetDefinition(ctx context.Context, id string) (DefinitionSummary, error)
	ListDefinitions(ctx context.Context) ([]DefinitionSummary, error)
}

// PollUntilTerminal polls GetInstance every 500 ms until COMPLETED/FAILED/CANCELLED.
func PollUntilTerminal(ctx context.Context, client ClientIface, instanceID string, timeout time.Duration) (Instance, error) {
	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return Instance{}, fmt.Errorf("timeout after %s waiting for instance %s", timeout, instanceID)
		}
		inst, err := client.GetInstance(ctx, instanceID)
		if err != nil {
			return Instance{}, fmt.Errorf("poll %s: %w", instanceID, err)
		}

		// Log status/step changes automatically
		AuditInstance(inst)

		switch inst.Status {
		case "COMPLETED", "FAILED", "CANCELLED":
			return inst, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}
