// Package handler provides the job-type handler registry.
// Handlers are keyed by jobType string — never by Java delegate class path (R-010).
package handler

import (
	"context"
	"fmt"
	"sync"
)

// JobContext gives handlers typed access to the job's variables and metadata.
type JobContext struct {
	JobID            string
	InstanceID       string
	StepID           string
	JobType          string
	Attempt          int
	RetriesRemaining int
	Variables        map[string]any
}

// Result is what a handler returns on success.
type Result struct {
	// VariablesToSet are merged back into the instance variables on CompleteJob.
	VariablesToSet map[string]any
}

// Handler is the function signature that workers implement.
type Handler func(ctx context.Context, job JobContext) (Result, error)

// Registry maps jobType strings to Handler functions.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// New creates an empty Registry.
func New() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

// Register associates jobType with handler. Panics if jobType is empty.
func (r *Registry) Register(jobType string, h Handler) {
	if jobType == "" {
		panic("workflow-sdk-go: Register called with empty jobType")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[jobType] = h
}

// Get returns the handler for jobType or an error if none is registered.
func (r *Registry) Get(jobType string) (Handler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[jobType]
	if !ok {
		return nil, fmt.Errorf("no handler registered for jobType %q", jobType)
	}
	return h, nil
}

// JobTypes returns all registered jobType strings.
func (r *Registry) JobTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		types = append(types, k)
	}
	return types
}
