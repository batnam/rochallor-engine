# Node / TypeScript SDK

**Package**: `rochallor-workflow-sdk-node` ([npm](https://www.npmjs.com/package/rochallor-workflow-sdk-node))

## Install and import

Requires **Node.js 24+**. The package emits ESM JavaScript and TypeScript declarations. Use `"type": "module"` in your application and `module` / `moduleResolution: "NodeNext"` when compiling TypeScript.

```sh
npm install rochallor-workflow-sdk-node
```

With npm 12, allow the Kafka native dependency's install script in your application's `package.json` before installing:

```json
{ "allowScripts": { "@confluentinc/kafka-javascript": true } }
```

For an existing npm 12 installation, run `npm install-scripts approve @confluentinc/kafka-javascript` and `npm rebuild @confluentinc/kafka-javascript`. Importing the root API also loads the Kafka native dependency, even when using the polling runner. Container verification uses Node 24 on Alpine Linux.

All types below are exported from `rochallor-workflow-sdk-node`; consumers do not generate protos or compile SDK source. Existing `dist/...` paths remain available for compatibility; use the root import in new code. No separate CommonJS build is provided.

## Key types

| Module | Type | Purpose |
|--------|------|---------|
| `src/client/rest.ts` | `RestEngineClient(baseUrl)` | REST client using `undici` |
| `src/client/grpc.ts` | `GrpcEngineClient(target, credentials?)` | gRPC client using `@grpc/grpc-js` |
| `src/client/types.ts` | `EngineClient` interface | Transport abstraction |
| `src/client/types.ts` | `Job` | Job record returned by `pollJobs` |
| `src/handler/registry.ts` | `HandlerRegistry` | Maps `jobType` strings to `Handler` functions |
| `src/handler/registry.ts` | `JobContext` | Passed to every handler — `jobId`, `instanceId`, `jobType`, `retriesRemaining`, `variables` |
| `src/handler/registry.ts` | `HandlerResult` | `{ variablesToSet?: Record<string, unknown> }` |
| `src/handler/registry.ts` | `Handler` | `(ctx: JobContext) => Promise<HandlerResult>` |
| `src/handler/registry.ts` | `NonRetryableError` | Throw to bypass the retry budget |
| `src/runner/runner.ts` | `Runner(engine, registry, config)` | Poll/dispatch loop |
| `src/runner/runner.ts` | `RunnerConfig` | `{ workerId, concurrency?, pollIntervalMs? }` |
| `src/runner/runner.ts` | `Runner.run(signal)` | Async; resolves after drain, rejects if the shutdown deadline expires |

---

## How the runner works

`new HandlerRegistry()` + `registry.register(...)` just build a `jobType → Handler` map in memory — no connection, no I/O. The `Runner` is what drives everything:

1. An async loop calls `pollJobs`; the selected client uses REST or gRPC. Empty polls back off; `pollIntervalMs` controls the delay after a poll error.
2. The engine claims available jobs atomically with `FOR UPDATE SKIP LOCKED` and returns them.
3. Each poll requests only free slots. Handlers and pending callbacks share the `concurrency` limit; no leased jobs wait in an internal work queue.
4. The task calls your registered handler, then calls `completeJob` or `failJob` based on the result.

**Error handling**: throw a plain `Error` → `failJob(retryable=true)` → engine retries up to `retryCount`. Throw `NonRetryableError` → `failJob(retryable=false)` → fails immediately regardless of retry budget.

For the full model (sequence diagram, retry flow, graceful shutdown), see [architecture.md — Worker polling model](https://batnam.github.io/rochallor-engine/architecture/#worker-polling-model).

---

## Minimal example — REST transport

```typescript
import { RestEngineClient, HandlerRegistry, Runner } from 'rochallor-workflow-sdk-node'

const engine   = new RestEngineClient('http://localhost:8080')
const registry = new HandlerRegistry()

registry.register('process-order', async ctx => {
  const orderId = ctx.variables['orderId'] as string
  // ... process order ...
  return { variablesToSet: { processed: true, orderId } }
})

const controller = new AbortController()
process.on('SIGINT',  () => controller.abort())
process.on('SIGTERM', () => controller.abort())

const runner = new Runner(engine, registry, { workerId: 'node-worker-1' })
await runner.run(controller.signal)
```

---

## Full demo — multiple handlers, non-retryable errors, gRPC transport

```typescript
import { GrpcEngineClient, HandlerRegistry, NonRetryableError, Runner } from 'rochallor-workflow-sdk-node'

// Use gRPC transport — swap for new RestEngineClient('http://...') to use REST
const engine   = new GrpcEngineClient('localhost:9090') // local plaintext connection
const registry = new HandlerRegistry()

// Handler: validate-application
registry.register('validate-application', async ctx => {
  const applicantId = ctx.variables['applicantId'] as string | undefined
  if (!applicantId) {
    // NonRetryableError — engine will not retry regardless of retryCount
    throw new NonRetryableError('applicantId is required')
  }
  console.log(`Validating applicant ${applicantId} (retries left: ${ctx.retriesRemaining})`)
  // ... call validation service ...
  return {
    variablesToSet: {
      validationPassed: true,
      validatedAt: new Date().toISOString(),
    },
  }
})

// Handler: credit-score
// Any thrown Error (other than NonRetryableError) is treated as retryable.
registry.register('credit-score', async ctx => {
  const applicantId = ctx.variables['applicantId'] as string
  const score = await fetchCreditScore(applicantId)  // may throw on transient error
  return { variablesToSet: { creditScore: score } }
})

// Handler: send-notification (no output variables)
registry.register('send-notification', async ctx => {
  const email = ctx.variables['email'] as string
  console.log(`Sending notification to ${email}`)
  // ... send email ...
  return { variablesToSet: { notificationSent: true } }
})

const controller = new AbortController()
process.on('SIGINT',  () => controller.abort())
process.on('SIGTERM', () => controller.abort())

const runner = new Runner(engine, registry, {
  workerId:      'node-worker-1',
  concurrency:   32,    // parallel async dispatches
  pollIntervalMs: 250,
})

console.log('Worker starting')
await runner.run(controller.signal)
console.log('Worker stopped')

async function fetchCreditScore(_applicantId: string): Promise<number> {
  return 720  // placeholder
}
```

---

## Upload a definition from Node

```typescript
import { RestEngineClient } from 'rochallor-workflow-sdk-node'

const client = new RestEngineClient('http://localhost:8080')

// The Node client exposes pollJobs / completeJob / failJob (worker interface).
// Use the REST API directly for admin operations (upload, start instance, etc.):
const resp = await fetch('http://localhost:8080/v1/definitions', {
  method:  'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({
    id:    'greet-workflow',
    name:  'Greet Workflow',
    steps: [
      { id: 'say-hello', name: 'Say Hello', type: 'SERVICE_TASK',
        jobType: 'greet', nextStep: 'end' },
      { id: 'end', name: 'End', type: 'END' },
    ],
  }),
})
const definition = await resp.json()
console.log('Uploaded:', definition.id, 'v' + definition.version)
```

---

## Kafka Dispatch (Opt-In)

The Node.js SDK supports push-based job dispatch via Kafka for high-scale environments.

### Usage

```typescript
import { KafkaRunner } from 'rochallor-workflow-sdk-node'

const runner = new KafkaRunner(
  {
    workerId: 'node-worker-1',
    brokers: ['localhost:9092'],
  },
  engine,
  registry
)

await runner.start()
```

### Delivery and idempotent handlers

Kafka dispatch uses **at-least-once** delivery and commits each record only after the engine acknowledges its callback. An in-process dedup window (default 10 min) absorbs most duplicates transparently — but a handler **can** be invoked more than once for the same `jobId` when:

- The relay was down longer than `dedupWindowMs` before republishing.
- This runner restarted between the original message and a relay-republished duplicate.

**Handlers must be idempotent.** Use a stable business operation key from variables (for example, an invoice ID) for external side effects. `jobId` identifies one delivery attempt and changes on automatic retry or lease recovery; it only deduplicates redelivery of that same attempt.

```typescript
registry.register('send-invoice', async (job) => {
  const invoiceId = job.variables.invoiceId
  if (typeof invoiceId !== 'string') throw new NonRetryableError('invoiceId is required')
  if (await db.invoiceAlreadySent(invoiceId)) return {}
  return sendInvoiceToCustomer(job.variables, { idempotencyKey: invoiceId })
})
```

Enforce the operation key with a database unique constraint or pass it to the external service as its idempotency key. A check-before-send alone does not prevent concurrent duplicates.

The engine's `completeJob` / `failJob` calls are already idempotent — a second call with the same `jobId` is a no-op. Only your external side-effects need to be guarded.

---

### TLS / SASL (production)

The default config above connects to brokers in **plaintext with no authentication**. For any production Kafka cluster, enable TLS and SASL via the `ssl` and `sasl` fields. The type of `sasl` is `KafkaJS.SASLOptions` from `@confluentinc/kafka-javascript`, so all four standard mechanisms are supported.

```typescript
// SASL/SCRAM-SHA-512 over TLS — common for MSK, Confluent Cloud, Aiven, Redpanda
const runner = new KafkaRunner(
  {
    workerId: 'node-worker-1',
    brokers:  ['kafka-1.prod:9093', 'kafka-2.prod:9093'],
    ssl:      true,
    sasl: {
      mechanism: 'scram-sha-512',
      username:  process.env.KAFKA_SASL_USERNAME!,
      password:  process.env.KAFKA_SASL_PASSWORD!,
    },
  },
  engine,
  registry,
)
```

Other mechanisms:

```typescript
// SASL/PLAIN over TLS — only safe with ssl: true
sasl: { mechanism: 'plain',           username: '...', password: '...' }

// SCRAM-SHA-256
sasl: { mechanism: 'scram-sha-256',   username: '...', password: '...' }

// OAUTHBEARER (e.g. AWS MSK IAM, Azure Event Hubs)
sasl: {
  mechanism: 'oauthbearer',
  oauthBearerProvider: async () => ({
    value:     await fetchAccessToken(),
    principal: 'svc-account',
    lifetime:  900_000,
  }),
}
```

Plaintext brokers should be restricted to local dev (`docker compose`) — never deploy without `ssl: true` and a `sasl` mechanism on a shared network.

---

### KafkaRunner configuration reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `workerId` | string | *(required)* | Unique identifier for this worker. |
| `brokers` | string[] | *(required)* | Array of Kafka broker addresses. |
| `clientId` | string | `workerId` | Kafka client identifier. |
| `dedupWindowMs` | number | `600000` | Window (ms) for in-memory deduplication (default 10m). |
| `ssl` | boolean | `false` | Enable TLS for broker connections. Required with `sasl` in production. |
| `sasl` | `KafkaJS.SASLOptions` | *(none)* | SASL auth: `plain`, `scram-sha-256`, `scram-sha-512`, or `oauthbearer`. |

---

## Runner configuration reference (Polling Mode)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `workerId` | string | *(required)* | Unique identifier for this worker process. |
| `concurrency` | number | `64` | Maximum claimed jobs, including handlers and pending callbacks. |
| `pollIntervalMs` | number | `500` | Delay after empty polls and poll errors. |


## Polling callback recovery (R03)

Polling retains the handler result, failure reason and retryability while delivering a callback. A transport failure never becomes a handler failure. Retries use a 5-second attempt timeout and exponential backoff (100 ms, ±20% jitter, capped at 30 seconds), bounded by the engine's `lockExpiresAt` and shutdown signal. REST and gRPC both preserve the lease timestamp. Heartbeat is off by default.

An expired lease stops callback retries. A handler still running keeps its capacity slot until it settles; design handlers to finish within the lease and make external side effects idempotent. Shutdown stops claims/retries and bounds handler drain by `shutdownTimeoutMs`. Built-in clients honor cancellation; custom clients must honor the optional `CallOptions.signal` to bound I/O. Existing implementations with fewer method parameters remain source compatible. Without lease metadata the runner makes one callback attempt, avoiding retries under an invented lease; invalid/expired metadata prevents dispatch.

Pass a `Metrics` instance as the fourth Runner constructor argument to use your Prometheus registry. Monitor `workflow_sdk_callback_pending`, `workflow_sdk_callback_retries_total`, `workflow_sdk_callback_oldest_pending_seconds`, and `workflow_sdk_callback_rejected_total` with finite `dispatch`/`callback` labels. Job IDs appear in logs. If pending age rises, check transport/engine health and authorization. HTTP 4xx except 408/429 and permanent gRPC statuses stop delivery; the engine recovers the unacknowledged attempt through the lease. Drain and pin the previous worker build to roll back; older polling SDKs reintroduce A6/A7.

## Kafka callback and partition recovery (R04)

The consumer disables auto-commit and automatic batch resolution. Each partition pauses at its first record while the handler and callback run; the consumer keeps polling so membership/rebalance callbacks remain active. After acknowledgement it commits exactly that record's next offset, seeks there, and resumes. Later batch records cannot commit over a pending record. Retained outcomes do not expire with dedup TTL; confirmed dedup entries are written only after acknowledgement.

Partition revocation/shutdown cancels delivery and fences stale commits. An already submitted commit completes before the rebalance callback returns. Restart/reassignment can rerun a handler whose callback was unconfirmed; side effects must remain idempotent. Empty/malformed records, permanent callback rejection and commit failure leave the affected partition paused and uncommitted, with topic/partition/offset and job/dedup IDs in logs. Monitor callback metrics and `workflow_sdk_kafka_record_errors_total` alongside consumer lag. Repair payload/authorization/transport issues, then restart the worker; do not reset offsets to skip blocked work.

`KafkaRunner` accepts an optional fourth `Metrics` argument. Await `start()` before normal processing, and call `stop()` on shutdown. Drain/canary a worker group; rollback pins the previous artifact and can restore A1. There are no engine or database changes.

The behavior follows the [Confluent consumer API](https://docs.confluent.io/platform/current/clients/confluent-kafka-javascript/docs/KafkaJS.Consumer.html); librdkafka manages heartbeats as documented in the [migration guide](https://docs.confluent.io/kafka-clients/javascript/current/migration.html).


Keep handlers idempotent and reconcile incomplete workflow outcomes during a canary. If the previous npm artifact cannot be imported, roll back to the previously verified worker image rather than assuming that artifact is a usable fallback.

## Verify a package from this repository

Requires Node 24+, npm and `protoc` on PATH. From `workflow-sdk-node/`:

```sh
npm ci
npm run package:verify
```

This removes old generated/build output, regenerates protos, builds, runs unit tests and the production audit, then creates `artifacts/sdk.tgz`, `SHA256SUMS` and build metadata. A temporary consumer outside the repository installs only production dependencies, type-checks public imports and instantiates both clients and runners. Deliberately broken entry points, CommonJS output, generated imports and a missing protobuf runtime must all fail.

From the repository root, test the same tarball in four separate stacks:

```sh
for mode in polling kafka_outbox; do
  for transport in rest grpc; do
    WE_DISPATCH_MODE="$mode" ./e2e/run.sh --sdk=node --transport="$transport"
  done
done
```

The Node worker installs the checksummed tarball. CI prepares it once, runs all four combinations and allows the tag workflow to publish those same bytes only after verification succeeds. The weekly verification also refreshes the production audit.

## Optional polling heartbeat (1.1.0)

Heartbeat is off by default. After every engine replica supports `ExtendJobLease`
(engine candidate v1.2.0 / R15), enable it per runner:

```ts
const runner = new Runner(client, registry, {
  workerId: 'worker-1', concurrency: 16,
  heartbeat: true, leaseSeconds: 30, heartbeatIntervalMs: 10_000,
  shutdownTimeoutMs: 5000,
})
await runner.run(controller.signal)
```

`EngineClient` retains its three required methods; `LeaseExtender` is an optional
capability. The built-in REST/gRPC clients implement it. `PollJobsRequest` adds
optional `leaseSeconds`: omitted/0 uses 30 seconds, otherwise 5–3600 seconds.
Existing custom clients continue working with heartbeat off. Enabling heartbeat
without client/server support rejects `run()` with `HeartbeatUnsupportedError`
before running a handler. The first heartbeat must be accepted; subsequent renewals
continue through handler execution and immutable callback retries until ack.
Requests are bounded by 5 seconds and the current lease, with backoff on transient
errors. `LeaseLostError` stops the old attempt without a failure callback.

Polling handlers receive optional `ctx.signal`. Observe it to cancel external
work. A handler that ignores cancellation retains its slot until its promise
settles. Shutdown stops intake and waits at most `shutdownTimeoutMs` for handlers;
`run()` rejects if they remain, and the same runner cannot restart while they run.
Custom transports should honor `CallOptions.signal` to release network resources.

Heartbeat timers share Node's event loop. A synchronous CPU-bound handler can
prevent renewal and lose its lease; a regression test verifies that its stale
result is fenced. Offload that work to a worker thread/process or use a verified
longer lease. Heartbeat does not make side effects exactly-once: retain application
idempotency and handle process pauses as ownership loss.

Monitor `workflow_sdk_heartbeat_total{result="accepted|retry|lease_lost|rejected"}`,
callback pending/retry metrics and replacement attempts. Canary with heartbeat off,
then enable for a small worker group while measuring DB writes and latency. To
roll back, stop intake for tasks depending on renewal, drain them, then disable
heartbeat and pin the previous SDK. Do this before rolling back the engine.
