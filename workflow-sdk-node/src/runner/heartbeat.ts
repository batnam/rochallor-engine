import { setTimeout as wait } from 'node:timers/promises'
import { HeartbeatUnsupportedError, LeaseLostError, type Job, type LeaseExtender } from '../client/types.js'
import { Metrics } from '../metrics/metrics.js'
import { delay } from '../retry/backoff.js'
import { permanent } from './callback.js'

/** Bound waiting even when a custom client ignores transport cancellation. */
export function abortable<T>(pending: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    const abort = () => reject(signal.reason)
    if (signal.aborted) { pending.catch(() => {}); abort(); return }
    signal.addEventListener('abort', abort, { once: true })
    pending.then(resolve, reject).finally(() => signal.removeEventListener('abort', abort))
  })
}

export async function maintainLease(engine: LeaseExtender, job: Job, workerId: string, leaseSeconds: number,
  intervalMs: number, parent: AbortSignal, metrics: Metrics, fatal: (error: unknown) => void,
): Promise<{ signal: AbortSignal; valid: () => boolean; stop: () => Promise<void> }> {
  let expiry = Date.parse(job.lockExpiresAt ?? '')
  if (!Number.isFinite(expiry)) throw new Error('heartbeat requires lockExpiresAt lease metadata')
  if (expiry <= Date.now()) throw new LeaseLostError()
  const controller = new AbortController()
  const signal = AbortSignal.any([parent, controller.signal])
  let timer: ReturnType<typeof setTimeout> | undefined
  const armExpiry = () => {
    clearTimeout(timer)
    timer = setTimeout(() => {
      metrics.heartbeats.inc({ result: 'lease_lost' })
      controller.abort(new LeaseLostError())
    }, Math.max(1, expiry - Date.now()))
  }
  armExpiry()
  let ready!: () => void
  let rejectReady!: (error: unknown) => void
  const started = new Promise<void>((resolve, reject) => { ready = resolve; rejectReady = reject })
  const loop = (async () => {
    let attempt = 0
    try {
      while (!signal.aborted) {
        try {
          const callSignal = AbortSignal.any([signal, AbortSignal.timeout(Math.max(1, Math.min(5000, expiry - Date.now())))])
          // The request signal bounds cooperative transports to 5s. If a custom
          // client ignores it, wait until lease loss; never overlap renewals.
          const next = Date.parse(await abortable(engine.extendJobLease(job.id, workerId, leaseSeconds, { signal: callSignal }), signal))
          signal.throwIfAborted()
          if (Date.now() >= expiry) throw new LeaseLostError()
          if (!Number.isFinite(next) || next < expiry || next <= Date.now()) {
            const error = new Error('invalid heartbeat expiry')
            fatal(error)
            throw error
          }
          expiry = next
          armExpiry()
          metrics.heartbeats.inc({ result: 'accepted' })
          ready()
          attempt = 0
          await wait(Math.max(1, Math.min(intervalMs, (expiry - Date.now()) / 3)), undefined, { signal })
        } catch (error) {
          signal.throwIfAborted()
          if (error instanceof LeaseLostError) {
            metrics.heartbeats.inc({ result: 'lease_lost' })
            throw error
          }
          if (error instanceof HeartbeatUnsupportedError || permanent(error)) {
            metrics.heartbeats.inc({ result: 'rejected' })
            fatal(error)
            throw error
          }
          metrics.heartbeats.inc({ result: 'retry' })
          await wait(Math.max(1, Math.min(delay(++attempt), expiry - Date.now())), undefined, { signal })
        }
      }
    } catch (error) {
      controller.abort(error)
      rejectReady(error)
    } finally { clearTimeout(timer) }
  })()
  try { await abortable(started, signal) } catch (error) { controller.abort(error); await loop; throw error }
  return { signal, valid: () => !signal.aborted && Date.now() < expiry, stop: async () => { controller.abort(); await loop } }
}
