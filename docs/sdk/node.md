# Node / TypeScript SDK

**Package**: `@batnam/workflow-sdk`

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
| `src/runner/runner.ts` | `Runner.run(signal)` | Async; resolves when `signal.aborted` and in-flight jobs drain |

---

## How the runner works

`new HandlerRegistry()` + `registry.register(...)` just build a `jobType → Handler` map in memory — no connection, no I/O. The `Runner` is what drives everything:

1. A `setInterval` loop fires every `pollIntervalMs` (default 500 ms) and calls `POST /v1/jobs/poll`.
2. The engine claims available jobs atomically with `FOR UPDATE SKIP LOCKED` and returns them.
3. Each job is dispatched as an async task (bounded by `concurrency`, default 64 in-flight promises).
4. The task calls your registered handler, then calls `completeJob` or `failJob` based on the result.

**Error handling**: throw a plain `Error` → `failJob(retryable=true)` → engine retries up to `retryCount`. Throw `NonRetryableError` → `failJob(retryable=false)` → fails immediately regardless of retry budget.

For the full model (sequence diagram, retry flow, graceful shutdown), see [architecture.md — Worker polling model](../architecture.md#worker-polling-model).

---

## Minimal example — REST transport

```typescript
import { RestEngineClient } from './src/client/rest.js'
import { HandlerRegistry } from './src/handler/registry.js'
import { Runner } from './src/runner/runner.js'

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
import * as grpc from '@grpc/grpc-js'
import { GrpcEngineClient }  from './src/client/grpc.js'
import { HandlerRegistry, NonRetryableError } from './src/handler/registry.js'
import { Runner } from './src/runner/runner.js'

// Use gRPC transport — swap for new RestEngineClient('http://...') to use REST
const engine   = new GrpcEngineClient('localhost:9090', grpc.credentials.createInsecure())
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
import { RestEngineClient } from './src/client/rest.js'

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
import { KafkaRunner } from './src/runner/kafka_runner.js'

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

### KafkaRunner configuration reference

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `workerId` | string | *(required)* | Unique identifier for this worker. |
| `brokers` | string[] | *(required)* | Array of Kafka broker addresses. |
| `clientId` | string | `workerId` | Kafka client identifier. |
| `dedupWindowMs` | number | `600000` | Window (ms) for in-memory deduplication (default 10m). |

---

## Runner configuration reference (Polling Mode)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `workerId` | string | *(required)* | Unique identifier for this worker process. |
| `concurrency` | number | `64` | Maximum parallel in-flight async dispatches. |
| `pollIntervalMs` | number | `500` | Milliseconds to sleep between poll rounds when the queue is empty. |
