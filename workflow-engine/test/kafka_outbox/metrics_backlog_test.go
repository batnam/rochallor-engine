//go:build integration

package kafka_outbox_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/api/rest"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/config"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
	"github.com/prometheus/client_golang/prometheus"
)

func TestMetrics_Backlog(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const jobType = "process-payment-metrics"
	f := newFixture(t, jobType)
	t.Cleanup(func() { f.Close(ctx) })

	// Start a runtime with a bad broker port to simulate a paused broker
	rt := kafka_outbox.New(kafka_outbox.Config{
		Pool:        f.Pool,
		SeedBrokers: "localhost:9099", // Bad port
		Transport:   config.KafkaTransportPlaintext,
		BatchSize:   50,
	})
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("runtime start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	// Enqueue N rows
	const N = 5
	for i := 0; i < N; i++ {
		seedJobAndEnqueue(t,
			ctx, f,
			"job_metrics_"+ulidlike(),
			"inst_metrics_"+ulidlike(),
			"se_metrics_"+ulidlike(),
			jobType,
		)
	}

	waitFor(t, 60*time.Second, "outbox backlog gauge to reach N", func() bool {
		metrics := scrapeMetrics(t, prometheus.DefaultGatherer)
		// We expect the gauge to report N
		return strings.Contains(metrics, "workflow_engine_dispatch_outbox_backlog 5")
	})

	// Now stop the bad runtime and start a good one
	_ = rt.Stop(context.Background())

	goodRt := kafka_outbox.New(kafka_outbox.Config{
		Pool:        f.Pool,
		SeedBrokers: f.SeedBrokers,
		Transport:   config.KafkaTransportPlaintext,
		BatchSize:   50,
	})
	if err := goodRt.Start(ctx); err != nil {
		t.Fatalf("good runtime start: %v", err)
	}
	t.Cleanup(func() { _ = goodRt.Stop(context.Background()) })

	waitFor(t, 60*time.Second, "outbox backlog gauge to drain to 0", func() bool {
		metrics := scrapeMetrics(t, prometheus.DefaultGatherer)
		return strings.Contains(metrics, "workflow_engine_dispatch_outbox_backlog 0")
	})
}

func scrapeMetrics(t *testing.T, reg prometheus.Gatherer) string {
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
