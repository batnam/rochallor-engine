# Go SDK

**Module**: `github.com/batnam/rochallor-engine/workflow-sdk-go`

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

    r := kafkarunner.New(kafkarunner.Config{
        WorkerID:    "go-worker-1",
        SeedBrokers: "localhost:9092",
        JobTypes:    []string{"process-order"},
    }, engine, registry)

    r.Run(ctx)
}
```

### Kafka Configuration Reference

When using `kafkarunner`, the following configuration fields are available:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `WorkerID` | string | *(required)* | Unique identifier for this worker. |
| `SeedBrokers` | string | *(required)* | Comma-separated list of brokers (e.g. `localhost:9092`). |
| `JobTypes` | `[]string` | *(required)* | List of job types to handle. |
| `DedupWindow` | `time.Duration` | `10m` | Window for in-memory deduplication of Kafka messages. |

---

## Runner configuration reference (Polling Mode)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `WorkerID` | string | *(required)* | Unique identifier for this worker process. |
| `Parallelism` | int | `64` | Maximum concurrent in-flight job goroutines. |
| `PollInterval` | `time.Duration` | `500ms` | Interval between poll rounds when the queue is empty. |
