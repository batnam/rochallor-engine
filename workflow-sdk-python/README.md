# Python SDK

**Package**: `rochallor-sdk` ([PyPI](https://pypi.org/project/rochallor-sdk/))

> The Python SDK supports **REST only**. A gRPC transport is not implemented.

## Key types

| Module | Type | Purpose |
|--------|------|---------|
| `workflow_sdk.client.rest` | `RestEngineClient(base_url, timeout?)` | REST client using `httpx` |
| `workflow_sdk.client.interface` | `EngineClient` protocol | Transport abstraction |
| `workflow_sdk.handler.registry` | `HandlerRegistry` | Maps `jobType` strings to handler callables |
| `workflow_sdk.runner.runner` | `Runner(client, registry, worker_id, ...)` | Poll/dispatch loop |
| `workflow_sdk.runner.runner` | `Runner.run(stop_event?)` | Blocks until `stop_event` is set or SIGINT/SIGTERM received |
| `workflow_sdk.errors` | `NonRetryableError` | Raise from a handler to bypass the retry budget |
| `workflow_sdk.errors` | `EngineClientError` | Raised on non-2xx HTTP responses — carries `.status_code` and `.message` |

The handler function receives a `dict` with the following keys:

| Key | Type | Description |
|-----|------|-------------|
| `id` | str | Job ID |
| `jobType` | str | Job type string |
| `instanceId` | str | Workflow instance ID |
| `stepExecutionId` | str | Step execution ID |
| `retriesRemaining` | int | Retry budget remaining |
| `variables` | dict | Current workflow variables |

Return a `dict` of variables to merge into the instance, or `None` / `{}` for no output.

---

## How the runner works

`HandlerRegistry()` + `registry.register(...)` just build a `jobType → callable` map in memory — no connection, no I/O. The `Runner` is what drives everything:

1. A loop fires every `poll_interval` seconds (default 0.5 s) and calls `POST /v1/jobs/poll`.
2. The engine claims available jobs atomically with `FOR UPDATE SKIP LOCKED` and returns them.
3. Each job is submitted to a `ThreadPoolExecutor` (bounded by `parallelism`, default 64).
4. The thread calls your registered handler, then calls `complete_job` or `fail_job` based on the result.

**Error handling**: raise a plain `Exception` → `fail_job(retryable=True)` → engine retries up to `retryCount`. Raise `NonRetryableError` → `fail_job(retryable=False)` → fails immediately regardless of retry budget.

For the full model (sequence diagram, retry flow, graceful shutdown), see [architecture.md — Worker polling model](../architecture.md#worker-polling-model).

---

## Minimal example

```python
import threading
from workflow_sdk.client.rest import RestEngineClient
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.runner.runner import Runner

client   = RestEngineClient("http://localhost:8080")
registry = HandlerRegistry()

def process_order(ctx):
    order_id = ctx["variables"]["orderId"]
    # ... process order ...
    return {"processed": True, "orderId": order_id}

registry.register("process-order", process_order)

# Runner installs SIGINT/SIGTERM handlers automatically when stop_event is None
Runner(client=client, registry=registry, worker_id="py-worker-1").run()
```

---

## Full demo — multiple handlers, non-retryable errors, context manager

```python
import logging
import threading
from datetime import datetime, timezone

from workflow_sdk.client.rest import RestEngineClient
from workflow_sdk.errors import EngineClientError, NonRetryableError
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.runner.runner import Runner

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger(__name__)


def validate_application(ctx: dict) -> dict:
    """Validate the loan application — non-retryable on bad input."""
    applicant_id = ctx["variables"].get("applicantId")
    if not applicant_id:
        raise NonRetryableError("applicantId is required and must be a non-empty string")

    retries_left = ctx["retriesRemaining"]
    log.info("Validating applicant %s (retries left: %d)", applicant_id, retries_left)
    # ... call validation service ...
    return {
        "validationPassed": True,
        "validatedAt": datetime.now(timezone.utc).isoformat(),
    }


def credit_score(ctx: dict) -> dict:
    """Fetch credit score — retryable on transient errors."""
    applicant_id = ctx["variables"]["applicantId"]
    score = fetch_credit_score(applicant_id)  # may raise on network error → retryable
    return {"creditScore": score}


def send_notification(ctx: dict) -> dict | None:
    """Send email notification — no output variables."""
    email = ctx["variables"].get("email", "")
    log.info("Sending notification to %s (jobId=%s)", email, ctx["id"])
    # ... send email ...
    return {"notificationSent": True}


def upload_workflow(client: RestEngineClient) -> None:
    """Upload the loan workflow definition if not already present."""
    try:
        client.get_definition("loan-processing-workflow")
        log.info("Definition already exists — skipping upload")
        return
    except EngineClientError as e:
        if e.status_code != 404:
            raise

    definition = {
        "id":   "loan-processing-workflow",
        "name": "Loan Processing Workflow",
        "steps": [
            {"id": "validate",      "name": "Validate Application",
             "type": "SERVICE_TASK", "jobType": "validate-application", "nextStep": "score"},
            {"id": "score",         "name": "Credit Score Check",
             "type": "SERVICE_TASK", "jobType": "credit-score",         "nextStep": "notify"},
            {"id": "notify",        "name": "Send Notification",
             "type": "SERVICE_TASK", "jobType": "send-notification",    "nextStep": "end"},
            {"id": "end",           "name": "Done",                     "type": "END"},
        ],
    }
    result = client.upload_definition(definition)
    log.info("Uploaded definition: %s v%s", result["id"], result["version"])


def main() -> None:
    # RestEngineClient is a context manager — closes the httpx.Client on exit
    with RestEngineClient("http://localhost:8080", timeout=10.0) as client:
        upload_workflow(client)

        registry = HandlerRegistry()
        registry.register("validate-application", validate_application)
        registry.register("credit-score",         credit_score)
        registry.register("send-notification",    send_notification)

        runner = Runner(
            client=client,
            registry=registry,
            worker_id="py-worker-1",
            parallelism=16,
            poll_interval=0.25,
        )
        # Blocks until SIGINT/SIGTERM; gives in-flight jobs a bounded drain period
        runner.run()


if __name__ == "__main__":
    main()


def fetch_credit_score(applicant_id: str) -> int:
    return 720  # placeholder
```

---

## Full REST admin API

`RestEngineClient` exposes all engine operations, not just the worker interface:

```python
from workflow_sdk.client.rest import RestEngineClient

client = RestEngineClient("http://localhost:8080")

# Definitions
result  = client.upload_definition(definition_dict)   # → {"id": ..., "version": ...}
defn    = client.get_definition("my-workflow")         # → definition dict
page    = client.list_definitions(keyword="loan", page=0, page_size=20)

# Instances
instance = client.start_instance(
    "my-workflow",
    variables={"applicantId": "A-001"},
    business_key="APP-2024-001",          # optional correlation key
)
state    = client.get_instance(instance["id"])
history  = client.get_instance_history(instance["id"])   # → list of step execution dicts
client.cancel_instance(instance["id"], reason="User requested")

# User tasks
client.complete_user_task(task_id, completed_by="jane@example.com", result={"approved": True})
```

---

## Kafka Dispatch (Opt-In)

The Python SDK supports push-based job dispatch via Kafka, providing a more scalable alternative to polling.

### Usage

```python
from workflow_sdk.runner.kafka_runner import KafkaRunner

# Setup client and registry as before
runner = KafkaRunner(
    worker_id=worker_id,
    brokers="localhost:9092",
    client=client,
    registry=registry
)

runner.run()
```

### At-least-once delivery and idempotent handlers

`KafkaRunner` delivers jobs with **at-least-once** semantics. An in-process dedup window (default 10 min) absorbs most duplicates transparently — but a handler **can** be invoked more than once for the same `job_id` when:

- The relay was down longer than `dedup_window_seconds` before republishing.
- This runner restarted between the original message and a relay-republished duplicate.

**Handlers must be idempotent.** Use a stable business operation key from variables (for example, an invoice ID) for external side effects. `jobId` identifies one delivery attempt and changes on automatic retry or lease recovery; it only deduplicates redelivery of that same attempt.

```python
def send_invoice(job):
    invoice_id = job["variables"]["invoiceId"]
    if db.invoice_already_sent(invoice_id):
        return {}
    return send_invoice_to_customer(job["variables"], idempotency_key=invoice_id)

registry.register("send-invoice", send_invoice)
```

Enforce the operation key with a database unique constraint or pass it to the external service as its idempotency key. A check-before-send alone does not prevent concurrent duplicates.

The engine's `complete_job` / `fail_job` calls are already idempotent — a second call with the same `job_id` is a no-op. Only your external side-effects need to be guarded.

---

### TLS / SASL (production)

The basic config above connects to brokers in **plaintext with no authentication**. For production, pass standard librdkafka properties through `extra_kafka_config` — anything set there is merged into the consumer config (and overrides defaults). Full key reference: [librdkafka CONFIGURATION.md](https://github.com/confluentinc/librdkafka/blob/master/CONFIGURATION.md).

```python
import os
from workflow_sdk.runner.kafka_runner import KafkaRunner

# SASL/SCRAM-SHA-512 over TLS — common for MSK, Confluent Cloud, Aiven, Redpanda
runner = KafkaRunner(
    worker_id="py-worker-1",
    brokers="kafka-1.prod:9093,kafka-2.prod:9093",
    client=client,
    registry=registry,
    extra_kafka_config={
        "security.protocol": "SASL_SSL",
        "sasl.mechanism":    "SCRAM-SHA-512",
        "sasl.username":     os.environ["KAFKA_SASL_USERNAME"],
        "sasl.password":     os.environ["KAFKA_SASL_PASSWORD"],
        # Optional — only when the broker uses a private CA
        # "ssl.ca.location": "/etc/kafka/ca.pem",
    },
)
runner.run()
```

Other mechanisms:

```python
# SASL/PLAIN over TLS
extra_kafka_config = {
    "security.protocol": "SASL_SSL",
    "sasl.mechanism":    "PLAIN",
    "sasl.username":     "...",
    "sasl.password":     "...",
}

# SASL/OAUTHBEARER (AWS MSK IAM, Azure Event Hubs, custom IdP)
# Use confluent-kafka's OAuthBearer token refresh callback.
extra_kafka_config = {
    "security.protocol":     "SASL_SSL",
    "sasl.mechanism":        "OAUTHBEARER",
    "oauth_cb":              lambda cfg: (fetch_access_token(), time.time() + 900),
}

# Mutual TLS only (no SASL)
extra_kafka_config = {
    "security.protocol":      "SSL",
    "ssl.certificate.location": "/etc/kafka/client.crt",
    "ssl.key.location":         "/etc/kafka/client.key",
    "ssl.ca.location":          "/etc/kafka/ca.pem",
}
```

Plaintext brokers should be restricted to local dev (`docker compose`) — never deploy without `SASL_SSL` (or `SSL` with mutual TLS) on a shared network.

---

### KafkaRunner constructor reference

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `worker_id` | str | *(required)* | Unique identifier for this worker. |
| `brokers` | str | *(required)* | Comma-separated list of Kafka brokers. |
| `client` | `EngineClient` | *(required)* | REST client for completion callbacks. |
| `registry` | `HandlerRegistry` | *(required)* | Maps job types to handlers. |
| `dedup_window_seconds` | float | `600.0` | Window (seconds) for in-memory deduplication (default 10m). |
| `extra_kafka_config` | dict | `None` | Overrides merged into the Kafka Consumer config — set `security.protocol`, `sasl.*`, `ssl.*` here for production. |

---

## Runner constructor reference (Polling Mode)

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `client` | `EngineClient` | *(required)* | REST client. |
| `registry` | `HandlerRegistry` | *(required)* | Maps job types to handlers. |
| `worker_id` | str | *(required)* | Unique identifier for this worker process. |
| `parallelism` | int | `64` | Maximum claimed jobs, including running handlers and pending callbacks. |
| `poll_interval` | float | `0.5` | Seconds between poll rounds when the queue is empty. |

## Polling callback recovery (R06)

Polls request only free capacity after pruning completed futures. A claimed job holds its slot through handler execution and callback acknowledgement; the executor does not queue additional leased jobs. The handler result or failure reason/retryability is snapshotted and retried independently of the handler. Completion transport errors never become business failures.

REST and gRPC preserve `lockExpiresAt`. Retry backoff starts at 100 ms with ±20% jitter and a 30-second cap, bounded by the existing lease and shutdown event. Built-in transports use at most 5-second I/O timeouts (or the remaining lease, if shorter); HTTP timeouts apply per I/O phase. There is no heartbeat. Expired/invalid lease metadata prevents dispatch; absent metadata from a legacy custom client permits one callback attempt. Client method signatures are unchanged; custom implementations must provide bounded I/O themselves.

Shutdown stops claiming/retrying and bounds drain by `shutdown_timeout`; it raises if threads remain. Python cannot forcibly cancel arbitrary handler side effects: a handler still running after expiry retains its slot until it returns, then sends no stale callback. Keep external side effects idempotent and normally finish within the baseline 30-second lease.

Monitor `workflow_sdk_callback_pending`, `workflow_sdk_callback_retries_total`, `workflow_sdk_callback_oldest_pending_seconds`, and `workflow_sdk_callback_rejected_total`, using finite `dispatch` and `callback` labels. Job IDs are logged. Rising pending age calls for checking transport/engine health and authorization; permanent HTTP 4xx (except 408/429) and permanent gRPC statuses stop callback delivery and leave recovery to the lease. Canary a drained worker group and inspect actual workflow outcomes. Rollback pins the previous build and may restore polling callback/claim defects.

R06/R07 address polling and Kafka callback recovery. Heartbeat for long handlers remains a later release.

Package verification: `python -m pytest`, then `python scripts/verify-package.py`. It builds wheel and sdist, checks fresh external consumers, and rejects a deliberately broken distribution. E2E installs the checksummed wheel; publishing downloads those same verified bytes and never rebuilds.

## Kafka delivery and recovery (R07)

Auto-commit and auto-offset-store are disabled, including when caller overrides request them. Each partition pauses with one outstanding record; a consumer thread continues polling for membership while a separate handler thread retains the result and retries its callback. Offsets commit synchronously and explicitly as record offset + 1 only after acknowledgement. A failure at N cannot be hidden by a later commit at N+1. Dedup records are written after acknowledgement; pending delivery does not expire with dedup TTL.

Revocation/loss cancels the old owner's callback; completion from an old future cannot commit. Shutdown stops claims and callbacks, leaves the group, and drains handler threads. Arbitrary Python handler side effects cannot be forcibly cancelled. Lost acknowledgement/crash can cause redelivery, so use stable business idempotency keys.

Malformed protobuf/empty job IDs and permanent callback rejection stop the runner with the record uncommitted. Valid job IDs with invalid payloads or missing handlers produce a non-retryable failure callback, which must itself be acknowledged. `KafkaRunner.run()` propagates consumer errors to the caller; restart after fixing the payload, credentials or infrastructure. Do not reset offsets to bypass pending work. Extra Kafka configuration remains available for TLS/SASL, but auto-offset/commit policy and per-job consumer group are reserved.

`KafkaRunner(..., metrics=Metrics(registry=...))` exposes the same callback pending/retry/oldest/rejected metrics with `dispatch="kafka"`, plus `workflow_sdk_kafka_record_errors_total`. Logs include job/dedup/topic/partition/offset. Canary a drained group and compare pending age, lag and actual workflow outcomes. Rollback pins the previous wheel/worker and may restore A1. No engine or DB changes are required. Native poll, synchronous commit and assignment handling follow the [Confluent Python API](https://docs.confluent.io/platform/current/clients/confluent-kafka-python/html/index.html).

## Optional polling heartbeat (1.1.0)

Heartbeat is off by default. Enable only after every engine replica supports
`ExtendJobLease` (engine candidate v1.2.0 / R15):

```python
runner = Runner(client, registry, "worker-1", parallelism=16,
                heartbeat=True, lease_seconds=30, heartbeat_interval=10,
                shutdown_timeout=5)
```

The first renewal must be accepted before the handler starts. Renewal continues
while the handler executes and while its immutable completion/failure callback
awaits acknowledgement. Each renewal has a transport timeout of at most 5 seconds,
limited by the current lease; transient failures back off within that lease.
A terminal/expired/wrong-worker attempt raises `LeaseLostError`; the runner sends
no failure callback for ownership loss. Unsupported engines/clients raise
`HeartbeatUnsupportedError` through `Runner.run()` and stop intake.

`EngineClient` keeps its original method signatures. `LeaseExtender` is a separate
optional protocol. Built-in clients additionally accept `poll_jobs(...,
lease_seconds=60)`; 0 means the 30-second engine default, otherwise 5–3600 seconds.
The runner communicates this optional setting through `request_lease()` in
`workflow_sdk.client.deadline`, leaving legacy custom clients unchanged. Custom
clients implementing heartbeat should honor `current_timeout()` and, if they
support custom polling leases, `current_lease_seconds()`.

Handlers may observe `ctx["cancel_event"]`, a `threading.Event`, to stop external
work on lease loss/shutdown. Cancellation is a request, not proof that a thread has
stopped: the runner keeps its capacity slot until the handler returns. `run()`
stops intake and waits at most `shutdown_timeout` after a bounded poll returns;
it raises if handlers remain and rejects another run on the same runner until
they finish. Python cannot force-kill those threads, and they can keep the process
alive. A process supervisor must enforce the final process shutdown deadline.

Renewal uses a separate thread outside the handler executor. Pure Python CPU work
allows thread switching (covered by a regression test). Native code holding the
GIL, a suspended process, or OS starvation can still prevent renewal; isolate that
work in another process or use a verified longer lease. Side effects require
idempotency even with heartbeat.

Monitor `workflow_sdk_heartbeat_total{result="accepted|retry|lease_lost|rejected"}`,
callback pending/retry metrics and replacement attempts. Canary with heartbeat
off, then enable on a small worker group while measuring DB writes and latency.
To roll back, stop intake for tasks dependent on renewal, drain them, then disable
heartbeat and pin the prior SDK. Disable/drain heartbeat workers before rolling
back the engine. Generate checked-in stubs with Python 3.12 and
`pip install grpcio-tools==1.60.0 setuptools==80.9.0`, then run
`python scripts/generate-proto.py` from the SDK directory.
