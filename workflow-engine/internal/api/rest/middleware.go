package rest

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
)

// prometheusMiddleware records HTTPRequestDuration for every request.
func prometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		elapsed := time.Since(start).Seconds()

		path := r.URL.Path
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			if p := rctx.RoutePattern(); p != "" {
				path = p
			}
		}

		obs.HTTPRequestDuration.WithLabelValues(
			r.Method,
			path,
			strconv.Itoa(ww.Status()),
		).Observe(elapsed)
	})
}

// restLogSkipPaths lists chi route patterns whose requests are suppressed
// from the access log. Workers poll /v1/jobs/poll in a tight loop — logging
// every hit drowns out real traffic.
var restLogSkipPaths = map[string]struct{}{
	"/v1/jobs/poll": {},
}

// loggingMiddleware emits one structured log line per HTTP request at the
// end of the chain. Runs after route matching so `path` is the chi route
// pattern (e.g. /v1/instances/{id}) rather than the concrete URL, which
// keeps log cardinality bounded.
//
// Paths listed in restLogSkipPaths are silenced on 2xx/3xx but still log on
// failures (4xx/5xx), so real problems on the poll endpoint are still visible.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)

		path := r.URL.Path
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			if p := rctx.RoutePattern(); p != "" {
				path = p
			}
		}

		status := ww.Status()
		if _, skip := restLogSkipPaths[path]; skip && status < 400 {
			return
		}

		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}

		obs.FromContext(r.Context()).LogAttrs(r.Context(), level, "rest request",
			slog.String("method", r.Method),
			slog.String("path", path),
			slog.Int("status", status),
			slog.Int("bytes", ww.BytesWritten()),
			slog.Duration("duration", time.Since(start)),
			slog.String("remote", r.RemoteAddr),
		)
	})
}
