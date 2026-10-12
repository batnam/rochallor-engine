package rest

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// Readiness separates dependency/lifecycle health from process liveness.
// Kafka connectivity is deliberately not checked: an initialized runtime can
// keep accepting durable outbox writes while the broker is temporarily down.
type Readiness struct {
	lifecycle context.Context
	ping      func(context.Context) error
	started   atomic.Bool
}

func NewReadiness(lifecycle context.Context, ping func(context.Context) error) *Readiness {
	return &Readiness{lifecycle: lifecycle, ping: ping}
}

// MarkReady is called only after migrations, dispatch initialization and all
// listener binds succeed. Cancellation of lifecycle immediately revokes it.
func (h *Readiness) MarkReady() { h.started.Store(true) }

func (h *Readiness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ready := h.started.Load() && h.lifecycle.Err() == nil
	if ready {
		ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
		defer cancel()
		ready = h.ping(ctx) == nil && ctx.Err() == nil && h.lifecycle.Err() == nil
	}
	if !ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
