package com.batnam.rochallor_engine.workflow_sdk_java.runner;

import com.batnam.rochallor_engine.workflow_sdk_java.client.EngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.client.Job;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.HandlerRegistry;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.NonRetryableException;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.*;

class RunnerTest {

    /** Fake EngineClient that hands out a single job then stays empty. */
    static class FakeEngine implements EngineClient {
        final AtomicInteger completed = new AtomicInteger();
        final AtomicInteger failed    = new AtomicInteger();
        volatile boolean retryable;
        private final List<Job> queue;

        FakeEngine(List<Job> queue) { this.queue = queue; }

        @Override
        public synchronized List<Job> pollJobs(String w, List<String> types, int max) {
            if (queue.isEmpty()) return List.of();
            return List.of(queue.remove(0));
        }

        @Override
        public void completeJob(String id, String w, Map<String, Object> v) {
            completed.incrementAndGet();
        }

        @Override
        public void failJob(String id, String w, String msg, boolean r) {
            failed.incrementAndGet();
            retryable = r;
        }
    }

    @Test
    void dispatchesAndCompletes() throws InterruptedException {
        Job j = new Job(); j.id = "j1"; j.jobType = "hello"; j.instanceId = "i1";
        FakeEngine engine = new FakeEngine(new java.util.ArrayList<>(List.of(j)));

        HandlerRegistry reg = new HandlerRegistry();
        reg.register("hello", ctx -> Map.of("done", true));

        Runner runner = new Runner("w1", 1, 50, engine, reg);
        runner.start();
        Thread.sleep(300);
        runner.stop(2);

        assertEquals(1, engine.completed.get());
        assertEquals(0, engine.failed.get());
    }

    @Test
    void nonRetryableFailureCallsFailJobWithRetryableFalse() throws InterruptedException {
        Job j = new Job(); j.id = "j2"; j.jobType = "bad"; j.instanceId = "i2";
        FakeEngine engine = new FakeEngine(new java.util.ArrayList<>(List.of(j)));

        HandlerRegistry reg = new HandlerRegistry();
        reg.register("bad", ctx -> { throw new NonRetryableException("fatal"); });

        Runner runner = new Runner("w1", 1, 50, engine, reg);
        runner.start();
        Thread.sleep(300);
        runner.stop(2);

        assertEquals(0, engine.completed.get());
        assertEquals(1, engine.failed.get());
        assertFalse(engine.retryable);
    }

    @Test
    void callbackFailureDoesNotBecomeBusinessFailure() throws Exception {
        var mapper = new com.fasterxml.jackson.databind.ObjectMapper().findAndRegisterModules();
        Job job = mapper.readValue("{\"id\":\"j1\",\"jobType\":\"test\",\"lockExpiresAt\":\"2099-01-01T00:00:00Z\"}", Job.class);
        AtomicInteger calls = new AtomicInteger();
        FakeEngine engine = new FakeEngine(new java.util.ArrayList<>(List.of(job))) {
            @Override public void completeJob(String id, String worker, Map<String,Object> variables) {
                if (completed.incrementAndGet() == 1) throw new RuntimeException("lost response");
            }
        };
        HandlerRegistry registry = new HandlerRegistry();
        registry.register("test", ctx -> { calls.incrementAndGet(); return Map.of("v", 1); });
        Runner runner = new Runner("w", 1, 5, engine, registry);
        runner.start();
        try { Thread.sleep(350); } finally { runner.stop(2); }
        assertEquals(2, engine.completed.get());
        assertEquals(0, engine.failed.get());
        assertEquals(1, calls.get());
    }

    @Test
    void multipleBatchesRespectRemainingCapacity() throws Exception {
        var release = new java.util.concurrent.CountDownLatch(1);
        var entered = new java.util.concurrent.CountDownLatch(2);
        var requested = new java.util.concurrent.CopyOnWriteArrayList<Integer>();
        EngineClient engine = new FakeEngine(new java.util.ArrayList<>()) {
            @Override public List<Job> pollJobs(String worker, List<String> types, int max) {
                requested.add(max);
                Job job = new Job(); job.id = "j" + requested.size(); job.jobType = "test";
                return List.of(job);
            }
        };
        HandlerRegistry registry = new HandlerRegistry();
        registry.register("test", ctx -> { entered.countDown(); release.await(); return Map.of(); });
        Runner runner = new Runner("w", 2, 10, engine, registry);
        runner.start();
        try {
            assertTrue(entered.await(1, java.util.concurrent.TimeUnit.SECONDS));
            Thread.sleep(50);
            assertEquals(List.of(2, 1), requested);
        } finally { release.countDown(); runner.stop(2); }
    }

    @Test
    void pendingCallbackKeepsCapacityUntilLeaseExpiry() throws Exception {
        Job job = new Job(); job.id = "pending"; job.jobType = "test"; job.lockExpiresAt = java.time.Instant.now().plusMillis(300);
        var requests = new AtomicInteger();
        var metricsRegistry = new io.micrometer.core.instrument.simple.SimpleMeterRegistry();
        FakeEngine engine = new FakeEngine(new java.util.ArrayList<>(List.of(job))) {
            @Override public synchronized List<Job> pollJobs(String worker, List<String> kinds, int max) {
                requests.incrementAndGet(); return super.pollJobs(worker, kinds, max);
            }
            @Override public void completeJob(String id, String worker, Map<String,Object> vars) {
                completed.incrementAndGet();
                assertEquals(1, vars.get("v"));
                vars.put("v", 9);
                throw new RuntimeException("unavailable");
            }
        };
        HandlerRegistry registry = new HandlerRegistry(); registry.register("test", ctx -> Map.of("v", 1));
        Runner runner = new Runner("w", 1, 5, engine, registry, new com.batnam.rochallor_engine.workflow_sdk_java.metrics.Metrics(metricsRegistry));
        runner.start();
        try {
            Thread.sleep(150);
            assertEquals(1, requests.get());
            assertTrue(engine.completed.get() >= 2);
            Thread.sleep(230);
            int attempts = engine.completed.get();
            Thread.sleep(120);
            assertEquals(attempts, engine.completed.get());
            assertEquals(0, engine.failed.get());
        } finally { runner.stop(2); }
        assertEquals(0, metricsRegistry.get("workflow_sdk_callback_pending").tags("dispatch", "polling", "callback", "complete").gauge().value());
    }

    @Test
    void slowHandlerKeepsSlotAfterExpiryAndSendsNoStaleCallback() throws Exception {
        Job job = new Job(); job.id = "slow"; job.jobType = "test"; job.lockExpiresAt = java.time.Instant.now().plusMillis(60);
        var release = new java.util.concurrent.CountDownLatch(1);
        var entered = new java.util.concurrent.CountDownLatch(1);
        var requests = new AtomicInteger();
        FakeEngine engine = new FakeEngine(new java.util.ArrayList<>(List.of(job))) {
            @Override public synchronized List<Job> pollJobs(String worker, List<String> kinds, int max) {
                requests.incrementAndGet(); return super.pollJobs(worker, kinds, max);
            }
        };
        HandlerRegistry registry = new HandlerRegistry();
        registry.register("test", ctx -> { entered.countDown(); release.await(); return Map.of(); });
        Runner runner = new Runner("w", 1, 5, engine, registry);
        runner.start();
        try {
            assertTrue(entered.await(1, java.util.concurrent.TimeUnit.SECONDS));
            Thread.sleep(100);
            assertEquals(1, requests.get());
        } finally { release.countDown(); runner.stop(2); }
        assertEquals(0, engine.completed.get());
        assertEquals(0, engine.failed.get());
    }

    @Test
    void legacyClientWithoutLeaseReceivesOneAttempt() throws Exception {
        Job job = new Job(); job.id = "legacy"; job.jobType = "test";
        FakeEngine engine = new FakeEngine(new java.util.ArrayList<>(List.of(job))) {
            @Override public void completeJob(String id, String worker, Map<String,Object> vars) {
                completed.incrementAndGet(); throw new RuntimeException("unavailable");
            }
        };
        HandlerRegistry registry = new HandlerRegistry(); registry.register("test", ctx -> Map.of());
        Runner runner = new Runner("w", 1, 5, engine, registry);
        runner.start();
        try { Thread.sleep(200); } finally { runner.stop(2); }
        assertEquals(1, engine.completed.get());
        assertEquals(0, engine.failed.get());
    }
}
