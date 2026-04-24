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
}
