import type { EngineClient } from '../client/types.js'
import { HandlerRegistry, NonRetryableError } from '../handler/registry.js'
import { sleep } from '../retry/backoff.js'

export interface RunnerConfig {
  workerId: string
  concurrency?: number       // default 64
  pollIntervalMs?: number    // default 500
}

/**
 * Async poll/lock/dispatch loop for the Node/TS SDK.
 * Stop via AbortController.signal.
 */
export class Runner {
  private readonly cfg: Required<RunnerConfig>

  constructor(
    private readonly engine: EngineClient,
    private readonly registry: HandlerRegistry,
    cfg: RunnerConfig,
  ) {
    this.cfg = {
      workerId: cfg.workerId,
      concurrency: cfg.concurrency ?? 64,
      pollIntervalMs: cfg.pollIntervalMs ?? 500,
    }
  }

  /** Runs until signal.aborted or the Promise is rejected. */
  async run(signal: AbortSignal): Promise<void> {
    const jobTypes = this.registry.jobTypes()
    const inFlight = new Set<Promise<void>>()

    console.info(`runner: starting worker_id=${this.cfg.workerId} job_types=${JSON.stringify(jobTypes)} concurrency=${this.cfg.concurrency}`)

    while (!signal.aborted) {
      try {
        const jobs = await this.engine.pollJobs({
          workerId: this.cfg.workerId,
          jobTypes,
          maxJobs: this.cfg.concurrency,
        })

        if (jobs.length === 0) {
          await sleep(1) // backoff 1st attempt
          continue
        }

        for (const job of jobs) {
          const p = this.dispatch(job).finally(() => inFlight.delete(p))
          inFlight.add(p)
        }

        // Yield to allow in-flight handlers to make progress
        await Promise.resolve()
      } catch (err) {
        if (signal.aborted) break
        console.warn('runner: poll error —', err)
        await new Promise(r => setTimeout(r, this.cfg.pollIntervalMs))
      }
    }

    // Drain in-flight
    console.info(`runner: draining ${inFlight.size} in-flight jobs`)
    await Promise.allSettled([...inFlight])
    console.info('runner: stopped')
  }

  private async dispatch(job: import('../client/types.js').Job): Promise<void> {
    const h = this.registry.get(job.jobType)
    if (!h) {
      console.error(`runner: no handler for job_type=${job.jobType} job_id=${job.id}`)
      await this.engine.failJob(
        job.id, this.cfg.workerId,
        `no handler registered for ${job.jobType}`,
        false,
      )
      return
    }

    console.info(`runner: dispatching job_id=${job.id} job_type=${job.jobType}`)
    try {
      const ctx = HandlerRegistry.contextFrom(job)
      const result = await h(ctx)
      console.info(`runner: job completed job_id=${job.id}`)
      await this.engine.completeJob(job.id, this.cfg.workerId, result.variablesToSet)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      const retryable = !(err instanceof NonRetryableError)
      if (retryable) {
        console.warn(`runner: job failed (retryable) job_id=${job.id}:`, err)
      } else {
        console.warn(`runner: job failed (non-retryable) job_id=${job.id}:`, err)
      }
      await this.engine.failJob(job.id, this.cfg.workerId, msg, retryable)
    }
  }
}
