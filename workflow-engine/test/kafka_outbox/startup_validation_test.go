//go:build integration

package kafka_outbox_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/config"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch/kafka_outbox"
)

func TestStartupValidation(t *testing.T) {
	ctx := context.Background()

	// 1. Missing Pool
	t.Run("MissingPool", func(t *testing.T) {
		rt := kafka_outbox.New(kafka_outbox.Config{
			SeedBrokers: "localhost:9092",
		})
		err := rt.Start(ctx)
		if err == nil || !strings.Contains(err.Error(), "Pool is required") {
			t.Errorf("expected error about missing pool, got %v", err)
		}
	})

	// Use a real fixture for the rest.
	f := newFixture(t)
	defer f.Close(ctx)

	// 2. Missing SeedBrokers
	t.Run("MissingSeedBrokers", func(t *testing.T) {
		rt := kafka_outbox.New(kafka_outbox.Config{
			Pool: f.Pool,
		})
		err := rt.Start(ctx)
		if err == nil || !strings.Contains(err.Error(), "SeedBrokers is required") {
			t.Errorf("expected error about missing brokers, got %v", err)
		}
	})

	t.Run("MissingTopic", func(t *testing.T) {
		rt := kafka_outbox.New(kafka_outbox.Config{
			Pool:        f.Pool,
			SeedBrokers: f.SeedBrokers,
			JobTypes:    []string{"missing-topic-123"},
			Transport:   config.KafkaTransportPlaintext,
		})
		err := rt.Start(ctx)
		if err == nil {
			t.Fatal("expected error about missing topic")
		}
		if !strings.Contains(err.Error(), "missing-topic-123") {
			t.Errorf("expected error to name the missing topic, got %v", err)
		}
		if !strings.Contains(err.Error(), "is missing") && !strings.Contains(err.Error(), "UNKNOWN_TOPIC_OR_PARTITION") {
			t.Errorf("expected error to indicate topic is missing, got %v", err)
		}
	})

	t.Run("InvalidBrokers", func(t *testing.T) {
		rt := kafka_outbox.New(kafka_outbox.Config{
			Pool:        f.Pool,
			SeedBrokers: "localhost:1", // guaranteed to fail
			JobTypes:    []string{"s1"},
			Transport:   config.KafkaTransportPlaintext,
		})
		// Use a short timeout so we don't wait for internal Kafka retries.
		tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		err := rt.Start(tctx)
		if err == nil {
			t.Error("expected error about unreachable brokers")
		}
	})
}
