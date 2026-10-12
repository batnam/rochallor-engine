package com.batnam.rochallor_engine.workflow_sdk_java.runner;

import com.batnam.rochallor_engine.workflow_sdk_java.client.*;
import com.batnam.rochallor_engine.workflow_sdk_java.metrics.Metrics;
import com.batnam.rochallor_engine.workflow_sdk_java.retry.BackoffPolicy;

import java.time.Duration;
import java.time.Instant;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.BooleanSupplier;
import java.util.function.Consumer;

/** Timer expiry never waits for a handler or a blocked heartbeat transport. */
final class Lease implements AutoCloseable {
    private final LeaseExtender client;
    private final Job job;
    private final String worker;
    private final Runner.LeaseOptions options;
    private final ScheduledExecutorService scheduler;
    private final Metrics metrics;
    private final BooleanSupplier running;
    private final Consumer<Exception> fatal;
    private final Thread owner = Thread.currentThread();
    private final AtomicBoolean active = new AtomicBoolean(true);
    private final CompletableFuture<Void> ready = new CompletableFuture<>();
    private volatile Instant expiry;
    private ScheduledFuture<?> expiration;
    private Thread heartbeat;

    Lease(LeaseExtender client, Job job, String worker, Runner.LeaseOptions options,
          ScheduledExecutorService scheduler, Metrics metrics, BooleanSupplier running, Consumer<Exception> fatal) {
        if (job.lockExpiresAt == null) throw new IllegalArgumentException("heartbeat requires lockExpiresAt lease metadata");
        this.client = client; this.job = job; this.worker = worker; this.options = options;
        this.scheduler = scheduler; this.metrics = metrics; this.running = running; this.fatal = fatal;
        expiry = job.lockExpiresAt;
    }

    void start() throws Exception {
        armExpiry();
        heartbeat = Thread.ofPlatform().daemon(true).name("workflow-heartbeat-" + job.id).start(this::renew);
        ready.get();
    }

    Instant deadline() { return expiry; }
    boolean valid() { return active.get() && running.getAsBoolean() && Instant.now().isBefore(expiry); }

    private synchronized void armExpiry() {
        if (expiration != null) expiration.cancel(false);
        if (!active.get()) return;
        expiration = scheduler.schedule(() -> {
            synchronized (this) {
                if (!Instant.now().isBefore(expiry)) lose(new LeaseLostException());
            }
        }, Math.max(0, Duration.between(Instant.now(), expiry).toNanos()), TimeUnit.NANOSECONDS);
    }

    private void renew() {
        int attempt = 0;
        try {
            while (valid()) {
                try {
                    Duration remaining = Duration.between(Instant.now(), expiry);
                    Instant next;
                    try (var scope = CallDeadline.within(remaining.compareTo(Duration.ofSeconds(5)) < 0 ? remaining : Duration.ofSeconds(5))) {
                        next = client.extendJobLease(job.id, worker, options.leaseSeconds());
                    }
                    synchronized (this) {
                        if (!valid()) break;
                        if (next == null || next.isBefore(expiry) || !Instant.now().isBefore(next)) throw new IllegalArgumentException("invalid heartbeat expiry");
                        expiry = next;
                        armExpiry();
                    }
                    metrics.heartbeat("accepted");
                    ready.complete(null);
                    attempt = 0;
                    Thread.sleep(Duration.ofNanos(Math.max(1, Math.min(options.interval().toNanos(), Duration.between(Instant.now(), expiry).toNanos() / 3))));
                } catch (Exception error) {
                    if (!valid() || error instanceof InterruptedException) break;
                    if (error instanceof LeaseLostException) { lose(error); return; }
                    if (error instanceof HeartbeatUnsupportedException || error instanceof IllegalArgumentException || Callbacks.permanent(error)) {
                        lose(error); fatal.accept(error); return;
                    }
                    metrics.heartbeat("retry");
                    Thread.sleep(Duration.ofNanos(Math.max(1, Math.min(BackoffPolicy.delay(++attempt).toNanos(), Duration.between(Instant.now(), expiry).toNanos()))));
                }
            }
        } catch (InterruptedException stopped) {
            Thread.currentThread().interrupt();
        } finally {
            if (active.get()) lose(new LeaseLostException());
        }
    }

    private void lose(Exception error) {
        if (!active.compareAndSet(true, false)) return;
        metrics.heartbeat(error instanceof LeaseLostException ? "lease_lost" : "rejected");
        ready.completeExceptionally(error);
        owner.interrupt();
        close();
    }

    @Override public synchronized void close() {
        active.set(false);
        if (expiration != null) expiration.cancel(false);
        if (heartbeat != null) heartbeat.interrupt();
        ready.completeExceptionally(new LeaseLostException());
    }

    void cancel() { close(); owner.interrupt(); }
}
