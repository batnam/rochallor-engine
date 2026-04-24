//go:build integration

package kafka_outbox_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/api/rest"
)

// TestPollDisabled — US1 Acceptance + FR-004 / R-005. In kafka_outbox mode
// POST /v1/jobs/poll MUST return 410 Gone with a body naming the current
// dispatch mode. This is the loud, actionable signal for operators who
// flipped the switch but forgot to update their workers.
func TestPollDisabled(t *testing.T) {
	// No containers needed — router composition is a pure unit test.
	router := rest.NewRouter(nil, nil, nil, "kafka_outbox")

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/poll", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status: want %d, got %d", http.StatusGone, rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "kafka_outbox") {
		t.Errorf("response body must name dispatch mode, got %q", body)
	}
	if !strings.Contains(body, "poll path disabled") {
		t.Errorf("response body must name the condition, got %q", body)
	}
}

// TestPollEnabledInPollingMode — sanity check. The same router in polling
// mode must route /v1/jobs/poll to the real handler (not 410). We can't
// fully exercise the handler without a DB pool, but we can verify the
// mode gating itself by asserting the status is NOT 410.
func TestPollEnabledInPollingMode(t *testing.T) {
	router := rest.NewRouter(nil, nil, nil, "polling")

	req := httptest.NewRequest(http.MethodPost, "/v1/jobs/poll", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusGone {
		t.Fatalf("polling mode must not return 410 on /v1/jobs/poll, got %d with body %q", rec.Code, rec.Body.String())
	}
	// Any other status is acceptable — the real handler will fail on nil
	// pool, but that's a different code path (500/panic recovered by chi
	// Recoverer). The point is: we did not hit the kafka_outbox-gated 410.
}
