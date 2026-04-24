// Package kafka_outbox_test holds the integration-test suite for feature
// 006-kafka-outbox-dispatch. All tests in this package spin up Postgres +
// Redpanda via testcontainers and exercise the real engine binaries end to
// end. The suite is `//go:build integration`-gated so `go test ./...` in
// environments without Docker stays green.
//
//go:build integration

package kafka_outbox_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredpanda "github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	pgstore "github.com/batnam/rochallor-engine/workflow-engine/internal/storage/postgres"
)

// testFixture is the shared container bundle each integration test spins up.
// Close() tears down both containers; callers typically t.Cleanup(fixture.Close)
// immediately after a successful startup.
type testFixture struct {
	Pool        *pgxpool.Pool
	SeedBrokers string

	pgContainer *tcpostgres.PostgresContainer
	rpContainer *tcredpanda.Container
}

// Close terminates both containers. Idempotent.
func (f *testFixture) Close(ctx context.Context) {
	if f.Pool != nil {
		f.Pool.Close()
		f.Pool = nil
	}
	if f.pgContainer != nil {
		_ = testcontainers.TerminateContainer(f.pgContainer)
		f.pgContainer = nil
	}
	if f.rpContainer != nil {
		_ = testcontainers.TerminateContainer(f.rpContainer)
		f.rpContainer = nil
	}
}

// newFixture starts Postgres + Redpanda, applies the engine's migrations
// (including 0009_dispatch_outbox), and optionally pre-creates Kafka topics
// for the given job types.
//
// Callers MUST call f.Close(ctx) in t.Cleanup.
func newFixture(t *testing.T, jobTypes ...string) *testFixture {
	t.Helper()
	ctx := context.Background()
	f := &testFixture{}

	// ── Postgres ──────────────────────────────────────────────────────────
	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("workflow"),
		tcpostgres.WithUsername("workflow"),
		tcpostgres.WithPassword("workflow"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	f.pgContainer = pg
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		f.Close(ctx)
		t.Fatalf("postgres DSN: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		f.Close(ctx)
		t.Fatalf("pgxpool: %v", err)
	}
	f.Pool = pool
	if err := pgstore.Migrate(pool); err != nil {
		f.Close(ctx)
		t.Fatalf("migrate: %v", err)
	}

	// ── Redpanda ──────────────────────────────────────────────────────────
	rp, err := tcredpanda.Run(ctx, "docker.redpanda.com/redpandadata/redpanda:v24.2.7")
	if err != nil {
		f.Close(ctx)
		t.Fatalf("start redpanda: %v", err)
	}
	f.rpContainer = rp
	seed, err := rp.KafkaSeedBroker(ctx)
	if err != nil {
		f.Close(ctx)
		t.Fatalf("redpanda seed broker: %v", err)
	}
	f.SeedBrokers = seed

	// Pre-create topics for each requested job type so the relay can publish.
	if len(jobTypes) > 0 {
		if err := createTopics(ctx, seed, jobTypes); err != nil {
			f.Close(ctx)
			t.Fatalf("create topics: %v", err)
		}
	}

	return f
}

// newPostgresFixture starts only Postgres and applies migrations.
// Used for US2 polling-regression tests that must not touch Kafka.
func newPostgresFixture(t *testing.T) *testFixture {
	t.Helper()
	ctx := context.Background()
	f := &testFixture{}

	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("workflow"),
		tcpostgres.WithUsername("workflow"),
		tcpostgres.WithPassword("workflow"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	f.pgContainer = pg
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		f.Close(ctx)
		t.Fatalf("postgres DSN: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		f.Close(ctx)
		t.Fatalf("pgxpool: %v", err)
	}
	f.Pool = pool
	if err := pgstore.Migrate(pool); err != nil {
		f.Close(ctx)
		t.Fatalf("migrate: %v", err)
	}

	return f
}

// createTopics makes each `workflow.jobs.<jobType>` topic on the broker.
func createTopics(ctx context.Context, seedBroker string, jobTypes []string) error {
	client, err := kgo.NewClient(kgo.SeedBrokers(seedBroker))
	if err != nil {
		return fmt.Errorf("kgo client: %w", err)
	}
	defer client.Close()
	admin := kadm.NewClient(client)
	names := make([]string, 0, len(jobTypes))
	for _, jt := range jobTypes {
		names = append(names, "workflow.jobs."+jt)
	}
	// 1 partition + rf=1 is enough for single-broker tests.
	_, err = admin.CreateTopics(ctx, 1, 1, nil, names...)
	return err
}

// waitFor polls `check` every 50ms until it returns true or until timeout.
// Test helper; failures are fatal on the calling test.
func waitFor(t *testing.T, timeout time.Duration, msg string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s after %s", msg, timeout)
}

// ulidlike returns a 20-char lowercase alphanumeric string suitable for
// stand-in unique IDs in tests. Not a real ULID; just needs to be unique
// across a single test invocation.
func ulidlike() string {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	t := time.Now().UnixNano()
	buf := make([]byte, 20)
	for i := range buf {
		buf[i] = alphabet[t&0x1f]
		t >>= 5
		if t == 0 {
			t = time.Now().UnixNano()
		}
	}
	return string(buf)
}
