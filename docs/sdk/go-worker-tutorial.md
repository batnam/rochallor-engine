# Tutorial — Build a Loan Origination Worker (Go SDK)

A worked walkthrough of the worker under
[`docs/example/go-rochallor-worker`](https://github.com/batnam/rochallor-engine/tree/main/docs/example/go-rochallor-worker).
The worker services two chained definitions shipped alongside the source:

| Workflow | File | Purpose |
|----------|------|---------|
| `LOS::loan-application-full` | [`workflow-loan-application.json`](https://github.com/batnam/rochallor-engine/blob/main/docs/example/go-rochallor-worker/workflowDefinition/workflow-loan-application.json) | Validate → parallel risk checks → classify → route → approve / reject / escalate |
| `LOS::loan-disbursement-workflow` | [`workflow-loan-disbursement.json`](https://github.com/batnam/rochallor-engine/blob/main/docs/example/go-rochallor-worker/workflowDefinition/workflow-loan-disbursement.json) | Compute fees → senior approval (if needed) → transfer → notify |

The first definition sets `"autoStartNextWorkflow": true` and `"nextWorkflowId": "LOS::loan-disbursement-workflow"`, so a single worker process handles the entire end-to-end origination flow.

If you only want the API reference, jump to [Go SDK](./go.md). This page focuses on **why** each piece looks the way it does, using the LOS example as the running illustration.

---

## 1. What the worker actually owns

A workflow definition mixes step types that the **engine** executes itself
(`DECISION`, `DECISION_TABLE`, `TRANSFORMATION`, `PARALLEL_GATEWAY`, `JOIN_GATEWAY`, `USER_TASK`, `END`)
with `SERVICE_TASK` steps that delegate to an **external worker** over a `jobType`.

Scanning both JSON files for `"type": "SERVICE_TASK"` gives the complete contract the worker must implement:

| `jobType` | Source workflow | `retryCount` | Reads from variables | Writes to variables |
|-----------|-----------------|--------------|----------------------|---------------------|
| `validate-application` | loan-application | `2` | `applicationId` | `validationPassed` |
| `credit-score` | loan-application | `3` | `applicationId`, `customerId` | `creditScoreChecked`, `creditScore` |
| `fraud-screen` | loan-application | `3` | `applicationId` | `fraudScreened`, `fraudScore` |
| `escalate-review` | loan-application | — | `applicationId` | `reviewEscalated` |
| `approve-loan` | loan-application | `3` | `applicationId` | `loanApproved` |
| `prepare-disbursement` | loan-disbursement | `3` | `applicationId` | `prepareDisbursement` |
| `transfer-funds` | loan-disbursement | `5` | `applicationId` | `transferFunds` |
| `notify-disbursement` | loan-disbursement | `2` | `applicationId`, `customerId` | `disbursementNotified` |
| `notify-approval-overdue` | loan-disbursement | — | `applicationId`, `customerId` | `approvalOverdueNotified` |

That table is the entire backlog: **one Go function per row**, registered against the matching `jobType`. Everything else (routing, joining, decision tables, timers) is the engine's responsibility.

!!! tip "How to read the contract"
    `Reads from variables` are populated upstream — either by an earlier handler's `VariablesToSet`, by a `TRANSFORMATION` step (e.g. `compute-disbursement`), or by the request that started the instance. The handler must tolerate the variable being missing or of an unexpected type — see [§5](#5-anatomy-of-a-handler).

---

## 2. Project layout

```
go-rochallor-worker/
├── cmd/worker/main.go              ← entry point: client + registry + runner
├── internal/
│   ├── config/config.go            ← env-driven configuration
│   ├── application/                ← one file per jobType in the loan-application flow
│   │   ├── register.go
│   │   ├── validate.go
│   │   ├── credit.go
│   │   ├── fraud.go
│   │   ├── escalate.go
│   │   └── approve.go
│   └── disbursment/                ← one file per jobType in the loan-disbursement flow
│       ├── register.go
│       ├── prepare.go
│       ├── transfer.go
│       └── notify.go
├── workflowDefinition/             ← the two JSON definitions
├── go.mod
└── go.sum
```

The split is purely organisational: the SDK only cares about a single `*handler.Registry` populated with `(jobType → function)` pairs. Grouping by **business domain** keeps each handler short and lets a future second worker (say, a disbursement-only worker) pick up `disbursment.Register` without touching the application handlers.

---

## 3. Wiring the runner — `cmd/worker/main.go`

```go
package main

import (
    "context"
    "go-rochallor-worker/internal/application"
    "go-rochallor-worker/internal/config"
    "go-rochallor-worker/internal/disbursment"
    "log/slog"
    "os/signal"
    "syscall"

    "github.com/batnam/rochallor-engine/workflow-sdk-go/client"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/handler"
    "github.com/batnam/rochallor-engine/workflow-sdk-go/runner"
)

func main() {
    cfg := config.Load()

    engine := client.NewRest(cfg.EngineURL, cfg.WorkerID)

    registry := handler.New()
    application.Register(registry)
    disbursment.Register(registry)

    r := runner.New(runner.Config{
        WorkerID:     cfg.WorkerID,
        Parallelism:  cfg.Parallelism,
        PollInterval: cfg.PollInterval,
    }, engine, registry)

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    slog.Info("los-worker started", "worker_id", cfg.WorkerID, "engine", cfg.EngineURL)
    r.Run(ctx)
    slog.Info("los-worker stopped")
}
```

Four moving parts — that's the whole framework:

1. **Engine client** — `client.NewRest(url, workerID)` produces an `EngineClient`. Swap for `client.NewGrpc(target, workerID)` to use gRPC instead; nothing else in `main` changes because the runner depends on the interface, not the implementation.
2. **Registry** — an in-memory `jobType → Handler` map. `application.Register` and `disbursment.Register` are thin functions defined per domain (see [§4](#4-domain-registration)).
3. **Runner** — `runner.New(cfg, engine, registry)` builds the poll/dispatch loop. `WorkerID` is required; `Parallelism` defaults to 64 and `PollInterval` to 500 ms if you leave them zero.
4. **Signal-aware context** — `signal.NotifyContext` cancels `ctx` on `SIGINT`/`SIGTERM`. `Runner.Run` blocks until that cancellation, then **waits for in-flight handlers to finish** before returning. Don't `os.Exit` afterwards — let `main` unwind naturally so the deferred `stop()` releases the signal handler.

### How polling actually works

```
ticker (every PollInterval)
   └─ POST /v1/jobs/poll  ──►  engine (FOR UPDATE SKIP LOCKED, up to Parallelism rows)
        └─ for each Job → goroutine bounded by semaphore (size = Parallelism)
              └─ registry.Get(jobType) → handler(ctx, JobContext) → Result
                   ├─ nil error              → CompleteJob(VariablesToSet)
                   ├─ *retry.NonRetryable    → FailJob(retryable=false)
                   └─ any other error        → FailJob(retryable=true)
```

See [`runner/runner.go`](https://github.com/batnam/rochallor-engine/blob/main/workflow-sdk-go/runner/runner.go) for the full implementation and [architecture.md — Worker polling model](../architecture.md#worker-polling-model) for the sequence diagram.

---

## 4. Domain registration

Each domain package exposes a single `Register` function. That keeps `main.go` agnostic about which handlers exist:

```go
// internal/application/register.go
package application

import "github.com/batnam/rochallor-engine/workflow-sdk-go/handler"

func Register(r *handler.Registry) {
    r.Register("validate-application", validateApplication)
    r.Register("credit-score", creditScore)
    r.Register("fraud-screen", fraudScreen)
    r.Register("escalate-review", escalateReview)
    r.Register("approve-loan", approveLoan)
}
```

```go
// internal/disbursment/register.go
package disbursment

import "github.com/batnam/rochallor-engine/workflow-sdk-go/handler"

func Register(r *handler.Registry) {
    r.Register("prepare-disbursement", prepareDisbursement)
    r.Register("transfer-funds", transferFunds)
    r.Register("notify-approval-overdue", notifyApprovalOverdue)
    r.Register("notify-disbursement", notifyDisbursement)
}
```

The string passed to `Register` **must match the `jobType` field in the workflow JSON exactly**. A typo here surfaces only at runtime: the engine will keep handing out jobs the worker can't service, and the runner will log `no handler registered for <type>` and `FailJob(retryable=false)`. The instance will sit stuck until you redeploy with the correct registration. Treat the table in [§1](#1-what-the-worker-actually-owns) as the single source of truth and review it during code review.

---

## 5. Anatomy of a handler

Every handler has the same shape:

```go
type Handler func(ctx context.Context, job JobContext) (Result, error)
```

`JobContext` is what the engine hands you for each unit of work:

| Field | Type | Notes |
|-------|------|-------|
| `JobID` | `string` | Stable per job — use as an idempotency key for any side-effect. |
| `InstanceID` | `string` | The workflow instance this job belongs to. |
| `StepID` | `string` | The `step_executions` row driving this job. |
| `JobType` | `string` | Matches the registration key. |
| `Attempt` / `RetriesRemaining` | `int` | Useful for logging and circuit-breaking. |
| `Variables` | `map[string]any` | The full variables map at lock time. |

A representative handler — `validate-application`:

```go
// internal/application/validate.go
func validateApplication(ctx context.Context, job handler.JobContext) (handler.Result, error) {
    applicationID, ok := job.Variables["applicationId"].(string)

    slog.Info("##### validateApplication called", "applicationId", applicationID)
    if !ok || applicationID == "" {
        return handler.Result{}, &retry.NonRetryable{
            Cause: errors.New("missing applicationId"),
        }
    }

    // TODO call to validation service
    // e.g. err := validationService.Validate(ctx, applicationID)

    return handler.Result{
        VariablesToSet: map[string]any{
            "validationPassed": true,
        },
    }, nil
}
```

Three things to internalise:

### 5.1 Variables come in as `map[string]any`

Use a type assertion with the comma-ok idiom. Treat **any** of these as a permanent input error:

- key missing (`ok == false`)
- empty string / zero-value
- wrong type (e.g. `applicationId` arrived as a number)

These are bugs in the upstream caller or workflow definition, not transient failures. Retrying won't help — fail fast and let an operator investigate.

### 5.2 Two error channels: retryable vs `NonRetryable`

```go
// Permanent — bypass retry budget, fail the step immediately.
return handler.Result{}, &retry.NonRetryable{Cause: errors.New("missing applicationId")}

// Transient — runner calls FailJob(retryable=true), engine re-enqueues
// up to retryCount times with exponential backoff (100 ms × 2^n, ±20% jitter, capped at 30 s).
return handler.Result{}, fmt.Errorf("credit bureau unavailable: %w", err)
```

The decision is per **call**, not per handler — the same function can return `NonRetryable` for bad input and a plain error for a transient HTTP 503 on the next request. Match the `retryCount` in the workflow JSON to how many retries each operation genuinely deserves: `transfer-funds` has `retryCount: 5` because a flaky payment network is normal; `validate-application` has only `2` because failures are mostly malformed input that won't get better.

### 5.3 `VariablesToSet` is shallow-merged into the instance

Anything you put in the map is visible to **every downstream step**. That is the contract the engine relies on:

| Where the downstream consumer lives | Example from the LOS workflow |
|--------------------------------------|-------------------------------|
| `DECISION_TABLE` rule input | `classify-risk-tier` reads `creditScore` and `fraudScore` produced by the parallel handlers. |
| `DECISION` condition expression | `route-application` branches on `#riskTier`, which the decision table wrote. |
| `TRANSFORMATION` source | `compute-disbursement` reads `loanAmount` (set by the workflow starter) and writes `disbursementFee`, `netAmount`, `requiresSeniorApproval`. |
| The next `SERVICE_TASK` handler | `notify-disbursement` reads `applicationId` and `customerId` that the application workflow established. |

Keep `VariablesToSet` minimal. Don't echo back variables you didn't change — it adds noise to the audit trail and risks accidentally overwriting newer values written by a parallel branch.

---

## 6. A second handler — input from two upstream sources

`credit-score` shows what reading more than one variable looks like:

```go
// internal/application/credit.go
func creditScore(ctx context.Context, job handler.JobContext) (handler.Result, error) {
    applicationID, ok := job.Variables["applicationId"].(string)
    customerId, okCus := job.Variables["customerId"].(string)

    slog.Info("##### creditScore called", "applicationId", applicationID)
    if !ok || !okCus || applicationID == "" || customerId == "" {
        return handler.Result{}, &retry.NonRetryable{
            Cause: errors.New("missing applicationId or customerId"),
        }
    }

    // TODO call to validation service
    var creditScoreValue = 1000
    if customerId == "123456789" {
        creditScoreValue = 649
    }
    return handler.Result{
        VariablesToSet: map[string]any{
            "creditScoreChecked": true,
            "creditScore":        creditScoreValue,
        },
    }, nil
}
```

The handler runs concurrently with `fraud-screen` thanks to the `PARALLEL_GATEWAY` step. The engine's `JOIN_GATEWAY` ([`merge-risk-results`](https://github.com/batnam/rochallor-engine/blob/main/docs/example/go-rochallor-worker/workflowDefinition/workflow-loan-application.json)) waits for both branches before advancing to the decision table — you don't have to coordinate anything in the worker.

---

## 7. Configuration — `internal/config/config.go`

```go
type Config struct {
    EngineURL    string
    WorkerID     string
    Parallelism  int
    PollInterval time.Duration
}

func Load() Config {
    return Config{
        EngineURL:    env("ENGINE_URL", "http://localhost:8080"),
        WorkerID:     env("WORKER_ID", "los-worder-1"),
        Parallelism:  envInt("PARALLELISM", 64),
        PollInterval: envDuration("POLL_INTERVAL_MS", 500) * time.Millisecond,
    }
}
```

| Env var | Default | Tuning guidance |
|---------|---------|-----------------|
| `ENGINE_URL` | `http://localhost:8080` | Engine REST base URL. For gRPC, set the gRPC `host:port` and switch `main.go` to `client.NewGrpc`. |
| `WORKER_ID` | `los-worder-1` | **Must be unique per process.** Used by `FOR UPDATE SKIP LOCKED` accounting and surfaced in `worker_id` columns on `step_executions`. |
| `PARALLELISM` | `64` | Hard cap on concurrent in-flight handlers. Size for the **slowest** downstream system you call. |
| `POLL_INTERVAL_MS` | `500` | Lower for latency-sensitive workloads, higher to reduce engine load. Consider the [Kafka push mode](./go.md#kafka-dispatch-opt-in) before going below 100 ms. |

Keep the loader dumb: env-only, sensible defaults, no flags or YAML. A 12-factor worker container is the path of least friction in Kubernetes/Compose.

---

## 8. Build & run

```bash
# from docs/example/go-rochallor-worker
go build -o bin/worker ./cmd/worker

# 1. start engine + Postgres (see Getting Started)
# 2. upload both workflow definitions via the engine REST API or the Modeller
# 3. start the worker
ENGINE_URL=http://localhost:8080 \
WORKER_ID=los-worker-1 \
PARALLELISM=32 \
POLL_INTERVAL_MS=250 \
./bin/worker
```

### 8.1 Start an instance

The starting payload must contain every variable the workflow reads before it
reaches the first `SERVICE_TASK` — for the LOS flow that means `applicationId`,
`customerId`, and `loanAmount`. Setting `businessKey` to the application ID is
strongly recommended: it gives you a stable handle for the chained disbursement
instance later (see [§8.3](#83-look-up-the-instanceid)).

```bash
curl --location 'http://localhost:8080/v1/instances' \
  --header 'Content-Type: application/json' \
  --data-raw '{
    "definitionId": "LOS::loan-application-full",
    "variables": {
      "applicationId": "APP-20240417-001",
      "customerId":    "123456789",
      "loanAmount":    500000001,
      "applicantEmail": "nguyen.van.a@example.com"
    },
    "businessKey": "APP-20240417-001"
  }'
```

`loanAmount: 500000001` is intentional — it exceeds the `500_000_000` threshold
in `compute-disbursement`'s `requiresSeniorApproval` transform, so the
disbursement workflow will pause at `senior-approval-task` and exercise the
user-task flow below. Lower the amount to route straight to `prepare-disbursement`.

The worker's `slog` output will trace each `jobType` as the engine dispatches it;
the engine UI / Modeller shows the instance progressing through the decision
table and into the chained disbursement workflow without further input — until
it hits a `USER_TASK`.

### 8.2 Completing a USER_TASK

Both definitions contain user tasks that block until an operator (or an
integration acting as one) resolves them:

| Workflow | Step ID | Triggered when |
|----------|---------|----------------|
| `LOS::loan-application-full` | `manual-review-task` | Decision table classifies the application as `MEDIUM` risk. |
| `LOS::loan-disbursement-workflow` | `senior-approval-task` | `loanAmount > 500_000_000` (i.e. `requiresSeniorApproval == true`). |

Complete them with `POST /v1/instances/{instanceId}/user-tasks/{userTaskId}/complete`.
The body's `variables` map is shallow-merged into the instance, the same way a
`SERVICE_TASK` handler's `VariablesToSet` is — so put the **decision variable**
that the downstream `DECISION` step expects right here:

```bash
# Senior approval (disbursement workflow)
# DECISION 'check-senior-decision' branches on #seniorDecision
curl --location 'http://localhost:8080/v1/instances/{{INSTANCE_ID}}/user-tasks/senior-approval-task/complete' \
  --header 'Content-Type: application/json' \
  --data '{
    "variables": {
      "seniorDecision": "APPROVED"
    }
  }'
```

```bash
# Manual underwriter review (application workflow)
# DECISION 'process-review-decision' branches on #reviewDecision
curl --location 'http://localhost:8080/v1/instances/{{INSTANCE_ID}}/user-tasks/manual-review-task/complete' \
  --header 'Content-Type: application/json' \
  --data '{
    "variables": {
      "reviewDecision": "APPROVED"
    }
  }'
```

The accepted values come directly from the `conditionalNextSteps` keys in the
JSON: `APPROVED` or `REJECTED` in both cases. A value that matches no branch
leaves the instance stuck at the decision — match the strings exactly.

> note "Boundary-timer interaction"
    Both user tasks attach a non-interrupting `TIMER` boundary event
    (`PT48H` on manual review, `PT8H` on senior approval). The timer fires a
    parallel branch (`escalate-review` / `notify-approval-overdue`) **without
    cancelling the user task** — completing the user task after the timer has
    fired still progresses the main flow. The two end states (`end-escalated`,
    `end-disbursement-timeout`) just record that the SLA was breached.

### 8.3 Look up the `instanceId`

You don't have to keep the IDs returned by `POST /v1/instances`. As long as you
set `businessKey` at start time, the list endpoint resolves the right instance
for each step. Filter by `definitionId` and `status=WAITING` to find the
instance currently parked on a user task:

```bash
# Parent — waiting at manual-review-task
curl --location 'http://localhost:8080/v1/instances?businessKey=APP-20240417-001&definitionId=LOS%3A%3Aloan-application-full&status=WAITING'

# Child — waiting at senior-approval-task
curl --location 'http://localhost:8080/v1/instances?businessKey=APP-20240417-001&definitionId=LOS%3A%3Aloan-disbursement-workflow&status=WAITING'
```

Each entry's `id` is the `{instanceId}` you plug into the `complete` URL above.
The `currentStepIds` field tells you which user task is blocking — pair it with
the `userTaskId` in the path so you don't try to complete a task that isn't
actually waiting.

> tip "Why URL-encode the colons?"
    `LOS::loan-application-full` contains `::`. As a path segment that's fine,
    but as a query-string value most HTTP clients require percent-encoding
    (`%3A%3A`). `curl --data-urlencode 'definitionId=LOS::loan-application-full'`
    is an equivalent way to avoid hand-encoding.

---

## 9. Patterns and pitfalls

**Do**

- Keep one file per `jobType` so the registry table and the filesystem stay in sync.
- Validate every variable you read; fail with `NonRetryable` when the input is structurally wrong.
- Use `job.JobID` as the idempotency key for any external side-effect — even in polling mode the engine can re-dispatch a job whose lock expired mid-execution.
- Match `retryCount` in the workflow JSON to the failure mode of the underlying call.

**Don't**

- Don't share a single `WORKER_ID` across two processes — both will poll the same queue and you'll see lock-conflict noise in metrics.
- Don't put business logic in `main.go`. The runner wiring should be boring; everything interesting lives behind a `Register` call.
- Don't echo unchanged variables in `VariablesToSet`. Set only what you produced.
- Don't catch `panic` yourself — the runner already recovers panics into a `NonRetryable` error so a crashed handler doesn't take the worker down.

---

## 10. Where to go next

- **API reference** — every type and field used here is enumerated in [Go SDK](./go.md).
- **Push-based dispatch** — swap polling for Kafka with [`kafkarunner`](./go.md#kafka-dispatch-opt-in).
- **Workflow JSON shape** — full grammar for `SERVICE_TASK`, `DECISION_TABLE`, `TRANSFORMATION`, boundary events, and chaining lives in [Workflow Format](../workflow-format.md).
- **Operational model** — backoff policy, lock expiry, and shutdown drain are documented in [Architecture](../architecture.md).
