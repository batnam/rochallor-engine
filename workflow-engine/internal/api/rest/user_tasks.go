package rest

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	engineapi "github.com/batnam/rochallor-engine/workflow-engine/internal/api"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// UserTaskHandlers exposes the user-task completion endpoints.
type UserTaskHandlers struct {
	svc *instance.Service
}

// NewUserTaskHandlers creates UserTaskHandlers backed by the instance service.
func NewUserTaskHandlers(svc *instance.Service) *UserTaskHandlers {
	return &UserTaskHandlers{svc: svc}
}

type completeUserTaskRequest struct {
	WorkerID  string         `json:"workerId,omitempty"`
	Variables map[string]any `json:"variables,omitempty"`
}

// CompleteByStableID handles POST /v1/instances/{instanceId}/user-tasks/{userTaskId}/complete.
// The userTaskId in the path is the STABLE STEP ID declared in the workflow
// definition (e.g. "checker-approval"), not the internal user_task row ULID.
func (h *UserTaskHandlers) CompleteByStableID(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceId")
	userTaskStepID := chi.URLParam(r, "userTaskId")

	var req completeUserTaskRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			engineapi.WriteBadRequest(w, "invalid request body: "+err.Error())
			return
		}
	}

	err := h.svc.CompleteUserTaskAndAdvance(r.Context(), instanceID, userTaskStepID, req.WorkerID, req.Variables)
	if writeResumeError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeResumeError maps sentinel resume errors to the appropriate HTTP status
// and returns true if a response was written. Used by user-task completion
// and wait-step signal handlers.
func writeResumeError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, instance.ErrInstanceNotFound),
		errors.Is(err, instance.ErrUserTaskNotFound):
		engineapi.WriteNotFound(w, err.Error())
	case errors.Is(err, instance.ErrInstanceTerminal),
		errors.Is(err, instance.ErrWaitStepNotParked):
		engineapi.WriteConflict(w, err.Error())
	case errors.Is(err, instance.ErrStepTypeMismatch):
		engineapi.WriteBadRequest(w, err.Error())
	default:
		engineapi.WriteInternalError(w, err.Error())
	}
	return true
}
