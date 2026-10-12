import com.batnam.rochallor_engine.workflow_sdk_java.client.*;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.*;
import com.batnam.rochallor_engine.workflow_sdk_java.runner.*;
import com.batnam.rochallor_engine.workflow_sdk_java.metrics.Metrics;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import java.util.List;
import java.util.Map;

public class ConsumerSmoke {
    public static void main(String[] args) throws Exception {
        EngineClient legacy = new EngineClient() {
            public List<Job> pollJobs(String worker, List<String> types, int max) { return List.of(); }
            public void completeJob(String id, String worker, Map<String,Object> variables) { }
            public void failJob(String id, String worker, String reason, boolean retryable) { }
        };
        HandlerRegistry registry = new HandlerRegistry();
        registry.register("smoke", ctx -> Map.of("ok", true));
        Metrics metrics = new Metrics(new SimpleMeterRegistry());
        Runner runner = new Runner("consumer", 1, 10, legacy, registry, metrics);
        runner.start(); runner.stop(1);
        if (runner.failure().isPresent()) throw new AssertionError("legacy client failed");
        try (KafkaRunner kafka = new KafkaRunner("consumer", "localhost:1", legacy, registry, null, metrics)) { }
        EngineClient rest = new RestEngineClient("http://localhost:1");
        GrpcEngineClient grpc = new GrpcEngineClient("localhost:1");
        grpc.shutdown();
        Job job = new Job(); job.lockExpiresAt = java.time.Instant.now();
        if (rest == null || job.lockExpiresAt == null) throw new AssertionError();
        heartbeatDuringCpuWork();
        System.out.println("External Maven consumer passed: REST, gRPC, legacy client, polling, Kafka and metrics");
    }

    private static void heartbeatDuringCpuWork() throws Exception {
        class CpuEngine implements EngineClient, LeaseExtender {
            final java.util.concurrent.atomic.AtomicInteger polls = new java.util.concurrent.atomic.AtomicInteger();
            final java.util.concurrent.atomic.AtomicInteger beats = new java.util.concurrent.atomic.AtomicInteger();
            final java.util.concurrent.CountDownLatch completed = new java.util.concurrent.CountDownLatch(1);
            final Job job = new Job();
            CpuEngine() { job.id = "cpu"; job.jobType = "cpu"; job.lockExpiresAt = java.time.Instant.now().plusMillis(500); }
            public List<Job> pollJobs(String worker, List<String> types, int max) { return polls.incrementAndGet() == 1 ? List.of(job) : List.of(); }
            public java.time.Instant extendJobLease(String id, String worker, int seconds) { beats.incrementAndGet(); return java.time.Instant.now().plusMillis(500); }
            public void completeJob(String id, String worker, Map<String,Object> variables) { completed.countDown(); }
            public void failJob(String id, String worker, String reason, boolean retryable) { throw new AssertionError("CPU handler failed"); }
        }
        var engine = new CpuEngine();
        var registry = new HandlerRegistry();
        registry.register("cpu", context -> {
            long end = System.nanoTime() + java.util.concurrent.TimeUnit.SECONDS.toNanos(1);
            while (System.nanoTime() < end) { Thread.onSpinWait(); }
            return Map.of("ok", true);
        });
        var runner = new Runner("cpu-consumer", 1, 5, engine, registry,
                new Runner.LeaseOptions(true, 0, java.time.Duration.ofMillis(25)));
        runner.start();
        try {
            if (!engine.completed.await(4, java.util.concurrent.TimeUnit.SECONDS) || engine.beats.get() < 3)
                throw new AssertionError("CPU handler starved heartbeat; accepted renewals=" + engine.beats.get());
        } finally { runner.stop(1); }
        System.out.println("CPU handler with one virtual-thread carrier: renewal and completion passed");
    }
}
