package kafka_outbox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// newKafkaClient constructs a franz-go client configured with acks=all and
// the idempotent producer (enabled by default in franz-go v1.x when not
// explicitly disabled). It branches on cfg.Transport for transport security.
//
// Fails fast if required inputs are missing — the config layer already
// validates these, but duplicated checks here keep this function usable
// from tests.
func newKafkaClient(cfg Config) (*kgo.Client, error) {
	seeds := splitBrokers(cfg.SeedBrokers)
	if len(seeds) == 0 {
		return nil, fmt.Errorf("kafka: no seed brokers configured")
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(seeds...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchMaxBytes(1 << 20), // 1 MiB per batch
	}

	switch cfg.Transport {
	case "plaintext":
		// No additional opts — cleartext TCP on trusted networks only.
	case "sasl_scram_tls":
		tlsCfg, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		mech, err := buildSASLMechanism(cfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.DialTLSConfig(tlsCfg), kgo.SASL(mech))
	default:
		return nil, fmt.Errorf("kafka: unknown transport %q", cfg.Transport)
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: new client: %w", err)
	}
	return client, nil
}

// pingBroker issues a lightweight Metadata request against the broker to
// validate connectivity + authentication end-to-end (FR-008). Called from
// Runtime.Start.
func pingBroker(ctx context.Context, client *kgo.Client) error {
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("kafka: ping broker: %w", err)
	}
	return nil
}

func splitBrokers(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func buildTLSConfig(cfg Config) (*tls.Config, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSServerName != "" {
		tlsCfg.ServerName = cfg.TLSServerName
	}
	if cfg.TLSCAPath != "" {
		pem, err := os.ReadFile(cfg.TLSCAPath)
		if err != nil {
			return nil, fmt.Errorf("kafka: read TLS CA %q: %w", cfg.TLSCAPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("kafka: no PEM certs found in %q", cfg.TLSCAPath)
		}
		tlsCfg.RootCAs = pool
	}
	return tlsCfg, nil
}

func buildSASLMechanism(cfg Config) (sasl.Mechanism, error) {
	if cfg.SASLUsername == "" || cfg.SASLPassword == "" {
		return nil, fmt.Errorf("kafka: SASL username and password are required for sasl_scram_tls")
	}
	auth := scram.Auth{User: cfg.SASLUsername, Pass: cfg.SASLPassword}
	switch cfg.SASLMechanism {
	case "", "SCRAM-SHA-512":
		return auth.AsSha512Mechanism(), nil
	case "SCRAM-SHA-256":
		return auth.AsSha256Mechanism(), nil
	default:
		return nil, fmt.Errorf("kafka: unknown SASL mechanism %q", cfg.SASLMechanism)
	}
}

// topicFor returns the Kafka topic name for a given job_type. Matches
// contracts/kafka-topics.md §1 — "workflow.jobs.<jobType>".
func topicFor(jobType string) string {
	return "workflow.jobs." + jobType
}
