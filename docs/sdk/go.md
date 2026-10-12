# Go SDK

**Module**: `github.com/batnam/rochallor-engine/workflow-sdk-go`

> Looking for a step-by-step walkthrough that builds a real worker against a
> chained workflow? See the
> [Loan Origination Worker tutorial](./go-worker-tutorial.md), which annotates
> the example in [`docs/example/go-rochallor-worker`](https://github.com/batnam/rochallor-engine/tree/main/docs/example/go-rochallor-worker).

## Key types

| Package | Type / Function | Purpose |
|---------|----------------|---------|
| `client` | `NewRest(baseURL, workerID)` | REST client (default) |
| `client` | `NewGrpc(target, workerID, ...opts)` | gRPC client |
| `client` | `EngineClient` interface | Transport abstraction |
| `handler` | `New()` | Create a handler registry |
| `handler` | `Registry.Register(jobType, Handler)` | Register a handler |
| `handler` | `JobContext` | Passed to every handler — carries `JobID`, `InstanceID`, `JobType`, `Variables`, `RetriesRemaining` |
| `handler` | `Result{VariablesToSet}` | Return value — variables merged into the instance on completion |
| `retry` | `NonRetryable{Cause}` | Wrap a handler error to skip the retry budget |
| `runner` | `New(Config, engine, registry)` | Create a runner |
| `runner` | `Config{WorkerID, Parallelism, PollInterval}` | Runner settings (defaults: 64 goroutines, 500 ms poll) |
| `runner` | `Runner.Run(ctx)` | Start the loop; blocks until `ctx` is cancelled |

---

## How the runner works

`handler.New()` + `registry.Register(...)` just build a `jobType → function` map in memory — no connection, no I/O. The `Runner` is what drives everything:

1. A ticker fires every `PollInterval` (default 500 ms) and calls `POST /v1/jobs/poll`.
2. The engine claims available jobs atomically with `FOR UPDATE SKIP LOCKED` and returns them.
3. Each job is dispatched to a goroutine (bounded by `Parallelism`, default 64).
4. The goroutine calls your registered handler, then calls `CompleteJob` or `FailJob` based on the result.

**Error handling**: return a plain `error` → `FailJob(retryable=true)` → engine retries up to `retryCount`. Wrap with `&retry.NonRetryable{Cause: err}` → `FailJob(retryable=false)` → fails immediately regardless of retry budget.

For the full model (sequence diagram, retry flow, graceful shutdown), see [architecture.md — Worker polling model](../architecture.md#worker-polling-model).

---

## Minimal example — REST transport

```go
package main

import (
    "context"
    "os/signal"
    "syscall"

    "github.com/batnam/rochallor-engine/workflow-sdk-go/client"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/runner"
)

func main() {
    engine := client.NewRest("http://localhost:8080", "go-worker-1")

    registry := handler.New()
    registry.Register("process-order", func(ctx context.Context, job handler.JobContext) (handler.Result, error) {
        // Read input variables
        orderID, _ := job.Variables["orderId"].(string)
        _ = orderID // call your business logic here

        // Return variables to merge into the workflow instance
        return handler.Result{VariablesToSet: map[string]any{"processed": true}}, nil
    })

    r := runner.New(runner.Config{WorkerID: "go-worker-1"}, engine, registry)

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    r.Run(ctx) // blocks until SIGINT / SIGTERM
}
```

---

## Full demo — multiple handlers, non-retryable errors, gRPC transport

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "os/signal"
    "syscall"
    "time"

    "github.com/batnam/rochallor-engine/workflow-sdk-go/client"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/retry"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/runner"
)

func main() {
    // Use gRPC transport — swap for client.NewRest to use REST
    engine, err := client.NewGrpc("localhost:9090", "go-worker-1")
    if err != nil {
        slog.Error("dial failed", "err", err)
        return
    }
    defer engine.Close()

    registry := handler.New()

    // Handler: validate-application
    // Returns a non-retryable error when the input is permanently invalid.
    registry.Register("validate-application", func(ctx context.Context, job handler.JobContext) (handler.Result, error) {
        applicantID, ok := job.Variables["applicantId"].(string)
        if !ok || applicantID == "" {
            // NonRetryable — engine will not retry regardless of retryCount
            return handler.Result{}, &retry.NonRetryable{
                Cause: errors.New("applicantId is required and must be a string"),
            }
        }
        slog.Info("validating application", "applicantId", applicantID, "attempt", job.Attempt)
        // ... call validation service ...
        return handler.Result{VariablesToSet: map[string]any{
            "validationPassed": true,
            "validatedAt":      time.Now().UTC().Format(time.RFC3339),
        }}, nil
    })

    // Handler: credit-score
    // Returns a retryable error on transient failures (network, timeout).
    registry.Register("credit-score", func(ctx context.Context, job handler.JobContext) (handler.Result, error) {
        applicantID := job.Variables["applicantId"].(string)
        score, err := fetchCreditScore(ctx, applicantID) // hypothetical call
        if err != nil {
            // Returning a plain error is retryable — runner calls FailJob(retryable=true)
            return handler.Result{}, fmt.Errorf("credit bureau unavailable: %w", err)
        }
        return handler.Result{VariablesToSet: map[string]any{"creditScore": score}}, nil
    })

    // Handler: send-notification
    registry.Register("send-notification", func(ctx context.Context, job handler.JobContext) (handler.Result, error) {
        email, _ := job.Variables["email"].(string)
        slog.Info("sending notification", "email", email, "jobId", job.JobID)
        // ... send email ...
        return handler.Result{VariablesToSet: map[string]any{"notificationSent": true}}, nil
    })

    r := runner.New(runner.Config{
        WorkerID:     "go-worker-1",
        Parallelism:  32,               // concurrent goroutines
        PollInterval: 250 * time.Millisecond,
    }, engine, registry)

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    slog.Info("worker starting")
    r.Run(ctx)
    slog.Info("worker stopped")
}

func fetchCreditScore(_ context.Context, _ string) (int, error) {
    return 720, nil // placeholder
}
```

---

## Kafka Dispatch (Opt-In)

The Go SDK supports an alternative, push-based dispatch mode via Kafka. This mode provides higher throughput and lower latency than polling.

### Usage

```go
import (
    "github.com/batnam/rochallor-engine/workflow-sdk-go/kafkarunner"
)

func main() {
    // ... setup engine client and registry as before ...

    r, err := kafkarunner.New(kafkarunner.Config{
        WorkerID:    "go-worker-1",
        SeedBrokers: "localhost:9092",
        JobTypes:    []string{"process-order"},
    }, engine, registry)
    if err != nil { panic(err) }
    if err := r.Run(ctx); err != nil { panic(err) }
}
```

### At-least-once delivery and idempotent handlers

`kafkarunner` delivers jobs with **at-least-once** semantics. An in-process dedup window (default 10 min) absorbs most duplicates transparently — but a handler **can** be invoked more than once for the same `JobID` when:

- The relay was down longer than `DedupWindow` before republishing.
- This runner restarted between the original message and a relay-republished duplicate.

**Handlers must be idempotent.** Use a stable business operation key from variables (for example, an invoice ID) for external side effects. `jobId` identifies one delivery attempt and changes on automatic retry or lease recovery; it only deduplicates redelivery of that same attempt.

```go
registry.Register("send-invoice", func(ctx context.Context, job handler.JobContext) (handler.Result, error) {
    invoiceID := job.Variables["invoiceId"].(string) // validated business input
    if sent, _ := db.InvoiceAlreadySent(ctx, invoiceID); sent {
        return handler.Result{}, nil
    }
    return handler.Result{}, sendInvoice(ctx, job.Variables, invoiceID)
})
```

Enforce the operation key with a database unique constraint or pass it to the external service as its idempotency key. A check-before-send alone does not prevent concurrent duplicates.

The engine's `CompleteJob` / `FailJob` calls are already idempotent — a second call with the same `JobID` is a no-op. Only your external side-effects need to be guarded.

---

### Kafka Configuration Reference

When using `kafkarunner`, the following configuration fields are available:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `WorkerID` | string | *(required)* | Unique identifier for this worker. |
| `SeedBrokers` | string | *(required)* | Comma-separated list of brokers (e.g. `localhost:9092`). |
| `JobTypes` | `[]string` | *(required)* | List of job types to handle. |
| `DedupWindow` | `time.Duration` | `10m` | Window for confirmed callbacks; pending outcomes never expire with this cache. |
| `KgoOpts` | `[]kgo.Opt` | none | TLS/SASL and tuning. The runner owns auto-commit and assignment/revocation callbacks; these cannot be overridden. |

---


### Callback recovery and operations

The Kafka runner retains the handler result and retries the same completion or failure callback. Each REST/gRPC attempt has a 5-second context deadline. Backoff starts at 100 ms, doubles with ±20% jitter and is capped at 30 seconds. Retry continues while the partition assignment and runner context remain valid. Handlers and custom `EngineClient` implementations must observe their context cancellation.

A record is added to the dedup cache only after callback confirmation. Offsets are committed synchronously per processed record, with a 5-second deadline. Pending records block later offsets from the same partition. Kafka heartbeats and rebalance callbacks continue while handlers/retries run; revocation cancels the old assignment, and that assignment cannot commit after ownership is released. A handler can execute again after process death or partition reassignment; use a stable business idempotency key.

Malformed protobuf or a missing job ID stops `Run` with an error and leaves the offset uncommitted. A valid job with invalid JSON or a missing handler sends a non-retryable failure, which must be confirmed before commit. Permanent HTTP/gRPC callback rejection also stops the runner. HTTP 408/429 and server/transport errors are retried; other 4xx responses require operator action.

Metrics registered with the default Prometheus registry:

| Metric | Meaning |
|---|---|
| `workflow_sdk_callback_pending` | Unconfirmed outcomes |
| `workflow_sdk_callback_retries_total` | Retries of the existing outcome |
| `workflow_sdk_callback_oldest_pending_seconds` | Oldest pending age, zero when idle |
| `workflow_sdk_callback_rejected_total` | Permanent callback rejections |
| `workflow_sdk_kafka_record_errors_total` | Records that cannot be associated with a valid job |

Callback labels are `dispatch` (`polling`/`kafka`) and `callback` (`complete`/`fail`). Logs carry job/dedup IDs and topic/partition/offset. Track pending age and consumer lag together: rising age with increasing lag means a partition is waiting for callback recovery.

For a blocked partition, inspect the logged record and engine callback status. Restore engine connectivity or correct credentials/configuration, then restart the worker if `Run` returned. For malformed records, stop the affected worker, preserve the record and offset for investigation, and have the workflow owner approve a remediation/replay procedure. Do not silently skip records or reset offsets to clear lag. Cancel the runner context before shutdown and allow handlers to drain; a handler ignoring cancellation can delay shutdown and may still perform side effects after lease/assignment loss.

Roll out one worker group first and verify persisted workflow outcomes. To roll back, drain/cancel and use the previously verified worker image; leave unconfirmed offsets for redelivery. The previous SDK may reintroduce callback loss, so rollback requires monitoring and reconciliation.

## Runner configuration reference (Polling Mode)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `WorkerID` | string | *(required)* | Unique identifier for this worker process. |
| `Parallelism` | int | `64` | Maximum concurrent in-flight job goroutines. |
| `PollInterval` | `time.Duration` | `500ms` | Interval between poll rounds when the queue is empty. |

## Polling delivery and capacity (R05)

`Parallelism` bounds all claimed work: running handlers plus unacknowledged callbacks. Poll requests use only free slots; jobs do not wait in a local queue after their lease starts. REST and gRPC preserve `Job.LockExpiresAt`. Handlers receive a context bounded by shutdown and that lease. A handler ignoring cancellation keeps its slot until it returns.

The runner snapshots the result or failure and retries that callback with 5-second attempt contexts and exponential backoff with jitter, bounded by the lease. A completion transport error never calls FailJob or spends a business retry. Expiry/shutdown stops delivery and lets the engine recover a new attempt. Permanent HTTP/gRPC rejection is logged and counted without acknowledging work. Custom clients keep the existing interface and must honor context cancellation; jobs without lease metadata receive one bounded callback attempt.

Use the callback metrics above with `dispatch="polling"`; watch pending age, lease recoveries and handler duration against the 30-second baseline lease. If pending age rises, check transport health/authorization before increasing concurrency. Drain and canary a worker group, then verify workflow outcomes. Rollback pins the prior worker build; it may restore A6/A7. Fault E2E now covers polling callback loss, full capacity, lease replacement/stale callbacks and shutdown on both transports. No heartbeat or engine/schema change is required.

### Optional polling heartbeat (v1.2.0 candidate)

Heartbeat is off by default. Upgrade every engine replica to the version that
supports `ExtendJobLease` / `/v1/jobs/{id}/heartbeat` before enabling it.

```go
r := runner.New(runner.Config{
    WorkerID: "payments-1", Parallelism: 8,
    LeaseSeconds: 30, Heartbeat: true,
    HeartbeatInterval: 10 * time.Second,
    ShutdownTimeout: 5 * time.Second,
}, engineClient, registry)
if err := r.RunWithError(ctx); err != nil { log.Print(err) }
```

Lease duration zero uses the engine default 30 seconds; otherwise 5–3600.
Heartbeat interval defaults to one third of the requested lease and cannot
exceed that bound. Each request has at most five seconds and cannot outlive the
current known lease. A separate goroutine watches expiry while the heartbeat
request, handler or immutable callback retry is pending. Heartbeat starts before
the handler and continues until callback acknowledgment or lease loss.

`EngineClient` is unchanged. REST/gRPC clients additionally implement the optional
`client.LeaseExtender`; custom clients only need it when opting in. Unsupported
clients/engines return `client.ErrHeartbeatUnsupported`; no long handler starts
under a false assumption of protection. `RunWithError` exposes that failure;
existing `Run` callers get an explicit error log. `client.ErrLeaseLost` means the
attempt must stop. REST 404/405/501 and gRPC UNIMPLEMENTED indicate missing
capability, while HTTP 409 / gRPC FAILED_PRECONDITION indicate lease loss.

Handlers should observe their context. Lost ownership cancels it and suppresses
old callbacks, but an uncooperative handler still occupies its slot until it
returns. Shutdown cancels active work and bounds drain by `ShutdownTimeout`;
external effects may outlive that timeout. The same runner rejects another Run
while such a handler is still draining. Custom transports must honor context
cancellation. Go goroutines allow CPU work and heartbeat to run independently;
this does not make external side effects exactly-once.

Monitor `workflow_sdk_heartbeat_total{result}` (`accepted`, `retry`, `lease_lost`,
`rejected`), callback pending age and engine heartbeat DB latency/write volume.
On repeated lease loss, check DB/network latency, handler cancellation and mixed
engine versions. Disable heartbeat and drain long jobs before rolling back the
engine/SDK; an old engine ignores custom lease duration and uses 30 seconds.
