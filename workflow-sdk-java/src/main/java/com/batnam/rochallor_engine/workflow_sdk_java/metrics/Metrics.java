package com.batnam.rochallor_engine.workflow_sdk_java.metrics;

import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.Gauge;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.Timer;

/**
 * Prometheus-compatible metrics for the Java SDK, backed by Micrometer.
 */
public class Metrics {

    public final Counter kafkaRecordErrors;
    private final Map<String, CallbackMetrics> callbacks = new ConcurrentHashMap<>();
    private final Map<String, Counter> heartbeats = new ConcurrentHashMap<>();

    public void heartbeat(String result) { heartbeats.get(result).increment(); }

    public static final class CallbackMetrics {
        public final Counter retries;
        public final Counter rejected;
        private final Map<Object, Long> pending = new ConcurrentHashMap<>();

        CallbackMetrics(MeterRegistry registry, String mode, String kind) {
            String[] tags = {"dispatch", mode, "callback", kind};
            retries = Counter.builder("workflow_sdk_callback_retries_total").tags(tags).register(registry);
            rejected = Counter.builder("workflow_sdk_callback_rejected_total").tags(tags).register(registry);
            Gauge.builder("workflow_sdk_callback_pending", pending, Map::size).tags(tags).register(registry);
            Gauge.builder("workflow_sdk_callback_oldest_pending_seconds", this,
                state -> state.pending.values().stream().mapToDouble(start -> (System.nanoTime() - start) / 1e9).max().orElse(0))
                .tags(tags).register(registry);
        }

        public AutoCloseable track() {
            Object token = new Object();
            pending.put(token, System.nanoTime());
            return () -> pending.remove(token);
        }
    }

    public CallbackMetrics callback(String mode, String kind) { return callbacks.get(mode + ":" + kind); }

    public final Timer   pollLatency;
    public final Counter lockConflicts;
    public final Timer   handlerLatency;
    public final Counter retriesTotal;
    public final Counter jobsCompleted;
    public final Counter jobsFailed;

    public Metrics(MeterRegistry registry) {
        for (String result : new String[]{"accepted", "retry", "lease_lost", "rejected"}) {
            heartbeats.put(result, Counter.builder("workflow_sdk_heartbeat_total").tag("result", result).register(registry));
        }
        for (String mode : new String[]{"polling", "kafka"}) {
            for (String kind : new String[]{"complete", "fail"}) callbacks.put(mode + ":" + kind, new CallbackMetrics(registry, mode, kind));
        }
        kafkaRecordErrors = Counter.builder("workflow_sdk_kafka_record_errors_total").register(registry);
        this.pollLatency = Timer.builder("workflow_sdk_poll_latency_seconds")
                .description("Latency of PollJobs calls")
                .register(registry);

        this.lockConflicts = Counter.builder("workflow_sdk_lock_conflicts_total")
                .description("Poll rounds that returned zero jobs")
                .register(registry);

        this.handlerLatency = Timer.builder("workflow_sdk_handler_latency_seconds")
                .description("Duration of handler executions")
                .register(registry);

        this.retriesTotal = Counter.builder("workflow_sdk_retries_total")
                .description("Jobs retried by the SDK runner")
                .register(registry);

        this.jobsCompleted = Counter.builder("workflow_sdk_jobs_completed_total")
                .tag("outcome", "success")
                .description("Jobs successfully completed")
                .register(registry);

        this.jobsFailed = Counter.builder("workflow_sdk_jobs_completed_total")
                .tag("outcome", "failure")
                .description("Jobs failed")
                .register(registry);
    }
}
