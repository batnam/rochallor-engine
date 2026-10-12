import { setTimeout as wait } from 'node:timers/promises'
import { HeartbeatUnsupportedError, LeaseLostError, type EngineClient, type Job, type LeaseExtender } from '../client/types.js'
import { HandlerRegistry } from '../handler/registry.js'
import { Metrics } from '../metrics/metrics.js'
import { deliver, execute } from './callback.js'
import { abortable, maintainLease } from './heartbeat.js'

export interface RunnerConfig {
  workerId: string
  concurrency?: number       // default 64
  pollIntervalMs?: number    // default 500
  leaseSeconds?: number     // default engine lease (30s)
  heartbeat?: boolean       // default false
  heartbeatIntervalMs?: number // default one third of the lease
  shutdownTimeoutMs?: number   // default 5000
}

/** Async poll/lock/dispatch loop. Stop via AbortController.signal. */
export class Runner {
  private readonly cfg: Required<RunnerConfig>
  private running = false

  constructor(
    private readonly engine: EngineClient,
    private readonly registry: HandlerRegistry,
    cfg: RunnerConfig,
    readonly metrics = new Metrics(),
  ) {
    this.cfg = { workerId: cfg.workerId, concurrency: cfg.concurrency ?? 64, pollIntervalMs: cfg.pollIntervalMs ?? 500,
      leaseSeconds: cfg.leaseSeconds ?? 0, heartbeat: cfg.heartbeat ?? false,
      heartbeatIntervalMs: cfg.heartbeatIntervalMs ?? ((cfg.leaseSeconds || 30) * 1000 / 3),
      shutdownTimeoutMs: cfg.shutdownTimeoutMs ?? 5000 }
    if (!Number.isInteger(this.cfg.leaseSeconds) || (this.cfg.leaseSeconds !== 0 && (this.cfg.leaseSeconds < 5 || this.cfg.leaseSeconds > 3600))) throw new Error('leaseSeconds must be 0 or 5–3600')
    if (!(this.cfg.heartbeatIntervalMs > 0) || this.cfg.heartbeatIntervalMs > (this.cfg.leaseSeconds || 30) * 1000 / 3) throw new Error('heartbeatIntervalMs must be positive and at most one third of the lease')
    if (!Number.isFinite(this.cfg.shutdownTimeoutMs) || !(this.cfg.shutdownTimeoutMs > 0)) throw new Error('shutdownTimeoutMs must be finite and positive')
    if (!Number.isInteger(this.cfg.concurrency) || this.cfg.concurrency < 1) throw new Error('concurrency must be a positive integer')
  }

  async run(parent: AbortSignal): Promise<void> {
    if (this.running) throw new Error('runner already running or draining handlers')
    const extender = this.engine as EngineClient & Partial<LeaseExtender>
    if (this.cfg.heartbeat && typeof extender.extendJobLease !== 'function') throw new HeartbeatUnsupportedError()
    this.running = true
    const controller = new AbortController()
    const signal = AbortSignal.any([parent, controller.signal])
    let fatalError: unknown
    const fatal = (error: unknown) => { fatalError ??= error; controller.abort(error) }
    const jobTypes = this.registry.jobTypes()
    const inFlight = new Set<Promise<void>>()
    try {
      while (!signal.aborted) {
        if (inFlight.size >= this.cfg.concurrency) {
          await abortable(Promise.race(inFlight), signal)
          continue
        }
        try {
          const jobs = await this.engine.pollJobs({ workerId: this.cfg.workerId, jobTypes, maxJobs: this.cfg.concurrency - inFlight.size, leaseSeconds: this.cfg.leaseSeconds },
            { signal: AbortSignal.any([signal, AbortSignal.timeout(5000)]) })
          for (const job of jobs) {
            const pending = this.dispatch(job, signal, fatal)
              .catch(err => console.warn(`runner: unacknowledged job_id=${job.id}`, err))
              .finally(() => inFlight.delete(pending))
            inFlight.add(pending)
          }
          if (!jobs.length) await wait(this.cfg.pollIntervalMs, undefined, { signal })
        } catch (err) {
          if (signal.aborted) break
          console.warn('runner: poll error', err)
          await wait(this.cfg.pollIntervalMs, undefined, { signal })
        }
      }
    } catch (err) {
      if (!signal.aborted) throw err
    } finally {
      controller.abort()
      // A timed-out drain does not free slots or permit another run on this runner.
      const drained = Promise.allSettled(inFlight).then(() => { this.running = false })
      const timeout = new AbortController()
      const timer = setTimeout(() => timeout.abort(new Error('runner shutdown timed out; handlers still running')), this.cfg.shutdownTimeoutMs)
      try { await abortable(drained, timeout.signal) }
      catch (error) { fatalError ??= error }
      finally { clearTimeout(timer) }
    }
    if (fatalError) throw fatalError
  }

  private async dispatch(job: Job, signal: AbortSignal, fatal: (error: unknown) => void): Promise<void> {
    if (signal.aborted) return
    const deadline = job.lockExpiresAt === undefined ? undefined : Date.parse(job.lockExpiresAt)
    if (deadline !== undefined && (!Number.isFinite(deadline) || deadline <= Date.now())) return
    let lease: Awaited<ReturnType<typeof maintainLease>> | undefined
    if (this.cfg.heartbeat) {
      try {
        lease = await maintainLease(this.engine as EngineClient & LeaseExtender, job, this.cfg.workerId,
          this.cfg.leaseSeconds, this.cfg.heartbeatIntervalMs, signal, this.metrics, fatal)
      } catch (error) {
        if (!signal.aborted && !(error instanceof LeaseLostError)) fatal(error)
        return
      }
    }
    const owned = lease?.signal ?? (deadline === undefined ? signal : AbortSignal.any([signal, AbortSignal.timeout(Math.max(1, deadline - Date.now()))]))
    try {
      const outcome = await execute(this.registry, job, owned)
      if (owned.aborted || (lease ? !lease.valid() : deadline !== undefined && deadline <= Date.now())) return
      // Legacy custom clients without lease metadata receive one bounded attempt.
      await deliver(this.engine, this.cfg.workerId, job.id, outcome, owned, this.metrics, 'polling', deadline !== undefined)
    } finally { await lease?.stop() }
  }
}
