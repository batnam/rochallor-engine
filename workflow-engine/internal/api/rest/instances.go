package rest

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	engineapi "github.com/batnam/rochallor-engine/workflow-engine/internal/api"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// InstanceHandlers exposes the instance lifecycle endpoints.
type InstanceHandlers struct {
	svc *instance.Service
}

// NewInstanceHandlers creates InstanceHandlers backed by svc.
func NewInstanceHandlers(svc *instance.Service) *InstanceHandlers {
	return &InstanceHandlers{svc: svc}
}

type startRequest struct {
	DefinitionID      string         `json:"definitionId"`
	DefinitionVersion int            `json:"definitionVersion,omitempty"`
	Variables         map[string]any `json:"variables,omitempty"`
	BusinessKey       string         `json:"businessKey,omitempty"`
}

// List handles GET /v1/instances.
func (h *InstanceHandlers) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	definitionID := q.Get("definitionId")
	status := q.Get("status")
	businessKey := q.Get("businessKey")

	page, _ := strconv.Atoi(q.Get("page"))
	pageSize := 20
	if ps, err := strconv.Atoi(q.Get("pageSize")); err == nil && ps > 0 {
		pageSize = ps
	}

	result, err := h.svc.List(r.Context(), definitionID, status, businessKey, page, pageSize)
	if err != nil {
		engineapi.WriteInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Start handles POST /v1/instances.
func (h *InstanceHandlers) Start(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		engineapi.WriteBadRequest(w, "invalid request body: "+err.Error())
		return
	}
	if req.DefinitionID == "" {
		engineapi.WriteBadRequest(w, "definitionId is required")
		return
	}

	inst, err := h.svc.Start(r.Context(), req.DefinitionID, req.DefinitionVersion, req.Variables, req.BusinessKey)
	if err != nil {
		engineapi.WriteInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, inst)
}

// Get handles GET /v1/instances/{id}.
func (h *InstanceHandlers) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	inst, err := h.svc.Get(r.Context(), id)
	if err != nil {
		engineapi.WriteNotFound(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// GetHistory handles GET /v1/instances/{id}/history.
func (h *InstanceHandlers) GetHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	history, err := h.svc.GetHistory(r.Context(), id)
	if err != nil {
		engineapi.WriteInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": history})
}

type cancelRequest struct {
	Reason string `json:"reason,omitempty"`
}

// Cancel handles POST /v1/instances/{id}/cancel.
func (h *InstanceHandlers) Cancel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req cancelRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	inst, err := h.svc.Cancel(r.Context(), id, req.Reason)
	if err != nil {
		engineapi.WriteInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inst)
}
