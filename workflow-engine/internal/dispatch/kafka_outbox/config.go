package kafka_outbox

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
)

// Config is the dependency bundle passed into New(). The fields mirror the
// WE_KAFKA_* env var surface defined in config.KafkaConfig, plus the engine
// DB handles and the logger.
//
// Secrets (SASLPassword) arrive via the env-only path in config.Load and are
// carried through here; they MUST never be logged.
type Config struct {
	// DB is the storage abstraction used by the relay to open the
	// claim/publish/delete transaction via RunInTx.
	DB db.DB

	// Store is the dispatch_outbox + audit_log repository. The hot-path
	// Dispatcher.Enqueue and the relay's drain cycle delegate all SQL
	// through this interface — no raw pgx access in this package.
	Store OutboxStore

	// Pool is retained only for the advisory-lock leader-election loop
	// (advisory_lock.go), which hijacks a dedicated connection to hold a
	// session-scoped lock and ping it for liveness. All outbox SQL —
	// including the migration-applied startup check — goes through Store.
	Pool *pgxpool.Pool

	// SeedBrokers is a comma-separated list of host:port pairs.
	SeedBrokers string

	// JobTypes is the list of every job_type present in the system.
	// Used to validate Kafka topics at startup.
	JobTypes []string

	// Transport selects the wire security posture: "plaintext" or

	// "sasl_scram_tls". Values are the same constants exported by
	// package config (KafkaTransportPlaintext / KafkaTransportSASLScramTLS).
	Transport string

	// SASL credentials — required only when Transport == sasl_scram_tls.
	SASLMechanism string // "SCRAM-SHA-256" or "SCRAM-SHA-512"
	SASLUsername  string
	SASLPassword  string

	// TLS overrides — both optional; sane defaults apply when empty.
	TLSCAPath     string
	TLSServerName string

	// BatchSize caps the relay drain query (rows per iteration). Zero falls
	// back to a safe default.
	BatchSize int

	// Logger is the slog logger used by the runtime. Nil is tolerated
	// (defaults to slog.Default).
	Logger *slog.Logger
}

const (
	defaultBatchSize = 200
	// relayIdleInterval is how long the relay sleeps between drain attempts
	// when the last batch returned zero rows. Short enough to keep latency
	// low, long enough that an idle engine doesn't hammer Postgres.
	defaultIdleInterval = 250 // milliseconds
	// leaderRetryInterval is how often non-leader replicas re-attempt the
	// advisory lock acquire. 5s matches.
	defaultLeaderRetryInterval = 5 // seconds
)
