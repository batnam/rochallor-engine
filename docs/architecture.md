# Architecture

## Overview

```
proto/workflow/v1/engine.proto   (canonical wire contract)
              │
              ▼
┌────────────────────────────────────────────────────────┐
│                  workflow-engine (Go)                  │
│                                                        │
│  REST  :8080   /v1/definitions                         │
│                /v1/instances                           │
│                /v1/instances/{id}/user-tasks/          │
│                            {stepId}/complete           │
│                /v1/instances/{id}/signals/             │
│                            {stepId}                    │
│                /v1/jobs/poll|complete|fail             │
│                                                        │
│  gRPC  :9090   WorkflowEngine service                  │
│                (mirrors REST surface)                  │
│                                                        │
│  Metrics :9091 /metrics (Prometheus)                   │
│                                                        │
│  [Two fully supported parallel dispatch architectures] │
│  ┌───────────────────────┐   ┌───────────────────────┐ │
│  │ Short-Polling Mode    │   │ Event-Driven Mode     │ │
│  │ (Default)             │   │ (Opt-In)              │ │
│  │                       │   │                       │ │
│  │ PostgreSQL            │   │ PostgreSQL            │ │
│  │ FOR UPDATE            │   │ Transaction Outbox    │ │
│  │ SKIP LOCKED           │   │        │              │ │
│  │ job queue             │   │        ▼              │ │
│  │                       │   │ Kafka Broker(s)       │ │
│  └───────────┬───────────┘   └────────┬──────────────┘ │
└──────────────┼────────────────────────┼────────────────┘
               │ (Polling)              │ (Consuming)
      ┌────────┴─────────┬──────────────┴─────────┐
      ▼                  ▼                        ▼
   Go SDK             Java SDK               Node/TS SDK
```

**Engine** — The core Go service. Accepts workflow definitions (JSON), creates instances, and distributes `SERVICE_TASK` jobs to workers via a poll/lock/execute model. The only supported definition format is the engine's own JSON schema.

**SDKs** — Thin polling workers for each language. Each SDK implements the same pattern: register a handler function per `jobType`, start a `Runner`, and the runner polls, dispatches, and calls `complete_job` or `fail_job` based on the handler's outcome.

**Proto** — `proto/workflow/v1/engine.proto` is the canonical contract. The REST OpenAPI spec (`workflow-engine/api/openapi/rest-openapi.yaml`) mirrors it. Generated Go code lives in `workflow-engine/api/gen/workflow/v1/` and must be regenerated via `make proto-gen` (not committed to the repository).

## Rochallor Monitor

Rochallor Monitor is an independently deployed, read-only observability
application. Its browser frontend calls a same-origin `/api` route, which Nginx
proxies to the monitor BFF. The BFF reads the workflow engine's PostgreSQL
schema directly through a dedicated read-only database role.

```text
Browser ── /api ──> Nginx ──> Monitor BFF ── SELECT ──> PostgreSQL
                              (no engine API dependency)
```

The monitor does not embed the engine, run schema migrations, or write
workflow state. Authentication and network access control remain the
deployment operator's responsibility. See [Monitor](monitor.md) for the
deployment contract, schema compatibility, and operational limitations.

---

## Dispatch Modes

The engine ships with two mutually-exclusive dispatch mechanisms, selected at startup via a single environment variable. **Short-polling is the default** and is what every deployment runs unless an operator explicitly opts into the alternative. The event-driven mode is **purely additive** — the polling path is not being replaced, deprecated, or feature-flagged out.

### The two modes at a glance

| | Short-polling via `SKIP LOCKED` (default) | Kafka + Transaction Outbox (opt-in) |
|---|---|---|
| Enabled when | `WE_DISPATCH_MODE` is unset, empty, or `polling` | `WE_DISPATCH_MODE=kafka_outbox` |
| Required dependencies | PostgreSQL only | PostgreSQL **and** a Kafka-compatible broker |
| Dispatch path | Workers `POST /v1/jobs/poll`; engine claims rows via atomic `UPDATE … FOR UPDATE SKIP LOCKED` | Engine writes a dispatch row to an outbox table inside the job-creation transaction; a leader-elected relay drains the outbox to Kafka; workers consume via consumer groups |
| Completion path | `POST /v1/jobs/complete` / `fail` — unchanged | **Identical.** Workers always complete via the same REST/gRPC API |
| Delivery guarantee | Strong (row lock is single source of truth) | At-least-once (workers must be idempotent on side-effects) |
| Ordering | Effectively global FIFO per `jobType` | FIFO per workflow instance (partition key = `instance_id`); not globally FIFO |
| Typical dispatch latency | Bounded below by half the poll interval (~200–500 ms) | Milliseconds (push-based) |
| Scaling ceiling | PostgreSQL row-lock contention | Broker throughput (orders of magnitude higher) |
| New failure modes | — | Broker outage, consumer rebalance, relay lag, advisory-lock split-brain |
| Extra ops cost at low traffic | ~0 | Non-trivial — Kafka cluster is neither free to run nor free to monitor |

### Straightforward guidance

**The engine fully supports both architectures in parallel. You choose which one fits your use case.**

**Use Short-polling (Default) when:**
- Your throughput is under `~3,000` jobs/sec.
- You prioritize operational simplicity and minimal infrastructure footprint.
- Your worker handlers are I/O bound or take significant time to process.

**Use Kafka + Outbox (Event-Driven) when BOTH of these are true:**
1. **Scale bottleneck:** Your workload has hit, or will measurably hit within 6–12 months, the polling path's row-lock contention ceiling. The concrete signals:
   - Sustained `wait_event_type='Lock'` on the `job` table under load.
   - Engine throughput plateaus regardless of how many replicas you add.
   - `workflow_engine_job_lock_conflicts_total` climbs steadily while the table has plenty of `UNLOCKED` rows.
2. **Operational readiness:** Your team either has Kafka operational experience, or has a credible budgeted plan to invest in it. Kafka is not a "set it and forget it" dependency; treating it as one is how production incidents are made.

> If **either** condition fails, stay on polling. Adding Kafka because it is "more modern" rather than because it solves a measured problem is a net regression for most deployments. The opt-in path exists to unblock a specific scale tier — not to recommend itself over the default.

### What is preserved across both modes

The following contracts are identical regardless of which dispatch mode you run:

- The REST + gRPC surface that workers use to complete or fail jobs.
- The workflow definition schema, the instance state machine, and the set of supported step types.
- The audit log (every dispatched, completed, failed, or retried job produces the same audit entries in both modes).
- Retry counts, lease semantics, and idempotent completion.
- The `job` table and every other existing table. The opt-in mode adds one new table (`dispatch_outbox`) that stays empty in polling mode.

### When (and how) to switch

The switch is a single environment variable plus an engine restart. No manual SQL is required in either direction. In-flight jobs already claimed under their lease finish under polling rules; new jobs created after the restart go through the newly selected dispatch path. Details, plus the full rollback procedure, live in  [Quickstart: Event-Driven Dispatch](quickstart-even-driven.md).

### Further reading on the opt-in mode

- **Honest polling-vs-event-driven tradeoff analysis** (recommended before opting in): [Tradeoffs: Polling vs Event-Driven (Kafka + Outbox)](tradeoffs.md)
- **Operator walkthrough — enable, verify, roll back**:  [Quickstart: Event-Driven Dispatch](quickstart-even-driven.md)

The rest of this document describes **the default (short-polling) path** in depth. Everything below applies to the mode every deployment runs out of the box.

---

## Transport Support

| SDK    | REST (stable) | gRPC (stable) | Event-Driven (stable) |
|--------|:---:|:---:|:-------------:|
| Go     | ✓   | ✓   |       ✓       |
| Java   | ✓   | ✓   |       ✓       |
| Node   | ✓   | ✓   |       ✓       |
| Python | ✓   | ✓   |       ✓       |

All transports implement the same `EngineClient` interface (poll / complete / fail). Use **REST** for simplicity; use **gRPC** when you need tighter coupling to the proto contract or lower latency.

---

## Parallel Job Processing

> **Scope of this section**: the **default short-polling dispatch mode**. If you have enabled `WE_DISPATCH_MODE=kafka_outbox`, this whole section's mechanism is replaced by the event-driven path described under [Dispatch Modes](#dispatch-modes) above. Everything else in the engine (state machine, completion API, retry semantics, audit log) is unchanged between modes.

The engine distributes `SERVICE_TASK` jobs to competing SDK workers using PostgreSQL's `FOR UPDATE SKIP LOCKED`. This section explains the mechanism so you can reason about throughput, contention, and observability when scaling workers.

### The problem: queue contention

A naïve job queue does `SELECT … WHERE status = 'UNLOCKED' LIMIT N`, then updates the rows to `LOCKED` in a second statement. Under concurrent workers, many workers read the same unlocked rows, attempt to update them, and block on each other's locks — the "thundering herd" problem. Throughput collapses under load.

### The solution: atomic lock-and-claim

The engine uses a single atomic statement that selects and locks in one round-trip:

```sql
UPDATE job
SET    status          = 'LOCKED',
       worker_id       = $1,
       locked_at       = now(),
       lock_expires_at = $2
WHERE  id IN (
    SELECT id
    FROM   job
    WHERE  status   = 'UNLOCKED'
    AND    job_type = ANY($3)
    ORDER  BY created_at
    FOR UPDATE SKIP LOCKED   -- <-- key clause
    LIMIT  $4
)
RETURNING id, instance_id, step_execution_id, job_type, status,
          worker_id, locked_at, lock_expires_at,
          retries_remaining, payload, created_at
```

`FOR UPDATE SKIP LOCKED` tells PostgreSQL: "acquire a row lock on each candidate row, but if any row is already locked by another transaction, skip it rather than waiting." The result is that each worker instantly claims its own disjoint batch of rows with no lock contention between workers. There is no coordination overhead and no starvation — workers proceed in parallel.

### Job state machine

```
UNLOCKED ──poll──► LOCKED ──complete──► COMPLETED
                     ├──fail────────► FAILED
                     └──lease expiry► CANCELLED

Retryable failure or lease recovery creates a separate UNLOCKED job.
Kafka callbacks can complete/fail an UNLOCKED job without polling.
```

| State | Meaning |
|-------|---------|
| `UNLOCKED` | Job is ready to be claimed by a worker. |
| `LOCKED` | Claimed by a specific worker; `lock_expires_at` is set. |
| `COMPLETED` | Worker called `complete_job` successfully. |
| `FAILED` | This delivery attempt failed; a replacement may be queued if retries remain. |
| `CANCELLED` | This delivery attempt was retired by lease recovery or interruption. |

### Partial index

The poll query is powered by a partial index that covers only claimable rows:

```sql
CREATE INDEX idx_job_unlocked_by_type
    ON job (status, job_type)
    WHERE status = 'UNLOCKED';
```

Because the index predicate matches the `WHERE status = 'UNLOCKED'` filter exactly, PostgreSQL scans only unlocked rows — the index shrinks as jobs are claimed and regrows as new jobs arrive. This keeps poll latency flat regardless of how many completed or failed rows accumulate in the table.

A second partial index drives the lease-expiry sweeper:

```sql
CREATE INDEX idx_job_lock_expires
    ON job (lock_expires_at)
    WHERE status = 'LOCKED';
```

### Lease expiry

Each lock carries a 30-second expiry (`lock_expires_at = now() + 30s`). For active/waiting instances, the sweeper retires an expired job as `CANCELLED` and creates a new `UNLOCKED` job for the same step execution, without spending a business retry. The replacement has a new `jobId`, even if the same worker claims it. Callbacks for an expired lease or a retired job are acknowledged without changing state. There is no lease-renewal API in this change: polling work that consistently exceeds 30 seconds will be redelivered instead of applying its late result.

### Callback ordering and delivery identity

`jobId` identifies one delivery attempt. A retryable failure marks that job `FAILED` and creates one replacement with a decremented retry budget. Repeated failures for the old ID cannot consume more retries or dispatch more work. A manual retry creates both a new step execution and a new job.

Completion and failure lock the instance before the job. They change state only while the instance is `ACTIVE`/`WAITING`, the step execution is `RUNNING`, and the job is eligible. Polling callbacks must match the existing `workerId` and an unexpired lease. State checks happen before output validation, variable updates or history writes. Terminal, superseded and duplicate callbacks are successful no-ops.

Kafka callbacks use the job ID already present in `JobDispatchEvent`; they do not acquire polling leases. An engine retry creates a new job and outbox event in one transaction. Broker redelivery of the same event is still the same attempt: the first accepted result wins, and later results are no-ops. The engine does not observe consumer ownership changes or prevent duplicate external side effects.

REST/gRPC request fields, Kafka event fields and SDK method signatures are unchanged. No authentication, authorization or token mechanism is added; API security remains the responsibility of the surrounding system.

**Upgrade impact:** workers that deduplicate external operations across retries must use a stable business operation key from variables (for example, an invoice/payment ID), not `jobId`. `stepExecutionId` remains stable across automatic retries and lease recovery, but changes on manual retry. Existing SDK clients need no wire changes. Coordinate the engine rollout: old replicas still reuse job IDs and do not enforce these ordering rules, so the guarantees apply only once all engine replicas run the new code. The delivery-identity change itself needs no schema migration.

### Durable boundary timers

The timer sweeper reads due candidates without changing them. For each candidate,
`FireBoundaryEvent` locks the instance first, then conditionally marks the timer
`fired=true` inside the transaction that dispatches its target. Interruption of
the source step, cancellation of its job/user task, the target job and any Kafka
outbox record all commit together. A dispatch failure, rollback or lost database
connection leaves the timer pending for a later sweep. Due timers also survive
engine downtime.

The same instance lock serializes timers with completion, cancellation and other
timers. A replay of a consumed timer does nothing. If the instance is terminal or
the source step has left `RUNNING`, the timer is consumed without dispatching a
target. Non-interrupting timers leave the source step running. Polling and Kafka
use the same transaction rules; Kafka publication still has at-least-once delivery.

### Durable workflow chaining

Reaching `END` inserts one `workflow_chain` request per source instance in the
same transaction as parent completion. It records the target definition ID and
the parent's variables/business key at completion. A rollback cannot expose the
request or create a child. The engine's chain worker checks pending requests
every second and resumes them after restart.

The worker resolves the latest target definition and validates its input, then
locks the pending request with `FOR UPDATE SKIP LOCKED`. Creating the child, its
initial work/outbox records and recording `child_instance_id` all commit together.
Concurrent workers or replay after a successful commit cannot create another
child, even if the first child has already completed. A child that reaches `END`
can enqueue its own next-workflow request in that same transaction.

A missing target definition, schema mismatch, business-key conflict or database/
dispatch failure leaves the request pending. Other requests continue processing;
startup failures are retried and logged with source instance and target definition
IDs. Fixing/publishing the target definition or resolving the active business-key
conflict allows a later attempt to proceed. Once a child has been created, its
subsequent failures use the normal instance/job lifecycle, not another chain start.

Pending requests can be inspected with:

```sql
SELECT source_instance_id, target_definition_id, created_at
FROM workflow_chain
WHERE child_instance_id IS NULL
ORDER BY created_at, source_instance_id;
```

**Upgrade:** migration `0011_workflow_chain` adds the durable request table and its
pending index; engine startup applies it through the existing migration runner.
All replicas must run the new timer/chain code for these guarantees to hold.
Already-lost requests or timers consumed by older code cannot be reconstructed
automatically. Do not drop the chain table with pending requests during a downgrade.
REST/gRPC, workflow JSON and Kafka event fields are unchanged; API security stays
with the surrounding system.

### Observability

Two Prometheus metrics track job queue health:

| Metric | Type | Meaning |
|--------|------|---------|
| `workflow_engine_job_lock_conflicts_total` | Counter | Incremented each time a worker polls and finds zero rows available (all candidates already locked by other workers). A rising rate signals you need more workers or a higher `LIMIT`. |
| `workflow_engine_job_poll_latency_seconds` | Histogram | End-to-end latency from job creation to the first successful lock acquisition. Use the p99 bucket to detect scheduler lag. |

### Throughput estimate

The numbers below are back-of-envelope estimates based on typical PostgreSQL behaviour for the `FOR UPDATE SKIP LOCKED` pattern on commodity hardware. They are not benchmarks — your actual numbers will vary with hardware, network latency, row size, and handler duration.

**Baseline assumptions**

| Parameter | Value |
|-----------|-------|
| PostgreSQL on dedicated instance | 8 vCPU, 32 GB RAM, SSD |
| `SKIP LOCKED` UPDATE round-trip | ~1–2 ms (LAN, same AZ) |
| Poll batch size per worker | 10 jobs |
| Worker processes | 4 (one per SDK node) |
| Parallelism per worker | 64 goroutines / threads |

**Single-worker ceiling**

A single runner with a 500 ms poll interval and batch size of 10 claims roughly **20 jobs/s** at idle queue depth. With a 50 ms poll interval and batch 10 it approaches **200 jobs/s**, but the poll loop itself then contributes ~20% of the database load.

**Multi-worker scaling**

Because `SKIP LOCKED` serialises only at the row level (not the table level), throughput scales near-linearly with worker count up to the point where the database WAL writer or I/O becomes the bottleneck:

| Workers | Batch size | Poll interval | Estimated throughput |
|---------|-----------|---------------|----------------------|
| 1       | 10        | 500 ms        | ~20 jobs/s           |
| 4       | 10        | 500 ms        | ~80 jobs/s           |
| 8       | 10        | 250 ms        | ~300 jobs/s          |
| 16      | 20        | 100 ms        | ~1 000 jobs/s        |
| 32      | 50        | 50 ms         | ~2 500 jobs/s        |

> These figures assume the job handler itself completes near-instantly (e.g. a simple DB write). If handlers call external services and take 100–500 ms each, the bottleneck shifts from the poll loop to handler concurrency — increase `parallelism` rather than adding more workers.

**Practical ceiling**

On a well-tuned PostgreSQL instance (connection pooling via PgBouncer, autovacuum tuned for high-churn tables), the `FOR UPDATE SKIP LOCKED` pattern can sustain **3 000–5 000 job claims per second** before WAL I/O becomes the limiting factor. Beyond that, you would need to shard the job table by `job_type` or introduce a dedicated queue service.

**Rule of thumb**: start with 2–4 workers at batch size 10 and poll interval 250 ms. Monitor `workflow_engine_job_poll_latency_seconds` (p99) and `workflow_engine_job_lock_conflicts_total`. Scale workers horizontally if p99 latency grows; reduce poll interval if conflict rate stays near zero (workers are idle).

### Scaling guidance

- **Add workers freely.** Because `SKIP LOCKED` eliminates contention, adding more workers increases throughput linearly until the database I/O becomes the bottleneck.
- **Tune the poll batch size.** Each poll claims up to `N` rows (configured per runner). A larger batch reduces poll round-trips; a smaller batch distributes work more evenly across workers at low queue depth.
- **Watch `job_lock_conflicts_total`.** If this counter rises steadily, workers are polling faster than jobs arrive — reduce the poll interval rather than adding workers.

---

## Worker polling model

This section explains what happens inside an SDK worker process — from `registry.register(...)` through to `completeJob` / `failJob`. Understanding this model helps you reason about latency, concurrency, and error handling in your handlers.

### Short polling — not long polling

The engine uses **short polling**: workers call `POST /v1/jobs/poll` on a fixed interval (default 500 ms), the engine responds immediately (with jobs or an empty array), and the connection closes. The engine never holds the connection open waiting for work to arrive.

Long polling is not used because PostgreSQL is the queue backend — the engine has no native push mechanism to notify workers when a new job is ready.

```
Worker                          Engine                        PostgreSQL
──────                          ──────                        ──────────
every 500 ms:
  POST /v1/jobs/poll ──────►                                  UPDATE job
  {workerID, jobTypes,                                        SET status='LOCKED'
   maxJobs=64}                  runs SKIP LOCKED query  ──►   WHERE status='UNLOCKED'
                                                              FOR UPDATE SKIP LOCKED
                                                              LIMIT 64
                                ◄── returns claimed rows ──
  ◄── [] or [job, job, …] ──
```

### The poll / dispatch loop

When you call `runner.start()` (Java) or `runner.Run(ctx)` (Go), the runner enters this loop:

```
┌─────────────────────────────────────────────────────────┐
│                      Runner loop                        │
│                                                         │
│  ticker fires (500 ms default)                          │
│       │                                                 │
│       ▼                                                 │
│  PollJobs(workerID, jobTypes, maxJobs)                  │
│       │                                                 │
│       ├── empty response → sleep, next tick             │
│       │                                                 │
│       └── [job1, job2, …]                               │
│               │                                         │
│               └── for each job: acquire semaphore slot  │
│                       │        (max = parallelism)      │
│                       ▼                                 │
│                 goroutine / thread                      │
│                       │                                 │
│                       ▼                                 │
│                 registry.get(job.jobType)               │
│                       │                                 │
│                       ▼                                 │
│                 handler(jobContext)                     │
│                       │                                 │
│           ┌───────────┴────────────┐                    │
│           ▼                        ▼                    │
│       no error                  error                   │
│           │                        │                    │
│     CompleteJob              NonRetryable?              │
│     {variablesToSet}          ├── yes → FailJob         │
│           │                   │        retryable=false  │
│           │                   └── no  → FailJob         │
│           │                            retryable=true   │
│           │                                             │
│     release semaphore slot                              │
└─────────────────────────────────────────────────────────┘
```

`registry.register(jobType, handler)` does nothing more than insert a function reference into a map. The runner reads that map when it needs to dispatch a received job.

### Retry flow

Every job carries a `retries_remaining` counter set from the step's `retryCount` field in the workflow definition. The engine decrements it on each retryable failure.

```
handler throws plain Exception / Error
    → runner calls FailJob(retryable=true)
    → engine: retries_remaining > 0 ?
          yes → old job stays FAILED; a new UNLOCKED job is dispatched
          no  → job → FAILED, instance → FAILED

handler throws NonRetryableException / NonRetryableError
    → runner calls FailJob(retryable=false)
    → engine ignores retries_remaining, marks FAILED immediately
```

Example in the workflow definition:

```json
{
  "id": "credit-score",
  "type": "SERVICE_TASK",
  "jobType": "credit-score",
  "retryCount": 3
}
```

With `retryCount: 3`, a plain exception will retry up to 3 times before the job is permanently failed. A `NonRetryableException` skips all 3 retries.

### Manual retry (operator-driven)

Auto retry handles transient failures. For everything else — bad input data, downstream system fixed after the fact, a config bug ops repaired by hand — the engine exposes a manual retry endpoint that re-runs a failed step on a FAILED instance.

```
POST /v1/instances/{id}/steps/{stepId}/retry
Content-Type: application/json    (optional body)

{ "variables": { "corrected": true, "amount": 100 } }   # optional patch
```

The same operation is on the gRPC surface as `WorkflowEngine.RetryStep` with fields `instance_id`, `step_id`, and an optional `variables` map.

**What the engine does, atomically**

1. Validates the instance is `FAILED` and the latest `step_execution` attempt for `stepId` is `FAILED`. RUNNING / COMPLETED / SKIPPED attempts are rejected with HTTP 409.
2. Flips the instance back to `ACTIVE`, clearing `failure_reason` and `completed_at`.
3. If `variables` is non-empty, shallow-merges its keys into the instance variables (same semantics as `CompleteJob` / `SignalWait` / `CompleteUserTask`).
4. Inserts a **new** `step_execution` row with `attempt_number` incremented — the previously FAILED row is left in place for audit.
5. Re-dispatches the step. For `SERVICE_TASK`, a fresh `UNLOCKED` job is enqueued with `retries_remaining` reset from the definition's `retryCount`; the previously FAILED job row is untouched (status stays `FAILED`, so workers ignore it).

From the worker's point of view the retry is indistinguishable from a normal dispatch — it sees a new job with the (possibly patched) variables in its payload. When the worker completes, the engine advances to the next step exactly as it would on the first attempt, so the entire downstream workflow continues automatically.

**When to use it**

- **Transient failure that auto retry didn't cover.** External system was down longer than `retryCount` rounds. Fix the system, hit retry.
- **Bad input data.** Validation step failed because a variable was wrong. Send the corrected value in the request body — the new dispatch reads the patched variables.
- **Long workflows you don't want to restart.** Step 49 of 50 failed; restarting from step 1 would re-do expensive prior side effects. Retry preserves the existing instance state and history.

**When *not* to use it**

- Step is fundamentally buggy and will fail every time — fix the workflow definition or the worker code first, otherwise you'll just trigger the same failure with one extra row of audit noise.
- Step's side effects are not idempotent (charged a card, sent an email) AND the prior failed attempt may have partially succeeded. Make the worker idempotent (idempotency key on external calls) before relying on manual retry.
- Instance is `COMPLETED` or `CANCELLED` — retry only works on FAILED instances. To re-run a successful step, start a new instance.

**Error responses**

| HTTP / gRPC | Meaning |
|-------------|---------|
| 404 / `NOT_FOUND` | Instance id does not exist. |
| 409 / `FAILED_PRECONDITION` | Instance is not in status `FAILED`, or the latest attempt for the named step is not `FAILED`. |
| 409 / `ALREADY_EXISTS` | Another in-flight instance now holds the same `business_key` for this definition — the ACTIVE flip would violate the partial unique index. Cancel the other instance, or pick a different retry strategy. |
| 400 / `INVALID_ARGUMENT` | Malformed request body or empty `instance_id` / `step_id`. |

**Audit trail**

Every retry creates a new `step_execution` row with `attempt_number` one greater than the latest existing row. `GET /v1/instances/{id}/history` lists all attempts in start order, so you can always tell which were auto retries (same row, no new attempt — auto retries reuse one `step_execution`) versus manual retries (new row per retry).

### Graceful shutdown

When the runner receives a shutdown signal (SIGINT/SIGTERM or context cancellation), it:

1. Stops accepting new jobs from the poll loop.
2. Waits for all in-flight handlers to finish (drain).
3. Exits only after the drain completes.

Jobs that were in-flight but not yet completed will have their lock expire (30 s) and be reclaimed by the lease sweeper — another worker will pick them up.
