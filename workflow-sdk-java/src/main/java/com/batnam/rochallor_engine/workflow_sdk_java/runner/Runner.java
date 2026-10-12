package com.batnam.rochallor_engine.workflow_sdk_java.runner;

import com.batnam.rochallor_engine.workflow_sdk_java.client.CallDeadline;
import com.batnam.rochallor_engine.workflow_sdk_java.client.EngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.client.Job;
import com.batnam.rochallor_engine.workflow_sdk_java.client.LeaseExtender;
import com.batnam.rochallor_engine.workflow_sdk_java.client.HeartbeatUnsupportedException;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.HandlerRegistry;
import com.batnam.rochallor_engine.workflow_sdk_java.metrics.Metrics;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Set;
import java.util.Optional;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ScheduledThreadPoolExecutor;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.RejectedExecutionException;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;
import java.util.logging.Logger;

/** Polling runner; handlers and unacknowledged callbacks share the capacity limit. */
public class Runner {
    private static final Logger LOG = Logger.getLogger(Runner.class.getName());
    private final String workerId;
    private final long pollIntervalMs;
    private final EngineClient engine;
    private final HandlerRegistry registry;
    private final Metrics metrics;
    private final Semaphore slots;
    private final ExecutorService executor = Executors.newVirtualThreadPerTaskExecutor();
    private volatile boolean running;
    private Thread pollThread;
    private Thread shutdownHook;
    private boolean started;
    private volatile Exception failure;
    private final LeaseOptions leaseOptions;
    private final ScheduledThreadPoolExecutor heartbeatScheduler;
    private final Set<Lease> leases = ConcurrentHashMap.newKeySet();

    /** Additive polling options; zero interval selects one third of the lease. */
    public record LeaseOptions(boolean heartbeat, int leaseSeconds, Duration heartbeatInterval) {
        public LeaseOptions {
            if (leaseSeconds != 0 && (leaseSeconds < 5 || leaseSeconds > 3600)) throw new IllegalArgumentException("leaseSeconds must be 0 or 5–3600");
            if (heartbeatInterval == null || heartbeatInterval.isNegative() || heartbeatInterval.compareTo(Duration.ofSeconds(leaseSeconds == 0 ? 30 : leaseSeconds).dividedBy(3)) > 0)
                throw new IllegalArgumentException("heartbeatInterval must be at most one third of the lease");
        }
        Duration interval() { return heartbeatInterval.isZero() ? Duration.ofSeconds(leaseSeconds == 0 ? 30 : leaseSeconds).dividedBy(3) : heartbeatInterval; }
    }

    /** An asynchronous fatal capability/configuration error, if intake stopped. */
    public Optional<Exception> failure() { return Optional.ofNullable(failure); }

    public Runner(String workerId, int parallelism, long pollIntervalMs, EngineClient engine, HandlerRegistry registry) {
        this(workerId, parallelism, pollIntervalMs, engine, registry, new Metrics(new SimpleMeterRegistry()));
    }

    public Runner(String workerId, int parallelism, long pollIntervalMs, EngineClient engine, HandlerRegistry registry, Metrics metrics) {
        this(workerId, parallelism, pollIntervalMs, engine, registry, metrics, new LeaseOptions(false, 0, Duration.ZERO));
    }

    public Runner(String workerId, int parallelism, long pollIntervalMs, EngineClient engine, HandlerRegistry registry, LeaseOptions options) {
        this(workerId, parallelism, pollIntervalMs, engine, registry, new Metrics(new SimpleMeterRegistry()), options);
    }

    public Runner(String workerId, int parallelism, long pollIntervalMs, EngineClient engine, HandlerRegistry registry, Metrics metrics, LeaseOptions options) {
        this.workerId = workerId;
        this.pollIntervalMs = pollIntervalMs > 0 ? pollIntervalMs : 500;
        this.engine = engine;
        this.registry = registry;
        this.metrics = metrics;
        this.slots = new Semaphore(parallelism > 0 ? parallelism : 64);
        this.leaseOptions = options;
        this.heartbeatScheduler = options.heartbeat() ? new ScheduledThreadPoolExecutor(1, task -> {
            Thread thread = new Thread(task, "workflow-lease-expiry-" + workerId); thread.setDaemon(true); return thread;
        }) : null;
        if (heartbeatScheduler != null) heartbeatScheduler.setRemoveOnCancelPolicy(true);
    }

    public synchronized void start() {
        if (started || executor.isShutdown()) throw new IllegalStateException("runner already started or stopped; create another after handlers stop");
        if (leaseOptions.heartbeat() && !(engine instanceof LeaseExtender)) throw new HeartbeatUnsupportedException();
        started = true;
        running = true;
        pollThread = new Thread(this::pollLoop, "workflow-runner-" + workerId);
        pollThread.setDaemon(true);
        pollThread.start();
        shutdownHook = new Thread(() -> stop(30));
        Runtime.getRuntime().addShutdownHook(shutdownHook);
    }

    /** Stop claims/retries, then drain handlers within the caller's timeout. */
    public void stop(int timeoutSeconds) {
        running = false;
        leases.forEach(Lease::cancel);
        if (heartbeatScheduler != null) heartbeatScheduler.shutdownNow();
        long end = System.nanoTime() + TimeUnit.SECONDS.toNanos(timeoutSeconds);
        if (pollThread != null) pollThread.interrupt();
        try {
            if (pollThread != null) pollThread.join(Math.max(1, timeoutSeconds * 1000L));
            executor.shutdown();
            if (!executor.awaitTermination(Math.max(0, end - System.nanoTime()), TimeUnit.NANOSECONDS)) executor.shutdownNow();
        } catch (InterruptedException interrupted) {
            executor.shutdownNow();
            Thread.currentThread().interrupt();
        } finally {
            if (shutdownHook != null) {
                try { Runtime.getRuntime().removeShutdownHook(shutdownHook); } catch (IllegalStateException shuttingDown) { }
            }
        }
    }

    private void fail(Exception error) {
        failure = error;
        LOG.severe("runner stopped: " + error);
        running = false;
        if (pollThread != null) pollThread.interrupt();
        leases.forEach(Lease::cancel);
        if (heartbeatScheduler != null) heartbeatScheduler.shutdownNow();
        executor.shutdownNow();
    }

    private void pollLoop() {
        List<String> jobTypes = List.copyOf(registry.jobTypes());
        while (running) {
            try {
                int available = slots.availablePermits();
                if (available == 0) { Thread.sleep(pollIntervalMs); continue; }
                List<Job> jobs;
                long start = System.nanoTime();
                try (var scope = CallDeadline.within(Duration.ofSeconds(5))) {
                    jobs = engine.pollJobs(workerId, jobTypes, available, leaseOptions.leaseSeconds());
                } finally { metrics.pollLatency.record(Duration.ofNanos(System.nanoTime() - start)); }
                if (!running) break;
                if (jobs == null || jobs.isEmpty()) {
                    metrics.lockConflicts.increment();
                    Thread.sleep(pollIntervalMs);
                    continue;
                }
                for (Job job : jobs) {
                    if (!running) break;
                    slots.acquire();
                    try {
                        executor.execute(() -> { try { dispatch(job); } finally { slots.release(); } });
                    } catch (RejectedExecutionException stopped) { slots.release(); throw stopped; }
                }
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
                break;
            } catch (Exception error) {
                if (error instanceof UnsupportedOperationException) { fail(error); break; }
                LOG.warning("runner: poll failed: " + error);
                try { Thread.sleep(pollIntervalMs); } catch (InterruptedException stopped) { break; }
            }
        }
    }

    private void dispatch(Job job) {
        if (!running || job.lockExpiresAt != null && !Instant.now().isBefore(job.lockExpiresAt)) return;
        Lease lease = null;
        try {
            if (leaseOptions.heartbeat()) {
                lease = new Lease((LeaseExtender) engine, job, workerId, leaseOptions, heartbeatScheduler, metrics, () -> running, this::fail);
                leases.add(lease);
                lease.start();
            }
            Lease ownedLease = lease;
            java.util.function.BooleanSupplier owned = () -> running && (ownedLease == null || ownedLease.valid());
            if (!owned.getAsBoolean() || Thread.currentThread().isInterrupted()) return;
            var outcome = Callbacks.execute(registry, job, metrics, owned);
            Callbacks.deliverRenewing(engine, workerId, job.id, outcome, owned,
                () -> ownedLease == null ? job.lockExpiresAt : ownedLease.deadline(), job.lockExpiresAt != null, metrics, "polling");
        } catch (Exception error) {
            if (leaseOptions.heartbeat() && error instanceof IllegalArgumentException) fail(error);
            LOG.warning("runner: unacknowledged job_id=" + job.id + ": " + error);
        } finally {
            if (lease != null) { lease.close(); leases.remove(lease); }
        }
    }
}
