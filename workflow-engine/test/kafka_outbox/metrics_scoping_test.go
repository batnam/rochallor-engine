//go:build integration

package kafka_outbox_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/api/rest"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/config"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/obs"
	"github.com/prometheus/client_golang/prometheus"
)

func TestObservability_ModeSpecificMetrics(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	defer f.Close(ctx)

	// Since prometheus.DefaultRegisterer is global, we might have interference 
	// if other tests already registered metrics.
	// But our ensureMetricsRegistered uses sync.Once.

	// We'll use a custom registry for PollingMode test to be isolated.
	reg := prometheus.NewRegistry()

	// 1. Polling mode
	t.Run("PollingMode", func(t *testing.T) {
		obs.RegisterPollingMetrics(reg)

		// Scrape
		metrics := scrape(t, reg)

		assertContains(t, metrics, "job_poll_latency_seconds")
		assertContains(t, metrics, "job_lock_conflicts_total")
		assertContains(t, metrics, "engine_job_pickup_latency_seconds")

		assertNotContains(t, metrics, "workflow_engine_dispatch_outbox_backlog")
		assertNotContains(t, metrics, "workflow_engine_dispatch_relay_publish_total")
	})

	// 2. Kafka Outbox mode
	t.Run("KafkaMode", func(t *testing.T) {
		// kafka_outbox metrics are registered via ensureMetricsRegistered()
		// which uses promauto.New... against prometheus.DefaultRegisterer.

		rt := kafka_outbox.New(kafka_outbox.Config{
			Pool:        f.Pool,
			SeedBrokers: f.SeedBrokers,
			Transport:   config.KafkaTransportPlaintext,
		})
		if err := rt.Start(ctx); err != nil {
			t.Fatalf("start runtime: %v", err)
		}
		defer rt.Stop(ctx)

		metrics := scrape(t, prometheus.DefaultGatherer)

		assertContains(t, metrics, "workflow_engine_dispatch_outbox_backlog")
		assertContains(t, metrics, "workflow_engine_dispatch_relay_leader")

		// Verify polling metrics are ABSENT (FR-010, T040)
		assertNotContains(t, metrics, "job_poll_latency_seconds")
		assertNotContains(t, metrics, "job_lock_conflicts_total")
		assertNotContains(t, metrics, "engine_job_pickup_latency_seconds")
	})
}

func scrape(t *testing.T, reg prometheus.Gatherer) string {
	handler := rest.MetricsHandlerFor(reg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	handler.ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("scrape failed with status %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	return string(body)
}

func assertContains(t *testing.T, s, substr string) {
	if !strings.Contains(s, substr) {
		t.Errorf("expected metrics to contain %q", substr)
	}
}

func assertNotContains(t *testing.T, s, substr string) {
	if strings.Contains(s, substr) {
		t.Errorf("expected metrics NOT to contain %q", substr)
	}
}
