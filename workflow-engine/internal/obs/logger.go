// Package obs bundles the Engine's observability primitives: structured logging
// (slog/JSON) and Prometheus metrics. Both are initialised once at startup and
// shared across packages via package-level variables.
package obs

import (
	"context"
	"log/slog"
	"net/http"
	"os"
)

// contextLoggerKey is the key used to store a per-request *slog.Logger in context.
type contextLoggerKey struct{}

// InitLogger creates a JSON slog handler at the requested level and installs it
// as the default logger for the process. Call once at startup before any
// log output is produced.
func InitLogger(level string) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	slog.SetDefault(slog.New(h))
}

// traceparentKey is the W3C Trace Context header name.
const traceparentKey = "traceparent"

// TraceparentMiddleware extracts the W3C traceparent header from inbound HTTP
// requests and stores a request-scoped *slog.Logger (with the traceparent
// attached) into the request context (per R-013).
func TraceparentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tp := r.Header.Get(traceparentKey); tp != "" {
			logger := slog.Default().With(slog.String("traceparent", tp))
			ctx := context.WithValue(r.Context(), contextLoggerKey{}, logger)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// FromContext returns the per-request logger stored by TraceparentMiddleware,
// or the default global logger if none was set.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(contextLoggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// FromRequest is a convenience wrapper around FromContext.
func FromRequest(r *http.Request) *slog.Logger {
	return FromContext(r.Context())
}
