//go:build integration

package kafka_outbox_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
)

// TestPollingModeBoot - Start in polling mode and assert
// no Kafka-related background work is started.
func TestPollingModeBoot(t *testing.T) {
	ctx := context.Background()
	f := newPostgresFixture(t)
	t.Cleanup(func() { f.Close(ctx) })

	// Start a polling runtime.
	rt := polling.New()
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("runtime start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Stop(ctx) })

	// 1. Assert no advisory lock is held for the relay key.
	// The key is hashtext('workflow-engine.dispatch_relay')::bigint.
	var lockCount int
	err := f.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_locks
		WHERE locktype = 'advisory'
		  AND objid = (SELECT hashtext('workflow-engine.dispatch_relay')::bigint)
	`).Scan(&lockCount)
	if err != nil {
		t.Fatalf("query pg_locks: %v", err)
	}
	if lockCount > 0 {
		t.Errorf("advisory lock count: got %d, want 0", lockCount)
	}

	// 2. Assert no relay or kafka-producer goroutines are running.
	// This is a coarse check but effective for catching leak regressions.
	buf := make([]byte, 1024*1024)
	n := runtime.Stack(buf, true)
	stacks := string(buf[:n])

	forbidden := []string{
		"kafka_outbox.(*relay)",
		"franz-go",
		"kafka_outbox.(*Producer)",
	}
	for _, s := range forbidden {
		if strings.Contains(stacks, s) {
			t.Errorf("found forbidden goroutine stack: %s", s)
		}
	}
}
