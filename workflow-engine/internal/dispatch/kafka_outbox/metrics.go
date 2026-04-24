package kafka_outbox

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Per FR-010: kafka_outbox metrics are registered ONLY when the engine boots
// in kafka_outbox mode, so polling-mode deployments cannot fire kafka_outbox-
// specific alerts by accident. The sync.Once guard ensures the registration
// survives multiple Runtime boots inside the same process (e.g., tests) and
// the prom vars are hot to the rest of the package after the first Start.
var (
	metricsOnce sync.Once

	outboxBacklog           prometheus.Gauge
	relayPublishTotal       *prometheus.CounterVec
	relayBatchLatencySecs   prometheus.Histogram
	relayLeader             prometheus.Gauge
	kafkaProducerErrors     *prometheus.CounterVec
	kafkaConsumerLagSeconds *prometheus.GaugeVec
)

// ensureMetricsRegistered is idempotent — registers the event-driven metric
// set on first call, no-op thereafter.
func ensureMetricsRegistered() {
	metricsOnce.Do(func() {
		outboxBacklog = promauto.NewGauge(prometheus.GaugeOpts{
			Name: "workflow_engine_dispatch_outbox_backlog",
			Help: "Current number of pending rows in dispatch_outbox (sampled periodically).",
		})
		relayPublishTotal = promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "workflow_engine_dispatch_relay_publish_total",
			Help: "Total dispatch_outbox rows handed to the broker, labelled by outcome.",
		}, []string{"result"})
		relayBatchLatencySecs = promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "workflow_engine_dispatch_relay_batch_latency_seconds",
			Help:    "End-to-end relay cycle latency (select → produce → delete → commit) in seconds.",
			Buckets: prometheus.DefBuckets,
		})
		relayLeader = promauto.NewGauge(prometheus.GaugeOpts{
			Name: "workflow_engine_dispatch_relay_leader",
			Help: "1 if this replica currently holds the relay advisory lock, 0 otherwise.",
		})
		kafkaProducerErrors = promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "workflow_engine_kafka_producer_errors_total",
			Help: "Kafka producer error count, labelled by franz-go error code.",
		}, []string{"code"})
		kafkaConsumerLagSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "workflow_engine_kafka_consumer_lag_seconds",
			Help: "Observed consumer-group lag in seconds (from engine-side admin probe).",
		}, []string{"topic", "group", "partition"})
	})
}
