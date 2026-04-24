package rest

import (
	"net/http"
	"strings"

	engineapi "github.com/batnam/rochallor-engine/workflow-engine/internal/api"
)

// FormatGuardMiddleware rejects requests with unsupported content types at the router level.
// It intercepts POST /v1/definitions when Content-Type is application/xml or text/xml,
// returning HTTP 415.
func FormatGuardMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/definitions") {
			ct := r.Header.Get("Content-Type")
			if strings.Contains(ct, "application/xml") || strings.Contains(ct, "text/xml") {
				engineapi.WriteUnsupportedFormat(w,
					"Only JSON workflow definitions are accepted")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
