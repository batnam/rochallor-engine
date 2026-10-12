package com.batnam.rochallor_engine.workflow_sdk_java.runner;

import com.batnam.rochallor_engine.workflow_sdk_java.client.*;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.*;
import org.junit.jupiter.api.Test;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;

class HeartbeatTest {
    static class Engine implements EngineClient, LeaseExtender {
        final AtomicInteger polls = new AtomicInteger(), beats = new AtomicInteger(), failures = new AtomicInteger();
        final CountDownLatch completed = new CountDownLatch(1);
        final Job job = new Job();
        Engine() {
            job.id = "job"; job.jobType = "work"; job.instanceId = "instance"; job.retriesRemaining = 2;
            job.lockExpiresAt = Instant.now().plusMillis(500);
        }
        public List<Job> pollJobs(String worker, List<String> types, int max) { return polls.incrementAndGet() == 1 ? List.of(job) : List.of(); }
        public Instant extendJobLease(String job, String worker, int seconds) throws Exception { beats.incrementAndGet(); return Instant.now().plusMillis(500); }
        public void completeJob(String id, String worker, Map<String,Object> variables) { completed.countDown(); }
        public void failJob(String id, String worker, String message, boolean retryable) { failures.incrementAndGet(); }
    }

    static Runner runner(EngineClient engine, HandlerRegistry registry) {
        return new Runner("worker", 1, 5, engine, registry, new Runner.LeaseOptions(true, 0, Duration.ofMillis(25)));
    }

    @Test void renewsThroughHandlerAndImmutableCallbackRetry() throws Exception {
        var callbacks = new AtomicInteger();
        Instant release = Instant.now().plusMillis(1050);
        var engine = new Engine() {
            @Override public void completeJob(String id, String worker, Map<String,Object> variables) {
                callbacks.incrementAndGet(); assertEquals(1, variables.get("frozen")); variables.put("frozen", 9);
                if (Instant.now().isBefore(release)) throw new RuntimeException("unavailable");
                completed.countDown();
            }
        };
        var handlerCalls = new AtomicInteger();
        var registry = new HandlerRegistry();
        registry.register("work", context -> { handlerCalls.incrementAndGet(); Thread.sleep(750); return Map.of("frozen", 1); });
        var runner = runner(engine, registry);
        runner.start();
        try { assertTrue(engine.completed.await(3, TimeUnit.SECONDS)); }
        finally { runner.stop(1); }
        assertEquals(1, handlerCalls.get()); assertTrue(engine.beats.get() >= 10);
        assertTrue(callbacks.get() > 1); assertEquals(0, engine.failures.get());
        assertTrue(runner.failure().isEmpty());
    }

    @Test void lostLeaseRetainsSlotAndShutdownDoesNotWaitForUncooperativeHandler() throws Exception {
        var engine = new Engine() {
            @Override public Instant extendJobLease(String job, String worker, int seconds) {
                if (beats.incrementAndGet() > 1) throw new LeaseLostException();
                return Instant.now().plusMillis(500);
            }
        };
        var release = new CountDownLatch(1);
        var entered = new CountDownLatch(1);
        var context = new AtomicReference<JobContext>();
        var registry = new HandlerRegistry();
        registry.register("work", ctx -> {
            context.set(ctx); entered.countDown();
            while (release.getCount() > 0) { try { release.await(); } catch (InterruptedException ignored) { } }
            return Map.of();
        });
        var runner = runner(engine, registry); runner.start();
        try {
            assertTrue(entered.await(1, TimeUnit.SECONDS)); Thread.sleep(100);
            assertTrue(context.get().isCancelled()); assertEquals(1, engine.polls.get());
            long start = System.nanoTime(); runner.stop(0);
            assertTrue(System.nanoTime() - start < TimeUnit.MILLISECONDS.toNanos(200));
            assertThrows(IllegalStateException.class, runner::start);
        } finally { release.countDown(); runner.stop(1); }
        assertEquals(1, engine.completed.getCount()); assertEquals(0, engine.failures.get());
    }

    @Test void unsupportedEngineStopsIntakeBeforeHandler() throws Exception {
        var engine = new Engine() {
            @Override public Instant extendJobLease(String id, String worker, int seconds) { throw new HeartbeatUnsupportedException(); }
        };
        var calls = new AtomicInteger(); var registry = new HandlerRegistry();
        registry.register("work", context -> { calls.incrementAndGet(); return Map.of(); });
        var runner = runner(engine, registry); runner.start();
        try {
            for (int i = 0; i < 100 && runner.failure().isEmpty(); i++) Thread.sleep(10);
            assertInstanceOf(HeartbeatUnsupportedException.class, runner.failure().orElseThrow());
        } finally { runner.stop(1); }
        assertEquals(0, calls.get()); assertEquals(0, engine.failures.get());
    }

    @Test void legacyClientIsCompatibleWithHeartbeatOffAndRejectedWhenEnabled() {
        EngineClient legacy = new RunnerTest.FakeEngine(new java.util.ArrayList<>());
        var registry = new HandlerRegistry();
        var runner = new Runner("worker", 1, 5, legacy, registry);
        runner.start(); runner.stop(1);
        assertThrows(HeartbeatUnsupportedException.class, () -> runner(legacy, registry).start());
    }

    @Test void hungHeartbeatCannotReviveExpiredAttempt() throws Exception {
        var release = new CountDownLatch(1);
        var engine = new Engine() {
            @Override public Instant extendJobLease(String id, String worker, int seconds) {
                beats.incrementAndGet();
                while (release.getCount() > 0) { try { release.await(); } catch (InterruptedException ignored) { } }
                return Instant.now().plusSeconds(30);
            }
        };
        var calls = new AtomicInteger(); var registry = new HandlerRegistry();
        registry.register("work", context -> { calls.incrementAndGet(); return Map.of(); });
        var runner = runner(engine, registry); runner.start();
        try { Thread.sleep(650); assertEquals(0, calls.get()); assertEquals(1, engine.beats.get()); }
        finally { release.countDown(); runner.stop(1); }
        assertEquals(0, engine.failures.get());
    }
}
