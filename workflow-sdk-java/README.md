# Java SDK

**Candidate coordinates**: `io.github.batnam:workflow-sdk-java:1.0.2` (not published yet)

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
3. Each claimed job acquires a capacity slot and runs on a virtual thread (`parallelism` defaults to 64).
4. The thread calls your registered handler, then calls `completeJob` or `failJob` based on the result.

**Error handling**: throw a plain `Exception` → `failJob(retryable=true)` → engine retries up to `retryCount`. Throw `NonRetryableException` → `failJob(retryable=false)` → fails immediately regardless of retry budget.

For the full model (sequence diagram, retry flow, graceful shutdown), see [architecture.md — Worker polling model](../architecture.md#worker-polling-model).

---

## Minimal example — REST transport

```java
import com.batnam.rochallor_engine.workflow_sdk_java.client.RestEngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.HandlerRegistry;
import com.batnam.rochallor_engine.workflow_sdk_java.runner.Runner;
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
import com.batnam.rochallor_engine.workflow_sdk_java.client.GrpcEngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.HandlerRegistry;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.NonRetryableException;
import com.batnam.rochallor_engine.workflow_sdk_java.runner.Runner;

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

## Workflow administration

The Java clients expose the worker operations `pollJobs`, `completeJob` and
`failJob`. Upload definitions and start instances through the engine REST API
or Monitor; these administration methods are not part of this SDK.

---

## Kafka Dispatch (Opt-In)

The Java SDK supports push-based job dispatch via Kafka, which is ideal for high-throughput workloads.

### Usage

```java
import com.batnam.rochallor_engine.workflow_sdk_java.runner.KafkaRunner;

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

### TLS / SASL (production)

The basic config above connects to brokers in **plaintext with no authentication**. For production, pass standard Kafka client properties through the `extraKafkaProps` argument — TLS/SASL settings are merged into the consumer config; reserved delivery settings are listed below.

```java
import org.apache.kafka.clients.CommonClientConfigs;
import org.apache.kafka.common.config.SaslConfigs;
import org.apache.kafka.common.config.SslConfigs;

import java.util.Properties;
import com.batnam.rochallor_engine.workflow_sdk_java.runner.KafkaRunner;

// SASL/SCRAM-SHA-512 over TLS — common for MSK, Confluent Cloud, Aiven, Redpanda
var props = new Properties();
props.put(CommonClientConfigs.SECURITY_PROTOCOL_CONFIG, "SASL_SSL");
props.put(SaslConfigs.SASL_MECHANISM,                   "SCRAM-SHA-512");
props.put(SaslConfigs.SASL_JAAS_CONFIG,
    "org.apache.kafka.common.security.scram.ScramLoginModule required "
    + "username=\"" + System.getenv("KAFKA_SASL_USERNAME") + "\" "
    + "password=\"" + System.getenv("KAFKA_SASL_PASSWORD") + "\";");
// Optional — needed only when the broker uses a private CA
props.put(SslConfigs.SSL_TRUSTSTORE_LOCATION_CONFIG, "/etc/kafka/truststore.jks");
props.put(SslConfigs.SSL_TRUSTSTORE_PASSWORD_CONFIG, System.getenv("KAFKA_TRUSTSTORE_PASSWORD"));

var runner = new KafkaRunner(
    "java-worker-1",
    "kafka-1.prod:9093,kafka-2.prod:9093",
    client,
    registry,
    props
);
runner.start();
```

Other mechanisms:

```java
// SASL/PLAIN over TLS
props.put(CommonClientConfigs.SECURITY_PROTOCOL_CONFIG, "SASL_SSL");
props.put(SaslConfigs.SASL_MECHANISM, "PLAIN");
props.put(SaslConfigs.SASL_JAAS_CONFIG,
    "org.apache.kafka.common.security.plain.PlainLoginModule required "
    + "username=\"...\" password=\"...\";");

// SASL/OAUTHBEARER — e.g. AWS MSK IAM via aws-msk-iam-auth, Azure Event Hubs
props.put(SaslConfigs.SASL_MECHANISM, "OAUTHBEARER");
props.put(SaslConfigs.SASL_JAAS_CONFIG, "software.amazon.msk.auth.iam.IAMLoginModule required;");
props.put(SaslConfigs.SASL_CLIENT_CALLBACK_HANDLER_CLASS,
    "software.amazon.msk.auth.iam.IAMClientCallbackHandler");

// Mutual TLS only (no SASL)
props.put(CommonClientConfigs.SECURITY_PROTOCOL_CONFIG, "SSL");
props.put(SslConfigs.SSL_KEYSTORE_LOCATION_CONFIG, "/etc/kafka/keystore.jks");
props.put(SslConfigs.SSL_KEYSTORE_PASSWORD_CONFIG, "...");
props.put(SslConfigs.SSL_KEY_PASSWORD_CONFIG,       "...");
```

Plaintext brokers should be restricted to local dev (`docker compose`) — never deploy without `SASL_SSL` (or `SSL` with mutual TLS) on a shared network.

---

### KafkaRunner constructor reference

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `workerId` | String | *(required)* | Unique identifier for this worker. |
| `brokers` | String | *(required)* | Comma-separated list of Kafka brokers. |
| `engine` | `EngineClient` | *(required)* | REST or gRPC client for completion callbacks. |
| `registry` | `HandlerRegistry` | *(required)* | Maps job types to handlers. |
| `extraKafkaProps` | `Properties` | `null` | Overrides merged into the Kafka Consumer config — pass `security.protocol`, `sasl.*`, `ssl.*` here for production. |

---

## Runner constructor reference (Polling Mode)

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `workerId` | String | *(required)* | Unique identifier for this worker process. |
| `parallelism` | int | `64` | Maximum claimed jobs, including handlers and pending callbacks. |
| `pollIntervalMs` | long | `500` | Milliseconds to sleep between poll rounds when the queue is empty. |
| `engine` | `EngineClient` | *(required)* | REST or gRPC client. |
| `registry` | `HandlerRegistry` | *(required)* | Maps job types to handlers. |

### Delivery identity and retries

`jobId()` identifies one delivery attempt and changes on automatic retry or lease recovery. Callback requests and SDK method signatures are unchanged. For external side effects, use a stable business operation key from `variables()` (for example, a payment ID) with the destination's idempotency mechanism; do not deduplicate across retries using `jobId()`.

## Callback recovery and ownership (R08/R09)

Polling claims only free `parallelism` slots. Each slot includes the handler and its pending callback; an active handler keeps the slot even after lease expiry. `Job.lockExpiresAt` is an optional `Instant` mapped by REST/gRPC. Existing constructors and EngineClient methods remain compatible. Jobs without lease metadata receive one bounded callback attempt.

The runner snapshots success variables or failure reason/retryability. Callback transport errors never become handler failures. Attempts use a 5-second timeout, exponential backoff from 100 ms (±20% jitter, capped at 30 seconds), bounded by shutdown and the polling lease. `CallDeadline.timeout()` exposes the current budget to custom clients without adding abstract methods; custom implementations must honor timeout/interruption. Direct built-in client calls default to 30 seconds. No heartbeat is enabled.

`stop(timeoutSeconds)` stops polling/retrying, joins the poll thread, then drains handlers within the remaining timeout before interrupting them. Interruption cannot undo arbitrary side effects. Keep handlers shorter than the baseline 30-second lease and use business idempotency keys. Old callbacks after lease replacement do not advance the workflow.

Kafka disables auto-commit and fixes `max.poll.records=1`. Each partition pauses at its outstanding record while a separate virtual thread runs the handler/callback; the consumer keeps polling for group membership. Only acknowledged offsets are committed, explicitly per partition with a 5-second bound. Assignment tokens fence revoked/lost owners; retained pending partitions remain paused after rebalance. Dedup is recorded after callback acknowledgement, and pending outcomes do not expire with the dedup cache. The [Kafka consumer API](https://kafka.apache.org/39/javadoc/org/apache/kafka/clients/consumer/KafkaConsumer.html) describes this poll/pause/commit pattern.

Malformed records/empty IDs, permanent callback rejection and commit errors stop consumption with offsets uncommitted. Valid jobs with bad payloads or missing handlers send a non-retryable failure that must be acknowledged. Logs carry topic/partition/offset and job/dedup IDs. TLS/SASL properties remain configurable; group, deserializers, auto-commit and one-record polling are reserved safety settings. Call `KafkaRunner.close()` on shutdown.

Both runner constructors accept an optional final `Metrics` argument. Micrometer exposes callback pending/retry/oldest-age/rejection meters with finite `dispatch`/`callback` labels, plus `workflow_sdk_kafka_record_errors_total`. Watch pending age, lease recoveries and Kafka lag together with actual workflow outcomes. Repair payload/credential/transport failures before restarting; do not reset offsets past blocked work. Drain and canary one group. Rollback pins the prior worker/JAR and can restore A1/A6/A7. No engine/schema migration is required.

## Verify and publish the candidate

Run `python3 scripts/verify-package.py` with JDK 21 available. It runs Gradle tests, builds an unsigned Maven repository, compiles/runs an external consumer and rejects a deliberately broken JAR. The E2E worker resolves that checksummed Maven artifact. Version is selected before build (`RELEASE_VERSION=sdk-java-vX.Y.Z` for verification, `-Pversion=X.Y.Z` for Gradle).

The tag workflow depends on verification, restores the verified JAR/POM/module metadata, checks source/version/checksums and signs those bytes with Maven Central credentials. Archive and metadata regeneration are skipped under `-PverifiedCandidate`. Creating a tag/publishing remains a separate release action.

## Optional polling heartbeat (1.1.0)

Heartbeat is off in existing constructors. Enable it only after all engine
replicas implement `ExtendJobLease` (engine candidate v1.2.0 / R15):

```java
var options = new Runner.LeaseOptions(true, 30, java.time.Duration.ofSeconds(10));
var runner = new Runner("worker-1", 16, 500, client, registry, options);
runner.start();
// The service supervisor can inspect runner.failure() for fatal async errors.
```

`LeaseExtender` is an optional interface. `EngineClient` still has only the three
original abstract methods; a default overload accepts `pollJobs(...,
leaseSeconds)`. Existing custom clients accept the default 0, and explicitly
reject non-default lease selection unless they override the overload. Built-in
clients support 0 (engine default 30 seconds) or 5–3600 seconds.

The first renewal must succeed before handler execution. A separate daemon platform
thread renews throughout the handler and callback acknowledgement retries. One
scheduler watches expiration independently of blocked handlers/transports; it
never performs network I/O. Renewal does not share the virtual-thread carrier
pool with CPU-bound handlers; the external consumer gate verifies this with one
carrier. Each active heartbeat attempt adds one daemon platform thread. Each request uses `CallDeadline` with at most 5
seconds and the current lease; transient errors back off within that lease.
`HeartbeatUnsupportedException` stops intake, is logged, and is exposed through
`failure()` (missing client capability is rejected synchronously by `start()`).
`LeaseLostException` stops renewal/retries and interrupts the old handler without
sending a failure callback or consuming a business retry.

Handlers should honor interruption and/or `JobContext.isCancelled()`. Ignoring
interruption retains the slot until the handler actually returns. `stop(seconds)`
cancels heartbeat work immediately, stops polling and gives handler threads a
bounded drain period; interruption cannot undo external side effects. A runner
is single-use: stop the old work before creating another. Custom clients must
honor `CallDeadline.timeout()` and interruption to release network resources.
A custom heartbeat call that ignores both may outlive shutdown on its daemon
thread, but expiration still fences its attempt. Process pauses and JVM/OS
starvation can also lose a lease; use idempotency for external effects.

Monitor `workflow_sdk_heartbeat_total{result="accepted|retry|lease_lost|rejected"}`,
callback pending age, replacement attempts, and engine DB writes/latency. Canary
with heartbeat off, then enable for a small group. To roll back, stop intake for
tasks depending on renewal, drain them, then disable heartbeat and pin the prior
SDK. Complete this before rolling back the engine.
