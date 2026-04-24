//go:build integration

package kafka_outbox_test

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
)

// TestPollingModeMetrics — US2 Acceptance #3. In polling mode, assert that
// polling metrics are present and kafka_outbox metrics are absent.
func TestPollingModeMetrics(t *testing.T) {
	ctx := context.Background()
	// No fixture needed; this test only checks the global Prometheus registry.

	// 1. Start a polling runtime.
	rt := polling.New()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("runtime start: %v", err)
	}
	defer func() { _ = rt.Stop(ctx) }()

	// 2. Gather all registered metrics.
	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	names := make(map[string]bool)
	for _, mf := range metricFamilies {
		t.Logf("Found metric: %s", mf.GetName())
		names[mf.GetName()] = true
	}

	// 3. Assert polling metrics are present.
	// (Per obs/metrics.go package init)
	pollingRequired := []string{
		"engine_job_pickup_latency_seconds",
		"job_poll_latency_seconds",
		"job_lock_conflicts_total",
	}
	for _, name := range pollingRequired {
		if !names[name] {
			t.Errorf("polling metric missing: %s", name)
		}
	}

	// 4. Assert kafka_outbox metrics are absent.
	// None should start with workflow_engine_dispatch_
	for name := range names {
		if len(name) > 25 && name[:25] == "workflow_engine_dispatch_" {
			t.Errorf("forbidden kafka_outbox metric found in polling mode: %s", name)
		}
	}
}
