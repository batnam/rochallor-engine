// Package rest implements the chi-based HTTP/REST handlers for the Engine.
package rest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	engineapi "github.com/batnam/rochallor-engine/workflow-engine/internal/api"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// DefinitionHandlers exposes the definition endpoints.
type DefinitionHandlers struct {
	repo *definition.Repository
}

// NewDefinitionHandlers creates DefinitionHandlers backed by repo.
func NewDefinitionHandlers(repo *definition.Repository) *DefinitionHandlers {
	return &DefinitionHandlers{repo: repo}
}

// Upload handles POST /v1/definitions.
// Returns 201 on success, 415 for unsupported content types, 400 for parse/validation errors.
func (h *DefinitionHandlers) Upload(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/xml") || strings.Contains(ct, "text/xml") {
		engineapi.WriteUnsupportedFormat(w, "Only JSON workflow definitions are accepted")
		return
	}

	def, err := definition.Parse(r.Body)
	if err != nil {
		engineapi.WriteBadRequest(w, "parse error: "+err.Error())
		return
	}
	if err := definition.Validate(def); err != nil {
		engineapi.WriteBadRequest(w, "validation error: "+err.Error())
		return
	}

	sum, err := h.repo.Upload(r.Context(), def)
	if err != nil {
		engineapi.WriteInternalError(w, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, sum)
}

// GetLatest handles GET /v1/definitions/{id}.
func (h *DefinitionHandlers) GetLatest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	def, err := h.repo.GetLatest(r.Context(), id)
	if err != nil {
		engineapi.WriteNotFound(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, def)
}

// GetVersion handles GET /v1/definitions/{id}/versions/{version}.
func (h *DefinitionHandlers) GetVersion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ver, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil {
		engineapi.WriteBadRequest(w, "invalid version parameter")
		return
	}
	def, err := h.repo.GetVersion(r.Context(), id, ver)
	if err != nil {
		engineapi.WriteNotFound(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, def)
}

// List handles GET /v1/definitions.
func (h *DefinitionHandlers) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	keyword := q.Get("keyword")
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize := 20
	if ps, err := strconv.Atoi(q.Get("pageSize")); err == nil && ps > 0 {
		pageSize = ps
	}

	result, err := h.repo.List(r.Context(), keyword, page, pageSize)
	if err != nil {
		engineapi.WriteInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// writeJSON writes v as application/json with the given status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
