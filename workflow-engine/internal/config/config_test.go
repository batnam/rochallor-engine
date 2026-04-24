package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/config"
)

// setEnv sets multiple env vars for a test and returns a cleanup function.
func setEnv(t *testing.T, kvs ...string) {
	t.Helper()
	if len(kvs)%2 != 0 {
		t.Fatal("setEnv: odd number of key-value arguments")
	}
	var toUnset []string
	for i := 0; i < len(kvs); i += 2 {
		k, v := kvs[i], kvs[i+1]
		old, existed := os.LookupEnv(k)
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("setenv %s: %v", k, err)
		}
		if existed {
			toUnset = append(toUnset, k, old)
		} else {
			toUnset = append(toUnset, k, "")
		}
	}
	t.Cleanup(func() {
		for i := 0; i < len(toUnset); i += 2 {
			if toUnset[i+1] == "" {
				os.Unsetenv(toUnset[i])
			} else {
				os.Setenv(toUnset[i], toUnset[i+1])
			}
		}
	})
}

// writeYAML writes content to a temp file and returns its path.
func writeYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "engine.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writeYAML: %v", err)
	}
	return path
}

// clearEngineEnv unsets all engine env vars for the duration of the test.
func clearEngineEnv(t *testing.T) {
	t.Helper()
	vars := []string{
		"WE_POSTGRES_DSN", "WE_REST_PORT", "WE_GRPC_PORT",
		"WE_METRICS_PORT", "WE_LOG_LEVEL", "WE_AUDIT_LOG_ENABLED",
		"DB_MAX_CONNS", "DB_MIN_CONNS",
		"WE_DISPATCH_MODE", "WE_KAFKA_SEED_BROKERS", "WE_KAFKA_TRANSPORT",
		"WE_KAFKA_SASL_MECHANISM", "WE_KAFKA_SASL_USERNAME", "WE_KAFKA_SASL_PASSWORD",
		"WE_KAFKA_TLS_CA_PATH", "WE_KAFKA_TLS_SERVER_NAME", "WE_OUTBOX_BATCH_SIZE",
	}
	for _, v := range vars {
		old, existed := os.LookupEnv(v)
		os.Unsetenv(v)
		t.Cleanup(func() {
			if existed {
				os.Setenv(v, old)
			}
		})
	}
}

// TestEnvOnly: all values come from environment variables; no YAML file.
func TestEnvOnly(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost:5432/workflow",
		"WE_REST_PORT", "8081",
		"WE_GRPC_PORT", "9091",
		"WE_METRICS_PORT", "9092",
		"WE_LOG_LEVEL", "debug",
		"WE_AUDIT_LOG_ENABLED", "false",
	)

	cfg, err := config.LoadFromPath("") // no yaml file
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.PostgresDSN != "postgres://user:pass@localhost:5432/workflow" {
		t.Errorf("PostgresDSN: got %q", cfg.PostgresDSN)
	}
	if cfg.RESTPort != 8081 {
		t.Errorf("RESTPort: want 8081, got %d", cfg.RESTPort)
	}
	if cfg.GRPCPort != 9091 {
		t.Errorf("GRPCPort: want 9091, got %d", cfg.GRPCPort)
	}
	if cfg.MetricsPort != 9092 {
		t.Errorf("MetricsPort: want 9092, got %d", cfg.MetricsPort)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel: want debug, got %q", cfg.LogLevel)
	}
	if cfg.AuditLogEnabled != false {
		t.Errorf("AuditLogEnabled: want false, got true")
	}
}

// TestYAMLOnly: all values come from the YAML file; env is unset.
func TestYAMLOnly(t *testing.T) {
	clearEngineEnv(t)
	yaml := writeYAML(t, `
postgresDSN: "postgres://yaml:secret@db:5432/wf"
restPort: 7070
grpcPort: 8080
metricsPort: 8081
logLevel: warn
auditLogEnabled: false
`)

	cfg, err := config.LoadFromPath(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.PostgresDSN != "postgres://yaml:secret@db:5432/wf" {
		t.Errorf("PostgresDSN: got %q", cfg.PostgresDSN)
	}
	if cfg.RESTPort != 7070 {
		t.Errorf("RESTPort: want 7070, got %d", cfg.RESTPort)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel: want warn, got %q", cfg.LogLevel)
	}
}

// TestEnvOverridesYAML: env vars win over YAML file values.
func TestEnvOverridesYAML(t *testing.T) {
	clearEngineEnv(t)
	yaml := writeYAML(t, `
postgresDSN: "postgres://yaml:secret@db:5432/wf"
restPort: 7070
logLevel: warn
`)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://env:pass@envhost:5432/workflow",
		"WE_REST_PORT", "9999",
	)

	cfg, err := config.LoadFromPath(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// env vars override YAML
	if cfg.PostgresDSN != "postgres://env:pass@envhost:5432/workflow" {
		t.Errorf("PostgresDSN: want env value, got %q", cfg.PostgresDSN)
	}
	if cfg.RESTPort != 9999 {
		t.Errorf("RESTPort: want 9999, got %d", cfg.RESTPort)
	}
	// non-overridden YAML values are preserved
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel: want warn (from YAML), got %q", cfg.LogLevel)
	}
}

// TestMissingRequiredVar: WE_POSTGRES_DSN absent → error.
func TestMissingRequiredVar(t *testing.T) {
	clearEngineEnv(t)
	_, err := config.LoadFromPath("")
	if err == nil {
		t.Fatal("expected error for missing WE_POSTGRES_DSN, got nil")
	}
}

// TestDefaults: only WE_POSTGRES_DSN set; all optional fields get default values.
func TestDefaults(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t, "WE_POSTGRES_DSN", "postgres://default:test@localhost/wf")

	cfg, err := config.LoadFromPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RESTPort != 8080 {
		t.Errorf("default RESTPort: want 8080, got %d", cfg.RESTPort)
	}
	if cfg.GRPCPort != 9090 {
		t.Errorf("default GRPCPort: want 9090, got %d", cfg.GRPCPort)
	}
	if cfg.MetricsPort != 9091 {
		t.Errorf("default MetricsPort: want 9091, got %d", cfg.MetricsPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("default LogLevel: want info, got %q", cfg.LogLevel)
	}
	if !cfg.AuditLogEnabled {
		t.Errorf("default AuditLogEnabled: want true, got false")
	}
}

// TestInvalidPort: a non-numeric WE_REST_PORT returns an error.
func TestInvalidPort(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"WE_REST_PORT", "not-a-port",
	)
	_, err := config.LoadFromPath("")
	if err == nil {
		t.Fatal("expected error for non-numeric WE_REST_PORT, got nil")
	}
}

// TestDBPoolEnv: DB_MAX_CONNS and DB_MIN_CONNS are parsed from the environment.
func TestDBPoolEnv(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"DB_MAX_CONNS", "50",
		"DB_MIN_CONNS", "10",
	)
	cfg, err := config.LoadFromPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DBMaxConns != 50 {
		t.Errorf("DBMaxConns: want 50, got %d", cfg.DBMaxConns)
	}
	if cfg.DBMinConns != 10 {
		t.Errorf("DBMinConns: want 10, got %d", cfg.DBMinConns)
	}
}

// TestDBPoolDefault: without env overrides, DBMaxConns defaults to
// NumCPU * 4 (floor 4) and DBMinConns defaults to 0.
func TestDBPoolDefault(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t, "WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf")
	cfg, err := config.LoadFromPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DBMaxConns < 4 {
		t.Errorf("default DBMaxConns floor: want >= 4, got %d", cfg.DBMaxConns)
	}
	if cfg.DBMinConns != 0 {
		t.Errorf("default DBMinConns: want 0, got %d", cfg.DBMinConns)
	}
}

// TestDBPoolMinExceedsMax: DB_MIN_CONNS > DB_MAX_CONNS is rejected.
func TestDBPoolMinExceedsMax(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"DB_MAX_CONNS", "5",
		"DB_MIN_CONNS", "10",
	)
	if _, err := config.LoadFromPath(""); err == nil {
		t.Fatal("expected error when DB_MIN_CONNS > DB_MAX_CONNS, got nil")
	}
}

// TestDBPoolInvalid: a non-numeric DB_MAX_CONNS is rejected.
func TestDBPoolInvalid(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"DB_MAX_CONNS", "abc",
	)
	if _, err := config.LoadFromPath(""); err == nil {
		t.Fatal("expected error for non-numeric DB_MAX_CONNS, got nil")
	}
}

// TestDispatchModeDefault: unset WE_DISPATCH_MODE defaults to "polling" (FR-001).
func TestDispatchModeDefault(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t, "WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf")
	cfg, err := config.LoadFromPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DispatchMode != config.DispatchModePolling {
		t.Errorf("default DispatchMode: want %q, got %q", config.DispatchModePolling, cfg.DispatchMode)
	}
}

// TestDispatchModeKafkaOutboxPlaintext: kafka_outbox + plaintext is valid with
// just seed brokers.
func TestDispatchModeKafkaOutboxPlaintext(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"WE_DISPATCH_MODE", "kafka_outbox",
		"WE_KAFKA_TRANSPORT", "plaintext",
		"WE_KAFKA_SEED_BROKERS", "localhost:9092",
	)
	cfg, err := config.LoadFromPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DispatchMode != config.DispatchModeKafkaOutbox {
		t.Errorf("DispatchMode: want kafka_outbox, got %q", cfg.DispatchMode)
	}
	if cfg.Kafka.SeedBrokers != "localhost:9092" {
		t.Errorf("SeedBrokers: got %q", cfg.Kafka.SeedBrokers)
	}
}

// TestDispatchModeUnknown: unknown mode strings are rejected loudly.
func TestDispatchModeUnknown(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"WE_DISPATCH_MODE", "hybrid",
	)
	if _, err := config.LoadFromPath(""); err == nil {
		t.Fatal("expected error for unknown WE_DISPATCH_MODE, got nil")
	}
}

// TestDispatchModeKafkaOutboxMissingBrokers: kafka_outbox requires seed brokers.
func TestDispatchModeKafkaOutboxMissingBrokers(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"WE_DISPATCH_MODE", "kafka_outbox",
		"WE_KAFKA_TRANSPORT", "plaintext",
	)
	if _, err := config.LoadFromPath(""); err == nil {
		t.Fatal("expected error for missing WE_KAFKA_SEED_BROKERS, got nil")
	}
}

// TestDispatchModeKafkaOutboxMissingTransport: kafka_outbox requires a transport.
func TestDispatchModeKafkaOutboxMissingTransport(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"WE_DISPATCH_MODE", "kafka_outbox",
		"WE_KAFKA_SEED_BROKERS", "localhost:9092",
	)
	if _, err := config.LoadFromPath(""); err == nil {
		t.Fatal("expected error for missing WE_KAFKA_TRANSPORT, got nil")
	}
}

// TestDispatchModeSASLMissingPassword: sasl_scram_tls requires a password (FR-012).
func TestDispatchModeSASLMissingPassword(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"WE_DISPATCH_MODE", "kafka_outbox",
		"WE_KAFKA_TRANSPORT", "sasl_scram_tls",
		"WE_KAFKA_SEED_BROKERS", "broker-1:9093",
		"WE_KAFKA_SASL_USERNAME", "workflow-engine",
		// no password
	)
	if _, err := config.LoadFromPath(""); err == nil {
		t.Fatal("expected error for missing WE_KAFKA_SASL_PASSWORD, got nil")
	}
}

// TestDispatchModeSASLMissingUsername: sasl_scram_tls requires a username.
func TestDispatchModeSASLMissingUsername(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"WE_DISPATCH_MODE", "kafka_outbox",
		"WE_KAFKA_TRANSPORT", "sasl_scram_tls",
		"WE_KAFKA_SEED_BROKERS", "broker-1:9093",
		"WE_KAFKA_SASL_PASSWORD", "secret",
		// no username
	)
	if _, err := config.LoadFromPath(""); err == nil {
		t.Fatal("expected error for missing WE_KAFKA_SASL_USERNAME, got nil")
	}
}

// TestDispatchModeSASLHappyPath: sasl_scram_tls with all required inputs loads.
func TestDispatchModeSASLHappyPath(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"WE_DISPATCH_MODE", "kafka_outbox",
		"WE_KAFKA_TRANSPORT", "sasl_scram_tls",
		"WE_KAFKA_SEED_BROKERS", "broker-1:9093,broker-2:9093",
		"WE_KAFKA_SASL_MECHANISM", "SCRAM-SHA-256",
		"WE_KAFKA_SASL_USERNAME", "workflow-engine",
		"WE_KAFKA_SASL_PASSWORD", "s3cret",
	)
	cfg, err := config.LoadFromPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Kafka.SASLUsername != "workflow-engine" || cfg.Kafka.SASLPassword != "s3cret" {
		t.Errorf("SASL creds not loaded: username=%q password-set=%v", cfg.Kafka.SASLUsername, cfg.Kafka.SASLPassword != "")
	}
	if cfg.Kafka.SASLMechanism != "SCRAM-SHA-256" {
		t.Errorf("SASLMechanism: want SCRAM-SHA-256, got %q", cfg.Kafka.SASLMechanism)
	}
}

// TestYAMLCannotSupplyKafkaSecrets: even if a YAML file carries SASL secrets,
// the loader must ignore them (secrets are env-only, FR-012).
func TestYAMLCannotSupplyKafkaSecrets(t *testing.T) {
	clearEngineEnv(t)
	yaml := writeYAML(t, `
postgresDSN: "postgres://user:pass@localhost/wf"
dispatchMode: "kafka_outbox"
kafka:
  seedBrokers: "broker:9093"
  transport: "sasl_scram_tls"
  saslUsername: "yaml-leak"
  saslPassword: "yaml-leak"
`)
	// Load must fail (no env-supplied username/password), proving YAML can't
	// back-door credentials — the yaml:"-" tags drop those fields on unmarshal.
	if _, err := config.LoadFromPath(yaml); err == nil {
		t.Fatal("expected error: YAML should not be able to supply SASL credentials")
	}
}

// TestOutboxBatchSizeInvalid: WE_OUTBOX_BATCH_SIZE must be a positive int.
func TestOutboxBatchSizeInvalid(t *testing.T) {
	clearEngineEnv(t)
	setEnv(t,
		"WE_POSTGRES_DSN", "postgres://user:pass@localhost/wf",
		"WE_OUTBOX_BATCH_SIZE", "0",
	)
	if _, err := config.LoadFromPath(""); err == nil {
		t.Fatal("expected error for WE_OUTBOX_BATCH_SIZE=0")
	}
}
