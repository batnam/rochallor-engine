# workflow-sdk-python

Python SDK for the [Rochallor Workflow engine](../README.md). Provides a polling worker, REST client, and Prometheus metrics — all with zero framework dependencies.

**Python 3.10+ required.**

---

## Installation

```bash
# From the repository root:
pip install -e "workflow-sdk-python"

# With dev dependencies (pytest, mypy, etc.):
pip install -e "workflow-sdk-python[dev]"
```

---

## Quick Start: Worker

```python
import signal
import threading

from workflow_sdk.client.rest import RestEngineClient
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.runner.runner import Runner

# 1. Create the REST client pointing at your engine
client = RestEngineClient("http://localhost:8080")

# 2. Register handlers by job type
registry = HandlerRegistry()

@registry.register("process-order")
def handle_process_order(ctx: dict) -> dict:
    order_id = ctx["variables"].get("orderId")
    # ... do work ...
    return {"status": "processed", "orderId": order_id}

@registry.register("send-notification")
def handle_notification(ctx: dict) -> dict:
    # Raise NonRetryableError for permanent failures
    from workflow_sdk.errors import NonRetryableError
    if not ctx["variables"].get("email"):
        raise NonRetryableError("email address is required")
    return {}

# 3. Start the runner (blocks until stop_event is set)
stop = threading.Event()
signal.signal(signal.SIGINT, lambda *_: stop.set())
signal.signal(signal.SIGTERM, lambda *_: stop.set())

runner = Runner(
    client=client,
    registry=registry,
    worker_id="py-worker-1",
    parallelism=16,        # concurrent job handlers (default 64)
    poll_interval=0.5,     # seconds between polls (default 0.5)
)
runner.run(stop_event=stop)
```

---

## API Reference

### `RestEngineClient`

```python
from workflow_sdk.client.rest import RestEngineClient

client = RestEngineClient(base_url, timeout=30.0)
```

| Method | Description |
|--------|-------------|
| `upload_definition(definition)` | Upload a workflow definition JSON; returns definition summary |
| `get_definition(definition_id)` | Fetch a definition by ID |
| `list_definitions(keyword="", page=0, page_size=20)` | Paginated list of definitions |
| `start_instance(definition_id, variables=None, ...)` | Start a workflow instance; returns instance summary |
| `get_instance(instance_id)` | Fetch instance state |
| `get_instance_history(instance_id)` | List step executions for an instance |
| `cancel_instance(instance_id, reason="")` | Cancel a running instance |
| `poll_jobs(worker_id, job_types, max_jobs=1)` | Claim jobs (used by `Runner` automatically) |
| `complete_job(job_id, worker_id, variables=None)` | Mark job complete with output variables |
| `fail_job(job_id, worker_id, error_message, retryable=True)` | Record job failure |
| `complete_user_task(task_id, completed_by="", result=None)` | Complete a user task |
| `close()` | Release the underlying HTTP connection pool |

`RestEngineClient` is a context manager:

```python
with RestEngineClient("http://localhost:8080") as client:
    instance = client.start_instance("my-workflow")
```

### `HandlerRegistry`

```python
from workflow_sdk.handler.registry import HandlerRegistry

registry = HandlerRegistry()

# Register via decorator
@registry.register("my-job-type")
def handler(ctx: dict) -> dict:
    return {"result": "ok"}

# Or register directly
registry.register("other-type", lambda ctx: {"x": 1})

# Inspect registered types
registry.job_types()  # -> ["my-job-type", "other-type"]
```

**Handler signature**: `(ctx: dict) -> dict | None`

The `ctx` dict contains:

| Key | Type | Description |
|-----|------|-------------|
| `id` | `str` | Job ID |
| `jobType` | `str` | Handler key |
| `instanceId` | `str` | Workflow instance ID |
| `stepId` | `str` | Step ID in the definition |
| `stepExecutionId` | `str` | Unique execution ID for this attempt |
| `retriesRemaining` | `int` | Retries left before permanent failure |
| `variables` | `dict` | Input variables from the workflow |
| `lockExpiresAt` | `str` | ISO-8601 timestamp when the job lock expires |

### `Runner`

```python
from workflow_sdk.runner.runner import Runner

runner = Runner(
    client=client,           # RestEngineClient (or any EngineClient implementor)
    registry=registry,       # HandlerRegistry with at least one handler
    worker_id="my-worker",   # Unique worker ID (shown in engine logs)
    parallelism=64,          # Max concurrent handler threads (default 64)
    poll_interval=0.5,       # Poll interval in seconds (default 0.5)
    metrics=None,            # Optional Metrics instance for Prometheus
)

stop = threading.Event()
runner.run(stop_event=stop)  # Blocks until stop_event is set
```

The runner drains all in-flight jobs before returning after `stop_event` is set.

### Errors

```python
from workflow_sdk.errors import NonRetryableError, EngineClientError, WorkflowSDKError
```

| Exception | When to use |
|-----------|-------------|
| `NonRetryableError` | Raise inside a handler to mark the job failed permanently (no retry) |
| `EngineClientError` | Raised by `RestEngineClient` on HTTP 4xx/5xx responses; has `.status_code` and `.message` |
| `WorkflowSDKError` | Base class; raised on connection errors |

Any other exception raised by a handler causes the job to fail with `retryable=True`.

### Metrics

```python
from prometheus_client import CollectorRegistry
from workflow_sdk.metrics.metrics import Metrics

# Use an isolated registry (recommended in tests / multi-worker setups)
reg = CollectorRegistry()
m = Metrics(registry=reg)

runner = Runner(client=client, registry=registry, worker_id="w1", metrics=m)
```

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `workflow_sdk_poll_latency_seconds` | Histogram | — | Time spent in each `poll_jobs` call |
| `workflow_sdk_lock_conflicts_total` | Counter | — | Empty poll rounds (no jobs claimed) |
| `workflow_sdk_handler_latency_seconds` | Histogram | `job_type` | Handler execution time |
| `workflow_sdk_retries_total` | Counter | `job_type` | Jobs retried after transient failure |
| `workflow_sdk_jobs_completed_total` | Counter | `job_type`, `outcome` | Completed jobs; outcome is `success` or `failure` |

Expose metrics via `prometheus_client.start_http_server(port)` in your worker process.

---

## Management Operations Example

The following script uploads a definition, starts an instance, polls until it completes, then prints the final variables:

```python
import time
from workflow_sdk.client.rest import RestEngineClient

client = RestEngineClient("http://localhost:8080")

# 1. Upload a simple one-step workflow
definition = {
    "id": "echo-workflow",
    "name": "Echo Workflow",
    "steps": [
        {
            "id": "echo",
            "type": "SERVICE_TASK",
            "jobType": "echo",
            "next": "end"
        },
        {"id": "end", "type": "END"}
    ]
}
uploaded = client.upload_definition(definition)
print(f"Definition: {uploaded['id']} v{uploaded['version']}")

# 2. Start an instance
instance = client.start_instance("echo-workflow", variables={"message": "hello"})
instance_id = instance["id"]
print(f"Instance started: {instance_id}")

# 3. Poll instance status until completed or failed
for _ in range(30):
    state = client.get_instance(instance_id)
    status = state["status"]
    print(f"  Status: {status}")
    if status in ("COMPLETED", "FAILED", "CANCELLED"):
        break
    time.sleep(1)

# 4. Print execution history
history = client.get_instance_history(instance_id)
for step in history:
    print(f"  Step {step['stepId']}: {step['status']}")

# 5. List all definitions
page = client.list_definitions()
print(f"Total definitions: {page['total']}")
```

---

## Running Tests

```bash
cd workflow-sdk-python
pytest tests/ -v
```

Expected: **52 tests pass** in < 1 second. No running engine required — all HTTP interactions are mocked via `pytest-httpx`.

---

## Type Checking

```bash
mypy src/
```

The package ships a `py.typed` marker (PEP 561). All public APIs have complete type annotations.

---

## Backoff Configuration

The SDK uses exponential backoff when retrying failed jobs (constants in `src/workflow_sdk/retry/backoff.py`):

| Constant | Value | Description |
|----------|-------|-------------|
| `BASE_DELAY` | 0.1 s | Initial delay before first retry |
| `FACTOR` | 2.0 | Exponential growth factor |
| `JITTER_FRAC` | 0.20 | ±20% random jitter per step |
| `MAX_DELAY` | 30.0 s | Maximum delay cap |
