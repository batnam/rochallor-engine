# Java SDK

**Coordinates**: `com.batnam.rochallor-engine:workflow-sdk-java:1.0.0`

## Key types

| Package | Type | Purpose |
|---------|------|---------|
| `client` | `RestEngineClient(baseUrl)` | REST client using JDK `HttpClient` |
| `client` | `GrpcEngineClient(target)` | gRPC client (generates stubs at build time via Gradle protobuf plugin) |
| `client` | `EngineClient` interface | Transport abstraction |
| `client` | `Job` | Job record returned by `pollJobs` |
| `handler` | `HandlerRegistry` | Maps `jobType` strings to `JobHandler` instances |
| `handler` | `JobContext` | Passed to every handler — exposes `jobId()`, `instanceId()`, `jobType()`, `retriesRemaining()`, `get(key)`, `variables()` |
| `handler` | `JobHandler` | `@FunctionalInterface` — `Map<String, Object> handle(JobContext)` |
| `handler` | `NonRetryableException` | Throw from a handler to bypass the retry budget |
| `runner` | `Runner(workerId, parallelism, pollIntervalMs, engine, registry)` | Poll/dispatch loop |
| `runner` | `Runner.start()` | Starts the poll loop in a daemon thread; installs a JVM shutdown hook |
| `runner` | `Runner.stop(timeoutSeconds)` | Graceful shutdown with drain timeout |

---

## How the runner works

`new HandlerRegistry()` + `registry.register(...)` just build a `jobType → JobHandler` map in memory — no connection, no I/O. The `Runner` is what drives everything:

1. A background daemon thread fires every `pollIntervalMs` (default 500 ms) and calls `POST /v1/jobs/poll`.
2. The engine claims available jobs atomically with `FOR UPDATE SKIP LOCKED` and returns them.
3. Each job is submitted to a thread pool (bounded by `parallelism`, default 64).
4. The thread calls your registered handler, then calls `completeJob` or `failJob` based on the result.

**Error handling**: throw a plain `Exception` → `failJob(retryable=true)` → engine retries up to `retryCount`. Throw `NonRetryableException` → `failJob(retryable=false)` → fails immediately regardless of retry budget.

For the full model (sequence diagram, retry flow, graceful shutdown), see [architecture.md — Worker polling model](../architecture.md#worker-polling-model).

---

## Minimal example — REST transport

```java
import com.batnam.workflow.sdk.client.RestEngineClient;
import com.batnam.workflow.sdk.handler.HandlerRegistry;
import com.batnam.workflow.sdk.runner.Runner;
import java.util.Map;

public class Worker {
    public static void main(String[] args) throws InterruptedException {
        var client   = new RestEngineClient("http://localhost:8080");
        var registry = new HandlerRegistry();

        registry.register("process-order", ctx -> {
            String orderId = (String) ctx.get("orderId").orElseThrow();
            // ... process order ...
            return Map.of("processed", true, "orderId", orderId);
        });

        // parallelism=64, pollIntervalMs=500
        var runner = new Runner("java-worker-1", 64, 500, client, registry);
        runner.start();

        // Block the main thread — runner runs on a daemon thread.
        // The JVM shutdown hook calls runner.stop(30) automatically.
        Thread.currentThread().join();
    }
}
```

---

## Full demo — multiple handlers, non-retryable errors, gRPC transport

```java
import com.batnam.workflow.sdk.client.GrpcEngineClient;
import com.batnam.workflow.sdk.handler.HandlerRegistry;
import com.batnam.workflow.sdk.handler.NonRetryableException;
import com.batnam.workflow.sdk.runner.Runner;

import java.time.Instant;
import java.util.Map;
import java.util.logging.Logger;

public class LoanWorker {
    private static final Logger LOG = Logger.getLogger(LoanWorker.class.getName());

    public static void main(String[] args) throws InterruptedException {
        // Use gRPC transport — swap for new RestEngineClient("http://...") to use REST
        var client   = new GrpcEngineClient("localhost:9090");
        var registry = new HandlerRegistry();

        // Handler: validate-application
        registry.register("validate-application", ctx -> {
            String applicantId = (String) ctx.get("applicantId")
                    .orElseThrow(() -> new NonRetryableException("applicantId is required"));
            LOG.info("Validating applicant " + applicantId + " (retries left: " + ctx.retriesRemaining() + ")");
            // ... call validation service ...
            return Map.of(
                    "validationPassed", true,
                    "validatedAt", Instant.now().toString()
            );
        });

        // Handler: credit-score
        // Plain Exception → retryable (runner calls failJob(retryable=true))
        // NonRetryableException → permanent failure (retryable=false)
        registry.register("credit-score", ctx -> {
            String applicantId = (String) ctx.variables().get("applicantId");
            int score = fetchCreditScore(applicantId); // may throw on transient error
            return Map.of("creditScore", score);
        });

        // Handler: send-notification (fire-and-forget, no output variables)
        registry.register("send-notification", ctx -> {
            String email = (String) ctx.variables().getOrDefault("email", "");
            LOG.info("Sending notification to " + email);
            // ... send email ...
            return Map.of("notificationSent", true);
        });

        var runner = new Runner(
                "java-worker-1",
                32,    // parallelism
                250,   // pollIntervalMs
                client,
                registry
        );
        runner.start();

        // Wait indefinitely — shutdown hook handles graceful drain
        Thread.currentThread().join();
    }

    static int fetchCreditScore(String applicantId) {
        return 720; // placeholder
    }
}
```

---

## Upload a definition from Java

```java
import com.batnam.workflow.sdk.client.RestEngineClient;
import java.util.List;
import java.util.Map;

public class UploadDefinition {
    public static void main(String[] args) throws Exception {
        var client = new RestEngineClient("http://localhost:8080");

        var definition = Map.of(
            "id",   "greet-workflow",
            "name", "Greet Workflow",
            "steps", List.of(
                Map.of("id", "say-hello", "name", "Say Hello",
                       "type", "SERVICE_TASK", "jobType", "greet", "nextStep", "end"),
                Map.of("id", "end", "name", "End", "type", "END")
            )
        );

        client.uploadDefinition(definition);
        var instance = client.startInstance("greet-workflow", Map.of("name", "Alice"), null, null);
        System.out.println("Started instance: " + instance.get("id"));
    }
}
```

---

## Kafka Dispatch (Opt-In)

The Java SDK supports push-based job dispatch via Kafka, which is ideal for high-throughput workloads.

### Usage

```java
import com.batnam.workflow.sdk.runner.KafkaRunner;

public class KafkaWorker {
    public static void main(String[] args) throws InterruptedException {
        // ... setup client and registry ...
        
        var runner = new KafkaRunner(
            "java-worker-1",
            "localhost:9092",
            client,
            registry,
            null // extra Kafka properties
        );
        
        runner.start();
        Thread.currentThread().join();
    }
}
```

### KafkaRunner constructor reference

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `workerId` | String | *(required)* | Unique identifier for this worker. |
| `brokers` | String | *(required)* | Comma-separated list of Kafka brokers. |
| `engine` | `EngineClient` | *(required)* | REST or gRPC client for completion callbacks. |
| `registry` | `HandlerRegistry` | *(required)* | Maps job types to handlers. |
| `extraKafkaProps` | `Properties` | `null` | Optional overrides for the Kafka Consumer. |

---

## Runner constructor reference (Polling Mode)

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `workerId` | String | *(required)* | Unique identifier for this worker process. |
| `parallelism` | int | `64` | Thread pool size — maximum concurrent jobs. |
| `pollIntervalMs` | long | `500` | Milliseconds to sleep between poll rounds when the queue is empty. |
| `engine` | `EngineClient` | *(required)* | REST or gRPC client. |
| `registry` | `HandlerRegistry` | *(required)* | Maps job types to handlers. |
