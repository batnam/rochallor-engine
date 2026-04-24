package rest

import (
	"net/http"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRouter builds the chi router with all handlers wired.
// The metrics endpoint (/metrics) is intentionally NOT on this router — it
// lives on a separate port started by main.
//
// dispatchMode controls the POST /v1/jobs/poll behaviour: in "kafka_outbox"
// mode the handler is replaced with a 410 Gone stub that tells workers to
// consume directly from Kafka (FR-004, R-005).
func NewRouter(
	pool *pgxpool.Pool,
	defRepo *definition.Repository,
	instSvc *instance.Service,
	dispatchMode string,
) http.Handler {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.RealIP)
	r.Use(obs.TraceparentMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(FormatGuardMiddleware)
	r.Use(prometheusMiddleware)
	r.Use(loggingMiddleware)

	defH := NewDefinitionHandlers(defRepo)
	instH := NewInstanceHandlers(instSvc)
	jobH := NewJobHandlers(pool, instSvc)
	utH := NewUserTaskHandlers(instSvc)
	sigH := NewSignalHandlers(instSvc)

	// Definitions
	r.Post("/v1/definitions", defH.Upload)
	r.Get("/v1/definitions", defH.List)
	r.Get("/v1/definitions/{id}", defH.GetLatest)
	r.Get("/v1/definitions/{id}/versions/{version}", defH.GetVersion)

	// Instances
	r.Post("/v1/instances", instH.Start)
	r.Get("/v1/instances/{id}", instH.Get)
	r.Get("/v1/instances/{id}/history", instH.GetHistory)
	r.Post("/v1/instances/{id}/cancel", instH.Cancel)

	// Jobs
	if dispatchMode == "kafka_outbox" {
		r.Post("/v1/jobs/poll", pollDisabledHandler)
	} else {
		r.Post("/v1/jobs/poll", jobH.Poll)
	}
	r.Post("/v1/jobs/{id}/complete", jobH.Complete)
	r.Post("/v1/jobs/{id}/fail", jobH.Fail)

	// User tasks — completed by stable step id from the workflow definition.
	r.Post("/v1/instances/{instanceId}/user-tasks/{userTaskId}/complete", utH.CompleteByStableID)

	// Wait-step signals.
	r.Post("/v1/instances/{instanceId}/signals/{waitStepId}", sigH.Signal)

	// Health probe
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return r
}

// MetricsHandler returns the Prometheus HTTP handler, intended for a
// dedicated metrics port (default :9090).
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

func MetricsHandlerFor(reg prometheus.Gatherer) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

// pollDisabledHandler replies 410 Gone on POST /v1/jobs/poll when the engine
// runs in kafka_outbox mode. 410 is the correct status for an endpoint that
// is deliberately, permanently off in this configuration; workers that hit
// it have not been updated to consume from the broker.
func pollDisabledHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusGone)
	_, _ = w.Write([]byte(`{"error":"poll path disabled","dispatch_mode":"kafka_outbox","hint":"consume directly from Kafka topic workflow.jobs.<jobType>"}`))
}
