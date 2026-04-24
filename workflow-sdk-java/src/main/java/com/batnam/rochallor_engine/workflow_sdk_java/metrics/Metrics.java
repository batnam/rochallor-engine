package com.batnam.rochallor_engine.workflow_sdk_java.metrics;

import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.Timer;

/**
 * Prometheus-compatible metrics for the Java SDK, backed by Micrometer.
 */
public class Metrics {

    public final Timer   pollLatency;
    public final Counter lockConflicts;
    public final Timer   handlerLatency;
    public final Counter retriesTotal;
    public final Counter jobsCompleted;
    public final Counter jobsFailed;

    public Metrics(MeterRegistry registry) {
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
