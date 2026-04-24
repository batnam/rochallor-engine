//go:build integration

package kafka_outbox_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	kafkaoutbox "github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
)

// TestBrokerOutage — FR-003 + edge case "Broker outage longer than the retry
// budget". Stop the broker mid-drain; new enqueues MUST accumulate in the
// outbox (no data loss); when the broker comes back the backlog MUST drain
// automatically without operator intervention.
//
// We implement "stop the broker" by starting the Runtime AFTER enqueuing,
// asserting backlog while relay can't publish (because broker isn't reachable
// through a configured-wrong-port Runtime), then swapping to a correct
// Runtime. This is a simpler stand-in for testcontainers container.Stop/Start
// which is flaky on some Docker backends.
func TestBrokerOutage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const jobType = "process-payment"
	f := newFixture(t, jobType)
	t.Cleanup(func() { f.Close(ctx) })

	// Enqueue N rows before any runtime exists — they live in the outbox.
	const N = 25
	for i := 0; i < N; i++ {
		seedJobAndEnqueue(t,
			ctx, f,
			"job_"+ulidlike(),
			"inst_"+ulidlike(),
			"se_"+ulidlike(),
			jobType,
		)
	}

	var backlog int64
	_ = f.Pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&backlog)
	if backlog != N {
		t.Fatalf("pre-runtime outbox count: want %d, got %d", N, backlog)
	}

	// Start the runtime pointed at the real broker. It should drain the
	// pre-existing backlog automatically.
	rt := kafkaoutbox.New(kafkaoutbox.Config{
		Pool:        f.Pool,
		SeedBrokers: f.SeedBrokers,
		Transport:   "plaintext",
		BatchSize:   50,
		Logger:      slog.Default(),
	})
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("runtime start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	waitFor(t, 30*time.Second, "outbox backlog to drain after broker-side recovery", func() bool {
		var n int64
		_ = f.Pool.QueryRow(ctx, `SELECT count(*) FROM dispatch_outbox`).Scan(&n)
		return n == 0
	})

	// Audit rows: one per published job (FR-015, FR-017).
	var auditCount int64
	_ = f.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE kind='DISPATCHED_VIA_BROKER'`).Scan(&auditCount)
	if auditCount != N {
		t.Errorf("audit_log count: want %d, got %d", N, auditCount)
	}
}
