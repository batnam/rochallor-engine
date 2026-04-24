# Python SDK

**Package**: `workflow-sdk` (`pip install -e ".[dev]"`)

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
        # Blocks until SIGINT/SIGTERM; drains in-flight jobs before returning
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

### KafkaRunner constructor reference

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `worker_id` | str | *(required)* | Unique identifier for this worker. |
| `brokers` | str | *(required)* | Comma-separated list of Kafka brokers. |
| `client` | `EngineClient` | *(required)* | REST client for completion callbacks. |
| `registry` | `HandlerRegistry` | *(required)* | Maps job types to handlers. |
| `dedup_window_seconds` | float | `600.0` | Window (seconds) for in-memory deduplication (default 10m). |
| `extra_kafka_config` | dict | `None` | Optional overrides for the Kafka Consumer (passed to `confluent_kafka`). |

---

## Runner constructor reference (Polling Mode)

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `client` | `EngineClient` | *(required)* | REST client. |
| `registry` | `HandlerRegistry` | *(required)* | Maps job types to handlers. |
| `worker_id` | str | *(required)* | Unique identifier for this worker process. |
| `parallelism` | int | `64` | `ThreadPoolExecutor` max workers — concurrent jobs. |
| `poll_interval` | float | `0.5` | Seconds between poll rounds when the queue is empty. |
