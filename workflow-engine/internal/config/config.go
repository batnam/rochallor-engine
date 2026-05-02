// Package config loads Engine configuration from environment variables,
// with an optional YAML file fallback at /etc/workflow/engine.yaml
// for local development.
//
// Priority: env vars > YAML file > built-in defaults.
// Secrets (e.g. WE_POSTGRES_DSN) are always read from the environment;
// they are never written to a YAML file.
package config

import (
	"fmt"
	"os"
	"runtime"
	"strconv"

	"gopkg.in/yaml.v3"
)

const defaultYAMLPath = "/etc/workflow/engine.yaml"

// Config holds all runtime configuration for the workflow engine.
type Config struct {
	// PostgresDSN is the libpq-compatible connection string.
	// Environment variable: WE_POSTGRES_DSN (required).
	PostgresDSN string `yaml:"postgresDSN"`

	// RESTPort is the port the HTTP/REST listener binds to.
	// Environment variable: WE_REST_PORT (default 8080).
	RESTPort int `yaml:"restPort"`

	// GRPCPort is the port the gRPC server binds to.
	// Environment variable: WE_GRPC_PORT (default 9090).
	GRPCPort int `yaml:"grpcPort"`

	// MetricsPort is the port Prometheus /metrics is exposed on.
	// Environment variable: WE_METRICS_PORT (default 9091).
	MetricsPort int `yaml:"metricsPort"`

	// LogLevel is the minimum log level: debug, info, warn, error.
	// Environment variable: WE_LOG_LEVEL (default info).
	LogLevel string `yaml:"logLevel"`

	// AuditLogEnabled controls whether Engine actions are recorded to audit_log.
	// Environment variable: WE_AUDIT_LOG_ENABLED (default true).
	AuditLogEnabled bool `yaml:"auditLogEnabled"`

	// DBMaxConns caps the pgxpool. Environment variable: DB_MAX_CONNS.
	// Default: runtime.NumCPU() * 4, floor 4 (matches historical behaviour).
	DBMaxConns int `yaml:"dbMaxConns"`

	// DBMinConns is the minimum idle connections held by the pool.
	// Environment variable: DB_MIN_CONNS. Default: 0 (pgxpool on-demand).
	DBMinConns int `yaml:"dbMinConns"`

	// DispatchMode selects how newly created jobs are handed to workers.
	// "" or "polling" (default): FOR UPDATE SKIP LOCKED claim path (FR-001).
	// "kafka_outbox": transactional outbox + Kafka event-driven dispatch (FR-002..FR-018).
	// Environment variable: WE_DISPATCH_MODE.
	DispatchMode string `yaml:"dispatchMode"`

	// Kafka holds broker + transport settings. Populated only when
	// DispatchMode == "kafka_outbox"; otherwise ignored.
	// Secrets (SASLPassword) are env-only and never read from YAML.
	Kafka KafkaConfig `yaml:"kafka"`
}

// KafkaConfig holds broker + transport settings for event-driven dispatch mode.
// See contracts/kafka-topics.md §4 for the authoritative env-var matrix.
type KafkaConfig struct {
	// SeedBrokers is a comma-separated list of host:port pairs.
	// Environment variable: WE_KAFKA_SEED_BROKERS.
	SeedBrokers string `yaml:"seedBrokers"`

	// Transport selects the wire security posture:
	//   "plaintext"       — trusted networks / dev.
	//   "sasl_scram_tls"  — production; requires SASL + TLS settings below.
	// Environment variable: WE_KAFKA_TRANSPORT.
	Transport string `yaml:"transport"`

	// SASLMechanism is the SASL/SCRAM mechanism. Valid when Transport =
	// "sasl_scram_tls". One of: "SCRAM-SHA-256", "SCRAM-SHA-512".
	// Environment variable: WE_KAFKA_SASL_MECHANISM. Default: SCRAM-SHA-512.
	SASLMechanism string `yaml:"saslMechanism"`

	// SASLUsername is the SASL principal. Secret — env-only.
	// Environment variable: WE_KAFKA_SASL_USERNAME.
	SASLUsername string `yaml:"-"`

	// SASLPassword is the SASL credential. Secret — env-only, never logged,
	// never read from YAML (mirrors WE_POSTGRES_DSN handling per FR-012).
	// Environment variable: WE_KAFKA_SASL_PASSWORD.
	SASLPassword string `yaml:"-"`

	// TLSCAPath is an optional PEM file path for a custom CA bundle.
	// When empty the system CA bundle is used.
	// Environment variable: WE_KAFKA_TLS_CA_PATH.
	TLSCAPath string `yaml:"tlsCAPath"`

	// TLSServerName is an optional SNI override. When empty the hostname from
	// the first seed broker is used. Environment variable: WE_KAFKA_TLS_SERVER_NAME.
	TLSServerName string `yaml:"tlsServerName"`

	// OutboxBatchSize caps the relay drain query. Default 200.
	// Environment variable: WE_OUTBOX_BATCH_SIZE.
	OutboxBatchSize int `yaml:"outboxBatchSize"`
}

// Valid DispatchMode values.
const (
	DispatchModePolling     = "polling"
	DispatchModeKafkaOutbox = "kafka_outbox"
)

// Valid Kafka transport values.
const (
	KafkaTransportPlaintext    = "plaintext"
	KafkaTransportSASLScramTLS = "sasl_scram_tls"
)

// defaults returns a Config pre-populated with built-in defaults.
func defaults() Config {
	maxConns := runtime.NumCPU() * 4
	if maxConns < 4 {
		maxConns = 4
	}
	return Config{
		RESTPort:        8080,
		GRPCPort:        9090,
		MetricsPort:     9091,
		LogLevel:        "info",
		AuditLogEnabled: true,
		DBMaxConns:      maxConns,
		DBMinConns:      0,
		DispatchMode:    DispatchModePolling,
		Kafka: KafkaConfig{
			SASLMechanism:   "SCRAM-SHA-512",
			OutboxBatchSize: 200,
		},
	}
}

// Load builds a Config by reading the optional YAML file first and then
// overlaying any environment variables that are set.
func Load() (Config, error) {
	return LoadFromPath(defaultYAMLPath)
}

// LoadFromPath is like Load but allows the caller to override the YAML path.
// If path is empty or the file does not exist, the YAML stage is skipped silently.
func LoadFromPath(path string) (Config, error) {
	cfg := defaults()

	// ── Stage 1: YAML file (optional) ─────────────────────────────────────
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if err = yaml.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("config: parse yaml %s: %w", path, err)
			}
		}
		// file-not-found is silently ignored (os.IsNotExist); other errors too
		// because this file is optional by design.
	}

	// ── Stage 2: environment variables (always win) ────────────────────────
	if v := os.Getenv("WE_POSTGRES_DSN"); v != "" {
		cfg.PostgresDSN = v
	}
	if v := os.Getenv("WE_REST_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: WE_REST_PORT must be an integer, got %q", v)
		}
		cfg.RESTPort = p
	}
	if v := os.Getenv("WE_GRPC_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: WE_GRPC_PORT must be an integer, got %q", v)
		}
		cfg.GRPCPort = p
	}
	if v := os.Getenv("WE_METRICS_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: WE_METRICS_PORT must be an integer, got %q", v)
		}
		cfg.MetricsPort = p
	}
	if v := os.Getenv("WE_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("WE_AUDIT_LOG_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: WE_AUDIT_LOG_ENABLED must be true/false, got %q", v)
		}
		cfg.AuditLogEnabled = b
	}
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: DB_MAX_CONNS must be an integer, got %q", v)
		}
		if n < 1 {
			return Config{}, fmt.Errorf("config: DB_MAX_CONNS must be >= 1, got %d", n)
		}
		cfg.DBMaxConns = n
	}
	if v := os.Getenv("DB_MIN_CONNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: DB_MIN_CONNS must be an integer, got %q", v)
		}
		if n < 0 {
			return Config{}, fmt.Errorf("config: DB_MIN_CONNS must be >= 0, got %d", n)
		}
		cfg.DBMinConns = n
	}

	// Dispatch-mode overrides (FR-001). Empty env leaves the YAML/default value.
	if v := os.Getenv("WE_DISPATCH_MODE"); v != "" {
		cfg.DispatchMode = v
	}
	if v := os.Getenv("WE_KAFKA_SEED_BROKERS"); v != "" {
		cfg.Kafka.SeedBrokers = v
	}
	if v := os.Getenv("WE_KAFKA_TRANSPORT"); v != "" {
		cfg.Kafka.Transport = v
	}
	if v := os.Getenv("WE_KAFKA_SASL_MECHANISM"); v != "" {
		cfg.Kafka.SASLMechanism = v
	}
	// Secrets — env-only, ignored if already present in YAML (defence in depth).
	if v := os.Getenv("WE_KAFKA_SASL_USERNAME"); v != "" {
		cfg.Kafka.SASLUsername = v
	}
	if v := os.Getenv("WE_KAFKA_SASL_PASSWORD"); v != "" {
		cfg.Kafka.SASLPassword = v
	}
	if v := os.Getenv("WE_KAFKA_TLS_CA_PATH"); v != "" {
		cfg.Kafka.TLSCAPath = v
	}
	if v := os.Getenv("WE_KAFKA_TLS_SERVER_NAME"); v != "" {
		cfg.Kafka.TLSServerName = v
	}
	if v := os.Getenv("WE_OUTBOX_BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: WE_OUTBOX_BATCH_SIZE must be an integer, got %q", v)
		}
		if n < 1 {
			return Config{}, fmt.Errorf("config: WE_OUTBOX_BATCH_SIZE must be >= 1, got %d", n)
		}
		cfg.Kafka.OutboxBatchSize = n
	}

	// ── Validation ─────────────────────────────────────────────────────────
	if cfg.PostgresDSN == "" {
		return Config{}, fmt.Errorf("config: WE_POSTGRES_DSN is required (not set and not in yaml file)")
	}
	if cfg.DBMinConns > cfg.DBMaxConns {
		return Config{}, fmt.Errorf("config: DB_MIN_CONNS (%d) must be <= DB_MAX_CONNS (%d)", cfg.DBMinConns, cfg.DBMaxConns)
	}
	if err := validateDispatchMode(&cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// validateDispatchMode enforces FR-001, FR-008, FR-012 at startup time:
//   - unknown mode strings are rejected loudly,
//   - kafka_outbox mode requires seed brokers + a valid transport mode,
//   - sasl_scram_tls requires SASL credentials.
//
// polling (or empty) requires nothing extra — it's the default regression-safe path.
func validateDispatchMode(cfg *Config) error {
	switch cfg.DispatchMode {
	case "", DispatchModePolling:
		cfg.DispatchMode = DispatchModePolling
		return nil
	case DispatchModeKafkaOutbox:
		if cfg.Kafka.SeedBrokers == "" {
			return fmt.Errorf("config: WE_KAFKA_SEED_BROKERS is required when WE_DISPATCH_MODE=%s", DispatchModeKafkaOutbox)
		}
		switch cfg.Kafka.Transport {
		case KafkaTransportPlaintext:
			// no further required inputs
		case KafkaTransportSASLScramTLS:
			if cfg.Kafka.SASLUsername == "" {
				return fmt.Errorf("config: WE_KAFKA_SASL_USERNAME is required when WE_KAFKA_TRANSPORT=%s", KafkaTransportSASLScramTLS)
			}
			if cfg.Kafka.SASLPassword == "" {
				return fmt.Errorf("config: WE_KAFKA_SASL_PASSWORD is required when WE_KAFKA_TRANSPORT=%s", KafkaTransportSASLScramTLS)
			}
			switch cfg.Kafka.SASLMechanism {
			case "SCRAM-SHA-256", "SCRAM-SHA-512":
			default:
				return fmt.Errorf("config: WE_KAFKA_SASL_MECHANISM must be SCRAM-SHA-256 or SCRAM-SHA-512, got %q", cfg.Kafka.SASLMechanism)
			}
		case "":
			return fmt.Errorf("config: WE_KAFKA_TRANSPORT is required when WE_DISPATCH_MODE=%s (plaintext|sasl_scram_tls)", DispatchModeKafkaOutbox)
		default:
			return fmt.Errorf("config: unknown WE_KAFKA_TRANSPORT %q (want plaintext|sasl_scram_tls)", cfg.Kafka.Transport)
		}
		return nil
	default:
		return fmt.Errorf("config: unknown WE_DISPATCH_MODE %q (want polling|kafka_outbox)", cfg.DispatchMode)
	}
}
