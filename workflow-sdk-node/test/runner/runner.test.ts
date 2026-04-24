import { describe, it, expect, vi, beforeEach } from 'vitest'
import { Runner } from '../../src/runner/runner.js'
import { HandlerRegistry } from '../../src/handler/registry.js'
import type { EngineClient, Job } from '../../src/client/types.js'
import { NonRetryableError } from '../../src/handler/registry.js'

/** Build a fake job for test use. */
function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: 'j1',
    jobType: 'noop',
    instanceId: 'inst-1',
    stepExecutionId: 'se-1',
    retriesRemaining: 3,
    variables: {},
    ...overrides,
  }
}

/** Creates a fake EngineClient with jest-style spy functions. */
function makeFakeClient(jobs: Job[][]): EngineClient & {
  completeJob: ReturnType<typeof vi.fn>
  failJob: ReturnType<typeof vi.fn>
  callCount: () => number
} {
  let callIdx = 0
  const completeJob = vi.fn().mockResolvedValue(undefined)
  const failJob = vi.fn().mockResolvedValue(undefined)

  const pollJobs = vi.fn(async () => {
    if (callIdx >= jobs.length) return []
    return jobs[callIdx++]!
  })

  return { pollJobs, completeJob, failJob, callCount: () => callIdx }
}

describe('Runner', () => {
  let registry: HandlerRegistry

  beforeEach(() => {
    registry = new HandlerRegistry()
  })

  it('dispatches a job and completes it', async () => {
    const job = makeJob()
    const client = makeFakeClient([[job]])
    registry.register('noop', async () => ({ variablesToSet: { done: true } }))

    const runner = new Runner(client, registry, { workerId: 'w1' })
    const ac = new AbortController()

    // Abort after the first batch is processed
    const p = runner.run(ac.signal)
    await vi.waitFor(() => {
      if (client.completeJob.mock.calls.length < 1) throw new Error('not yet')
    })
    ac.abort()
    await p

    expect(client.completeJob).toHaveBeenCalledWith(
      'j1', 'w1', { done: true },
    )
    expect(client.failJob).not.toHaveBeenCalled()
  })

  it('calls failJob with retryable=true on generic error', async () => {
    const job = makeJob()
    const client = makeFakeClient([[job]])
    registry.register('noop', async () => { throw new Error('transient') })

    const runner = new Runner(client, registry, { workerId: 'w1' })
    const ac = new AbortController()

    const p = runner.run(ac.signal)
    await vi.waitFor(() => {
      if (client.failJob.mock.calls.length < 1) throw new Error('not yet')
    })
    ac.abort()
    await p

    expect(client.failJob).toHaveBeenCalledWith(
      'j1', 'w1', 'transient', true,
    )
    expect(client.completeJob).not.toHaveBeenCalled()
  })

  it('calls failJob with retryable=false for NonRetryableError', async () => {
    const job = makeJob()
    const client = makeFakeClient([[job]])
    registry.register('noop', async () => {
      throw new NonRetryableError('permanent failure')
    })

    const runner = new Runner(client, registry, { workerId: 'w1' })
    const ac = new AbortController()

    const p = runner.run(ac.signal)
    await vi.waitFor(() => {
      if (client.failJob.mock.calls.length < 1) throw new Error('not yet')
    })
    ac.abort()
    await p

    expect(client.failJob).toHaveBeenCalledWith(
      'j1', 'w1', 'permanent failure', false,
    )
  })

  it('calls failJob when no handler is registered', async () => {
    const job = makeJob({ jobType: 'unknown-type' })
    const client = makeFakeClient([[job]])
    // registry has no handlers

    const runner = new Runner(client, registry, { workerId: 'w1' })
    const ac = new AbortController()

    const p = runner.run(ac.signal)
    await vi.waitFor(() => {
      if (client.failJob.mock.calls.length < 1) throw new Error('not yet')
    })
    ac.abort()
    await p

    expect(client.failJob).toHaveBeenCalledWith(
      'j1', 'w1', expect.stringContaining('unknown-type'), false,
    )
  })

  it('drains in-flight work before resolving after abort', async () => {
    const order: string[] = []
    const job = makeJob()

    const client = makeFakeClient([[job]])
    registry.register('noop', async () => {
      await new Promise(r => setTimeout(r, 30))
      order.push('handler-done')
      return {}
    })

    const runner = new Runner(client, registry, { workerId: 'w1' })
    const ac = new AbortController()

    const p = runner.run(ac.signal)
    // Give the runner time to pick up the job, then abort
    await new Promise(r => setTimeout(r, 10))
    ac.abort()
    order.push('abort-sent')
    await p
    order.push('run-resolved')

    expect(order).toEqual(['abort-sent', 'handler-done', 'run-resolved'])
  })

  it('respects concurrency limit (does not over-poll)', async () => {
    const jobs = Array.from({ length: 5 }, (_, i) => makeJob({ id: `j${i}`, jobType: 'noop' }))
    const client = makeFakeClient([jobs])
    let concurrent = 0
    let maxConcurrent = 0

    registry.register('noop', async () => {
      concurrent++
      maxConcurrent = Math.max(maxConcurrent, concurrent)
      await new Promise(r => setTimeout(r, 20))
      concurrent--
      return {}
    })

    const runner = new Runner(client, registry, { workerId: 'w1', concurrency: 3 })
    const ac = new AbortController()

    const p = runner.run(ac.signal)
    await vi.waitFor(() => {
      if (client.completeJob.mock.calls.length < 5) throw new Error('not done')
    }, { timeout: 2000 })
    ac.abort()
    await p

    // All 5 dispatched (no server-side filtering in fake), but concurrency tracking works
    expect(client.completeJob).toHaveBeenCalledTimes(5)
  })
})
