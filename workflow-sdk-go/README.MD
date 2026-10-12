# Go SDK

Module: `github.com/batnam/rochallor-engine/workflow-sdk-go`. See the [Go SDK guide](../docs/sdk/go.md) for polling, REST/gRPC, handler and configuration examples.

```sh
go get github.com/batnam/rochallor-engine/workflow-sdk-go
```

## Kafka worker

```go
package main

import (
    "context"
    "log"
    "os/signal"
    "syscall"

    "github.com/batnam/rochallor-engine/workflow-sdk-go/client"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/kafkarunner"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    engine := client.NewRest("http://localhost:8080", "go-worker-1")
    registry := handler.New()
    registry.Register("process-order", func(ctx context.Context, job handler.JobContext) (handler.Result, error) {
        return handler.Result{VariablesToSet: map[string]any{"processed": true}}, nil
    })
    worker, err := kafkarunner.New(kafkarunner.Config{
        WorkerID: "go-worker-1", SeedBrokers: "localhost:9092", JobTypes: registry.JobTypes(),
    }, engine, registry)
    if err != nil { log.Fatal(err) }
    if err := worker.Run(ctx); err != nil { log.Fatal(err) }
}
```

Use `client.NewGrpc(target, workerID)` to select gRPC; check its error and close the client on exit. For polling use `runner.New(config, engine, registry).Run(ctx)`. The engine must use the matching dispatch mode.


## Callback recovery and operations

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


## Verification

```sh
go test -race ./...
go vet ./...
sh scripts/check-consumer.sh
```

The external module consumer compiles both clients and runners against the candidate source. PR and tag workflows additionally run REST/gRPC × polling/Kafka E2E. Set `E2E_SDK_FAULTS=1` with the Kafka E2E stack to exercise callback response loss, pending offsets, shutdown, worker death, rebalance and malformed records. The Go module tag is created only after those gates pass on the source to be tagged.

R02/R05 address Kafka delivery and polling callback/admission behavior. Long handlers still require idempotency; optional heartbeat requires the engine capability and configuration described below.

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
