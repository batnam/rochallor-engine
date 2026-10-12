package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadinessLifecycleAndDatabase(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	var unavailable atomic.Bool
	ready := NewReadiness(ctx, func(context.Context) error {
		calls.Add(1)
		if unavailable.Load() {
			return errors.New("private database failure")
		}
		return nil
	})
	router := NewRouter(nil, nil, nil, nil, nil, "polling", ready)
	probe := func(path string, expected int) {
		t.Helper()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != expected {
			t.Fatalf("%s: got %d: %s", path, recorder.Code, recorder.Body.String())
		}
		if path == "/readyz" && recorder.Body.String() != "{\"status\":\"ready\"}\n" && recorder.Body.String() != "{\"status\":\"not_ready\"}\n" {
			t.Fatalf("unexpected readiness response: %s", recorder.Body.String())
		}
	}
	probe("/readyz", 503)
	probe("/healthz", 200)
	if calls.Load() != 0 {
		t.Fatal("startup must not probe an uninitialized database")
	}
	ready.MarkReady()
	probe("/readyz", 200)
	unavailable.Store(true)
	probe("/readyz", 503)
	probe("/healthz", 200)
	unavailable.Store(false)
	probe("/readyz", 200)
	cancel()
	probe("/readyz", 503)
	probe("/healthz", 200)
}

func TestReadinessDatabaseDeadline(t *testing.T) {
	ready := NewReadiness(context.Background(), func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Error("database probe has no short deadline")
		}
		<-ctx.Done()
		return ctx.Err()
	})
	ready.MarkReady()
	recorder := httptest.NewRecorder()
	start := time.Now()
	ready.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != 503 || time.Since(start) > time.Second {
		t.Fatalf("unbounded readiness: %d %s", recorder.Code, time.Since(start))
	}
}
