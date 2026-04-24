//go:build load

// lifecycle_perf_test.go is the SC-001 load-test harness for feature
// 005-engine-performance-optimization.
//
// It is gated behind the `load` build tag so it never runs under `go test ./...`.
// Run it via:
//
//	WE_POSTGRES_DSN=postgres://... \
//	go test -tags=load -run TestSignalThroughputBaseline \
//	    -count=1 -timeout=5m ./workflow-engine/internal/instance
//
// The harness:
//   - connects to the Postgres DSN in WE_POSTGRES_DSN
//   - runs migrations (idempotent)
//   - uploads a definition containing a long chain of WAIT steps
//   - starts one workflow instance
//   - fans out N concurrent goroutines (default 64; override with
//     SC001_CONCURRENCY) that repeatedly signal the instance's first current
//     WAIT step for SC001_DURATION (default 60s)
//
// On completion it prints exactly one parseable line of plain text to stdout:
//
//	signals_processed_per_sec=<float>
//
// Capture the value on `main` (T022a) and on the feature branch (T022b) on the
// same rig to validate the ≥ 1.5× ratio required by SC-001.
package instance

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/polling"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/expression"
	pgstore "github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

// Chain length must be large enough that concurrency × duration of signals
// cannot drain it. At an optimistic 10k signals/sec that is 600k — we pick 50k
// and expect the harness to stop on the duration budget, not on END.
const sc001ChainLength = 50000

func TestSignalThroughputBaseline(t *testing.T) {
	dsn := os.Getenv("WE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SC-001 harness requires WE_POSTGRES_DSN to point at a load-test database")
	}

	concurrency := envInt("SC001_CONCURRENCY", 64)
	duration := envDuration("SC001_DURATION", 60*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), duration+2*time.Minute)
	defer cancel()

	pool, err := pgstore.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("pgstore.NewPool: %v", err)
	}
	defer pool.Close()

	if err := pgstore.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	SetExpressionEvaluator(expression.Evaluate)
	defRepo := definition.NewRepository(pool)
	svc := NewService(pool, defRepo, polling.Dispatcher{})

	def := buildWaitChainDefinition(sc001ChainLength)
	if _, err := defRepo.Upload(ctx, def); err != nil {
		t.Fatalf("definition upload: %v", err)
	}

	inst, err := svc.Start(ctx, def.ID, def.Version, nil, "")
	if err != nil {
		t.Fatalf("start instance: %v", err)
	}

	var signalsOK atomic.Int64
	start := time.Now()
	deadline := start.Add(duration)

	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				step, ok := peekCurrentWaitStep(ctx, pool, inst.ID)
				if !ok {
					return // instance has advanced past all WAITs or is terminal
				}
				err := svc.SignalWaitAndAdvance(ctx, inst.ID, step, nil)
				if err == nil {
					signalsOK.Add(1)
					continue
				}
				// ErrWaitStepNotParked is expected under contention: another
				// goroutine won the FOR UPDATE race for this step. Loop and
				// re-peek rather than backing off.
			}
		}()
	}
	wg.Wait()

	elapsed := time.Since(start).Seconds()
	rate := float64(signalsOK.Load()) / elapsed

	// Exactly one parseable line — T022a/T022b parse on "=" split.
	fmt.Printf("signals_processed_per_sec=%.3f\n", rate)
	t.Logf("concurrency=%d duration=%s total_signals=%d", concurrency, duration, signalsOK.Load())
}

// buildWaitChainDefinition constructs a definition with n sequential WAIT
// steps followed by an END. Each signal advances one hop; N concurrent
// senders serialize on the workflow_instance FOR UPDATE.
func buildWaitChainDefinition(n int) *definition.WorkflowDefinition {
	steps := make([]definition.WorkflowStep, 0, n+1)
	for i := 0; i < n; i++ {
		stepID := fmt.Sprintf("wait-%05d", i)
		next := fmt.Sprintf("wait-%05d", i+1)
		if i == n-1 {
			next = "end"
		}
		steps = append(steps, definition.WorkflowStep{
			ID:       stepID,
			Name:     stepID,
			Type:     definition.StepTypeWait,
			NextStep: next,
		})
	}
	steps = append(steps, definition.WorkflowStep{
		ID:   "end",
		Name: "end",
		Type: definition.StepTypeEnd,
	})
	return &definition.WorkflowDefinition{
		ID:    "sc001::wait-chain",
		Name:  "SC-001 wait chain",
		Steps: steps,
	}
}

// peekCurrentWaitStep returns the instance's first current_step_ids entry,
// or ok=false if the instance has no remaining WAIT parked.
func peekCurrentWaitStep(ctx context.Context, pool *pgxpool.Pool, instanceID string) (string, bool) {
	var current []string
	var status string
	err := pool.QueryRow(ctx,
		`SELECT status, current_step_ids FROM workflow_instance WHERE id = $1`,
		instanceID,
	).Scan(&status, &current)
	if err != nil {
		return "", false
	}
	if status != string(InstanceStatusActive) && status != string(InstanceStatusWaiting) {
		return "", false
	}
	if len(current) == 0 {
		return "", false
	}
	return current[0], true
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
