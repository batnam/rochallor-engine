package com.batnam.rochallor_engine.workflow_sdk_java.runner;

import com.batnam.rochallor_engine.workflow_sdk_java.client.CallDeadline;
import com.batnam.rochallor_engine.workflow_sdk_java.client.EngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.client.HttpError;
import com.batnam.rochallor_engine.workflow_sdk_java.client.Job;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.HandlerRegistry;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.JobContext;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.NonRetryableException;
import com.batnam.rochallor_engine.workflow_sdk_java.metrics.Metrics;
import com.batnam.rochallor_engine.workflow_sdk_java.retry.BackoffPolicy;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.grpc.Status;

import java.time.Duration;
import java.time.Instant;
import java.util.Map;
import java.util.function.BooleanSupplier;
import java.util.function.Supplier;
import java.util.concurrent.CancellationException;
import java.util.logging.Logger;

/** Handler execution is separated from acknowledgement transport failures. */
final class Callbacks {
    private static final Logger LOG = Logger.getLogger(Callbacks.class.getName());
    private static final ObjectMapper JSON = new ObjectMapper();
    record Outcome(String kind, String payload, boolean retryable) { }

    static Outcome execute(HandlerRegistry registry, Job job, Metrics metrics) {
        return execute(registry, job, metrics, () -> true);
    }

    static Outcome execute(HandlerRegistry registry, Job job, Metrics metrics, BooleanSupplier owned) {
        var handler = registry.get(job.jobType);
        if (handler.isEmpty()) return new Outcome("fail", "no handler registered for " + job.jobType, false);
        long start = System.nanoTime();
        try {
            var variables = handler.get().handle(new JobContext(job, () -> !owned.getAsBoolean()));
            String payload = JSON.writeValueAsString(variables == null ? Map.of() : variables);
            if (!owned.getAsBoolean()) throw new CancellationException("handler ownership ended");
            metrics.jobsCompleted.increment();
            return new Outcome("complete", payload, false);
        } catch (Exception failure) {
            if (!owned.getAsBoolean()) throw new CancellationException("handler ownership ended");
            metrics.jobsFailed.increment();
            boolean retryable = !(failure instanceof NonRetryableException);
            if (retryable) metrics.retriesTotal.increment();
            return new Outcome("fail", String.valueOf(failure.getMessage()), retryable);
        } finally {
            metrics.handlerLatency.record(Duration.ofNanos(System.nanoTime() - start));
        }
    }

    static void deliver(EngineClient engine, String workerId, String jobId, Outcome outcome,
                        BooleanSupplier owned, Instant deadline, boolean retry, Metrics metrics, String mode) throws Exception {
        deliverRenewing(engine, workerId, jobId, outcome, owned, () -> deadline, retry, metrics, mode);
    }

    static void deliverRenewing(EngineClient engine, String workerId, String jobId, Outcome outcome,
                        BooleanSupplier owned, Supplier<Instant> currentDeadline, boolean retry, Metrics metrics, String mode) throws Exception {
        var state = metrics.callback(mode, outcome.kind());
        long started = System.nanoTime();
        try (var pending = state.track()) {
            for (int attempt = 1; ; attempt++) {
                Instant deadline = currentDeadline.get();
                checkOwnership(owned, deadline);
                Duration timeout = Duration.ofSeconds(5);
                if (deadline != null && Duration.between(Instant.now(), deadline).compareTo(timeout) < 0) {
                    timeout = Duration.between(Instant.now(), deadline);
                    if (timeout.isNegative() || timeout.isZero()) throw new InterruptedException("lease expired");
                }
                try (var scope = CallDeadline.within(timeout)) {
                    if (outcome.kind().equals("complete")) {
                        Map<String,Object> variables = JSON.readValue(outcome.payload(), new TypeReference<>() {});
                        engine.completeJob(jobId, workerId, variables);
                    } else engine.failJob(jobId, workerId, outcome.payload(), outcome.retryable());
                    checkOwnership(owned, currentDeadline.get());
                    return;
                } catch (Exception failure) {
                    checkOwnership(owned, currentDeadline.get());
                    if (permanent(failure)) { state.rejected.increment(); throw failure; }
                    if (!retry) throw failure;
                    state.retries.increment();
                    LOG.warning("callback pending job_id=" + jobId + " callback=" + outcome.kind() + " attempt=" + attempt
                        + " age_seconds=" + (System.nanoTime() - started) / 1e9 + ": " + failure);
                    long end = System.nanoTime() + BackoffPolicy.delay(attempt).toNanos();
                    while (System.nanoTime() < end) {
                        checkOwnership(owned, currentDeadline.get());
                        Thread.sleep(Math.max(1, Math.min(50, (end - System.nanoTime()) / 1_000_000)));
                    }
                }
            }
        }
    }

    private static void checkOwnership(BooleanSupplier owned, Instant deadline) throws InterruptedException {
        if (Thread.currentThread().isInterrupted() || !owned.getAsBoolean() || deadline != null && !Instant.now().isBefore(deadline)) {
            throw new InterruptedException("callback ownership ended");
        }
    }

    static boolean permanent(Exception error) {
        if (error instanceof HttpError http) return http.statusCode >= 400 && http.statusCode < 500 && http.statusCode != 408 && http.statusCode != 429;
        return switch (Status.fromThrowable(error).getCode()) {
            case INVALID_ARGUMENT, NOT_FOUND, ALREADY_EXISTS, PERMISSION_DENIED, FAILED_PRECONDITION,
                 OUT_OF_RANGE, UNIMPLEMENTED, UNAUTHENTICATED, DATA_LOSS -> true;
            default -> false;
        };
    }
}
