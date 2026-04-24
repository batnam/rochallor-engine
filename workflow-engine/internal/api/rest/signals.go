package rest

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	engineapi "github.com/batnam/rochallor-engine/workflow-engine/internal/api"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// SignalHandlers exposes the WAIT-step signal endpoint.
type SignalHandlers struct {
	svc *instance.Service
}

// NewSignalHandlers creates SignalHandlers backed by the instance service.
func NewSignalHandlers(svc *instance.Service) *SignalHandlers {
	return &SignalHandlers{svc: svc}
}

// Signal handles POST /v1/instances/{instanceId}/signals/{waitStepId}. The
// request body — if present — is an arbitrary JSON object whose keys are
// shallow-merged into the instance variables on resume. An empty or absent
// body is valid: the signal itself is the event.
//
// Note: this endpoint's body differs from /user-tasks/{id}/complete — the body
// IS the variable map (no `{"variables": …}` wrapper). This is intentional and
// documented in the OpenAPI contract.
func (h *SignalHandlers) Signal(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "instanceId")
	waitStepID := chi.URLParam(r, "waitStepId")

	var vars map[string]any
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&vars); err != nil {
			engineapi.WriteBadRequest(w, "invalid request body: "+err.Error())
			return
		}
	}

	err := h.svc.SignalWaitAndAdvance(r.Context(), instanceID, waitStepID, vars)
	if writeResumeError(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
