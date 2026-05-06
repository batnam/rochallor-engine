package rest

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	engineapi "github.com/batnam/rochallor-engine/workflow-engine/internal/api"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/job"
)

// JobHandlers exposes the job poll/complete/fail endpoints.
type JobHandlers struct {
	db         db.DB
	jobStore   job.JobStore
	instSvc    *instance.Service
	dispatcher dispatch.Dispatcher
}

// NewJobHandlers creates JobHandlers backed by the storage abstractions and
// the instance service.
func NewJobHandlers(dbConn db.DB, jobStore job.JobStore, instSvc *instance.Service, dispatcher dispatch.Dispatcher) *JobHandlers {
	return &JobHandlers{db: dbConn, jobStore: jobStore, instSvc: instSvc, dispatcher: dispatcher}
}

type pollRequest struct {
	WorkerID string   `json:"workerId"`
	JobTypes []string `json:"jobTypes"`
	MaxJobs  int      `json:"maxJobs,omitempty"`
}

// Poll handles POST /v1/jobs/poll.
func (h *JobHandlers) Poll(w http.ResponseWriter, r *http.Request) {
	var req pollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		engineapi.WriteBadRequest(w, "invalid request body: "+err.Error())
		return
	}
	if req.WorkerID == "" {
		engineapi.WriteBadRequest(w, "workerId is required")
		return
	}
	if len(req.JobTypes) == 0 {
		engineapi.WriteBadRequest(w, "jobTypes must not be empty")
		return
	}
	max := req.MaxJobs
	if max <= 0 {
		max = 10
	}

	jobs, err := job.Poll(r.Context(), h.jobStore, req.WorkerID, req.JobTypes, max)
	if err != nil {
		engineapi.WriteInternalError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

type completeRequest struct {
	WorkerID  string         `json:"workerId"`
	Variables map[string]any `json:"variables,omitempty"`
}

// Complete handles POST /v1/jobs/{id}/complete.
// Marks the job COMPLETED, merges variables into the instance, and dispatches
// the next step (CompleteJobAndAdvance).
func (h *JobHandlers) Complete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req completeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		engineapi.WriteBadRequest(w, "invalid request body: "+err.Error())
		return
	}
	if err := h.instSvc.CompleteJobAndAdvance(r.Context(), id, req.WorkerID, req.Variables); err != nil {
		engineapi.WriteInternalError(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type failRequest struct {
	WorkerID     string `json:"workerId"`
	ErrorMessage string `json:"errorMessage"`
	Retryable    bool   `json:"retryable"`
}

// Fail handles POST /v1/jobs/{id}/fail.
func (h *JobHandlers) Fail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req failRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		engineapi.WriteBadRequest(w, "invalid request body: "+err.Error())
		return
	}
	if err := job.Fail(r.Context(), h.db, h.jobStore, h.dispatcher, id, req.WorkerID, req.ErrorMessage, req.Retryable); err != nil {
		engineapi.WriteInternalError(w, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
