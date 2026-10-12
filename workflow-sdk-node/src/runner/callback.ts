import { setTimeout as wait } from 'node:timers/promises'
import { HttpError, type EngineClient, type Job } from '../client/types.js'
import { HandlerRegistry, NonRetryableError } from '../handler/registry.js'
import { Metrics } from '../metrics/metrics.js'
import { delay } from '../retry/backoff.js'

// Store serialized variables so a transport cannot mutate the next retry.
export type Outcome = { kind: 'complete'; variables: string } | { kind: 'fail'; message: string; retryable: boolean }

export async function execute(registry: HandlerRegistry, job: Job, signal?: AbortSignal): Promise<Outcome> {
  const handler = registry.get(job.jobType)
  if (!handler) return { kind: 'fail', message: `no handler registered for ${job.jobType}`, retryable: false }
  try {
    const result = await handler(HandlerRegistry.contextFrom(job, signal))
    return { kind: 'complete', variables: JSON.stringify(result.variablesToSet ?? {}) }
  } catch (err) {
    return { kind: 'fail', message: err instanceof Error ? err.message : String(err), retryable: !(err instanceof NonRetryableError) }
  }
}

export async function deliver(engine: EngineClient, workerId: string, jobId: string, outcome: Outcome,
  signal: AbortSignal, metrics: Metrics, mode: 'polling' | 'kafka', retry = true): Promise<void> {
  const finish = metrics.trackCallback(mode, outcome.kind)
  const started = Date.now()
  try {
    for (let attempt = 1; ; attempt++) {
      signal.throwIfAborted()
      const options = { signal: AbortSignal.any([signal, AbortSignal.timeout(5000)]) }
      try {
        if (outcome.kind === 'complete') await engine.completeJob(jobId, workerId, JSON.parse(outcome.variables), options)
        else await engine.failJob(jobId, workerId, outcome.message, outcome.retryable, options)
        signal.throwIfAborted()
        return
      } catch (err) {
        signal.throwIfAborted()
        if (permanent(err)) {
          metrics.callbackRejected.inc({ dispatch: mode, callback: outcome.kind })
          throw err
        }
        if (!retry) throw err
        metrics.callbackRetries.inc({ dispatch: mode, callback: outcome.kind })
        console.warn(`callback pending job_id=${jobId} callback=${outcome.kind} attempt=${attempt} age_ms=${Date.now() - started}`, err)
        await wait(delay(attempt), undefined, { signal })
      }
    }
  } finally { finish() }
}

export function permanent(err: unknown): boolean {
  if (err instanceof HttpError) return err.statusCode >= 400 && err.statusCode < 500 && ![408, 429].includes(err.statusCode)
  // gRPC status codes indicating a permanent request/authorization error.
  return typeof err === 'object' && err !== null && 'code' in err && [3, 5, 6, 7, 9, 11, 12, 15, 16].includes(Number(err.code))
}
