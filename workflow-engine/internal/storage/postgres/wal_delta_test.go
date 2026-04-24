//go:build load

// wal_delta_test.go is the SC-002 WAL-delta harness for feature
// 005-engine-performance-optimization.
//
// Gated behind the `load` build tag so it never runs under `go test ./...`.
// Run it via:
//
//	WE_POSTGRES_DSN=postgres://... \
//	go test -tags=load -run TestJSONBSetWALDelta \
//	    -count=1 -timeout=3m ./workflow-engine/internal/storage/postgres
//
// Seeds one workflow_instance with a `variables` JSONB ≥ SC002_PAYLOAD_KB
// (default 256), performs a full-row rewrite and a jsonb_set partial update
// of the same single key on independent rows, and compares the
// pg_current_wal_lsn() delta. Asserts ≥ 40% reduction (SC-002).
package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pgstore "github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

func TestJSONBSetWALDelta(t *testing.T) {
	dsn := os.Getenv("WE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SC-002 harness requires WE_POSTGRES_DSN to point at a load-test database")
	}

	payloadKB := envInt("SC002_PAYLOAD_KB", 256)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgstore.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("pgstore.NewPool: %v", err)
	}
	defer pool.Close()

	if err := pgstore.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Two rows so each measurement starts from the same "freshly-toasted" state.
	id1 := fmt.Sprintf("sc002-full-%d", time.Now().UnixNano())
	id2 := fmt.Sprintf("sc002-set-%d", time.Now().UnixNano())

	seed := buildSeedVariables(payloadKB)
	seedJSON, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}

	for _, id := range []string{id1, id2} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO workflow_instance (id, definition_id, definition_version, status, variables)
			 VALUES ($1, 'sc002::fixture', 1, 'ACTIVE', $2::jsonb)
			 ON CONFLICT (id) DO UPDATE SET variables = EXCLUDED.variables`,
			id, string(seedJSON),
		); err != nil {
			t.Fatalf("seed instance %s: %v", id, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM workflow_instance WHERE id IN ($1,$2)`, id1, id2)
	})

	fullDelta, err := measureWAL(ctx, pool, func() error {
		next := mutatedCopy(seed, "dirty_key", "new_value")
		b, _ := json.Marshal(next)
		_, err := pool.Exec(ctx,
			`UPDATE workflow_instance SET variables = $1::jsonb WHERE id = $2`,
			string(b), id1,
		)
		return err
	})
	if err != nil {
		t.Fatalf("full-rewrite measurement: %v", err)
	}

	setDelta, err := measureWAL(ctx, pool, func() error {
		_, err := pool.Exec(ctx,
			`UPDATE workflow_instance
			   SET variables = jsonb_set(variables, ARRAY['dirty_key'], $1::jsonb, true)
			 WHERE id = $2`,
			`"new_value"`, id2,
		)
		return err
	})
	if err != nil {
		t.Fatalf("jsonb_set measurement: %v", err)
	}

	reduction := 1.0 - float64(setDelta)/float64(fullDelta)
	fmt.Printf("wal_delta_full_rewrite_bytes=%d\n", fullDelta)
	fmt.Printf("wal_delta_jsonb_set_bytes=%d\n", setDelta)
	fmt.Printf("wal_reduction_fraction=%.4f\n", reduction)

	if fullDelta <= 0 || setDelta <= 0 {
		t.Fatalf("WAL delta non-positive: full=%d set=%d — check background activity on the instance", fullDelta, setDelta)
	}
	if reduction < 0.40 {
		t.Fatalf("SC-002 FAIL: WAL reduction %.2f%% < 40%% target", reduction*100)
	}
	t.Logf("SC-002 PASS: WAL reduction %.2f%% >= 40%% target", reduction*100)
}

// measureWAL returns the byte difference of pg_current_wal_lsn() around fn,
// computed via pg_wal_lsn_diff.
func measureWAL(ctx context.Context, pool *pgxpool.Pool, fn func() error) (int64, error) {
	var beforeLSN string
	if err := pool.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&beforeLSN); err != nil {
		return 0, fmt.Errorf("before lsn: %w", err)
	}
	if err := fn(); err != nil {
		return 0, fmt.Errorf("mutation: %w", err)
	}
	var delta int64
	if err := pool.QueryRow(ctx,
		`SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), $1::pg_lsn)::bigint`,
		beforeLSN,
	).Scan(&delta); err != nil {
		return 0, fmt.Errorf("delta: %w", err)
	}
	return delta, nil
}

// buildSeedVariables builds a map whose JSON representation is at least
// payloadKB kilobytes. A single large padding string drives the size past
// Postgres' TOAST threshold where full-rewrite write amplification dominates.
func buildSeedVariables(payloadKB int) map[string]any {
	target := payloadKB * 1024
	return map[string]any{
		"dirty_key":   "initial",
		"constant_a":  "keep",
		"constant_b":  42,
		"constant_c":  []int{1, 2, 3, 4, 5},
		"padded_blob": strings.Repeat("x", target),
	}
}

func mutatedCopy(src map[string]any, key string, val any) map[string]any {
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	out[key] = val
	return out
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
