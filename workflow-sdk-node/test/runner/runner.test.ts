import { describe, it, expect, vi, beforeEach } from 'vitest'
import { Runner } from '../../src/runner/runner.js'
import { HandlerRegistry } from '../../src/handler/registry.js'
import type { EngineClient, Job, PollJobsRequest } from '../../src/client/types.js'
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

  const pollJobs = vi.fn(async (req: PollJobsRequest) => {
    if (callIdx >= jobs.length) return []
    const batch = jobs[callIdx]!
    const result = batch.splice(0, req.maxJobs)
    if (!batch.length) callIdx++
    return result
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
      'j1', 'w1', { done: true }, expect.objectContaining({ signal: expect.any(AbortSignal) }),
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
      'j1', 'w1', 'transient', true, expect.any(Object),
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
      'j1', 'w1', 'permanent failure', false, expect.any(Object),
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
      'j1', 'w1', expect.stringContaining('unknown-type'), false, expect.any(Object),
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
    const client = makeFakeClient([jobs.slice(0, 3), jobs.slice(3)])
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

    expect(client.completeJob).toHaveBeenCalledTimes(5)
    expect(maxConcurrent).toBeLessThanOrEqual(3)
  })

  it('retries completion with the same result without running the handler or failure callback again', async () => {
    const job = { ...makeJob(), lockExpiresAt: new Date(Date.now() + 3000).toISOString() }
    const client = makeFakeClient([[job]])
    const handler = vi.fn(async () => ({ variablesToSet: { done: true } }))
    registry.register('noop', handler)
    client.completeJob.mockRejectedValueOnce(new Error('lost response')).mockResolvedValue(undefined)
    const controller = new AbortController()
    const running = new Runner(client, registry, { workerId: 'w1', concurrency: 1 }).run(controller.signal)
    try {
      await vi.waitFor(() => expect(client.completeJob).toHaveBeenCalledTimes(2), { timeout: 1000 })
      expect(handler).toHaveBeenCalledTimes(1)
      expect(client.failJob).not.toHaveBeenCalled()
      expect(client.completeJob.mock.calls[0].slice(0, 3)).toEqual(client.completeJob.mock.calls[1].slice(0, 3))
    } finally {
      controller.abort()
      await running
    }
  })
})

describe('polling ownership', () => {
  it('does not start a handler when shutdown happens during polling', async () => {
    const controller = new AbortController()
    const client = makeFakeClient([])
    client.pollJobs = async () => { controller.abort(); return [makeJob()] }
    const registry = new HandlerRegistry()
    const handler = vi.fn(async () => ({}))
    registry.register('noop', handler)
    await new Runner(client, registry, { workerId: 'w' }).run(controller.signal)
    expect(handler).not.toHaveBeenCalled()
    expect(client.completeJob).not.toHaveBeenCalled()
    expect(client.failJob).not.toHaveBeenCalled()
  })

  it('checks the lease after a handler even before the timeout signal is delivered', async () => {
    const now = Date.now(), deadline = now + 30000
    const clock = vi.spyOn(Date, 'now').mockReturnValue(now)
    const controller = new AbortController()
    const client = makeFakeClient([])
    let polls = 0
    client.pollJobs = async () => {
      if (++polls === 1) return [makeJob({ lockExpiresAt: new Date(deadline).toISOString() })]
      controller.abort(); return []
    }
    const registry = new HandlerRegistry()
    registry.register('noop', async () => { clock.mockReturnValue(deadline + 1); return {} })
    try {
      await new Runner(client, registry, { workerId: 'w', concurrency: 1 }).run(controller.signal)
      expect(client.completeJob).not.toHaveBeenCalled()
      expect(client.failJob).not.toHaveBeenCalled()
    } finally { clock.mockRestore() }
  })

  it('keeps pending callbacks in capacity and stops retrying at the lease deadline', async () => {
    const deadline = Date.now() + 350
    const client = makeFakeClient([[makeJob({ lockExpiresAt: new Date(deadline).toISOString() })], [makeJob({ id: 'j2' })]])
    const registry = new HandlerRegistry()
    const handler = vi.fn(async () => ({ variablesToSet: { frozen: 1 } }))
    registry.register('noop', handler)
    const times: number[] = []
    client.completeJob.mockImplementation(async (id: string, _worker: string, vars: Record<string, unknown>) => {
      if (id === 'j1') { times.push(Date.now()); expect(vars.frozen).toBe(1); vars.frozen = 9; throw new Error('unavailable') }
    })
    const controller = new AbortController()
    const run = new Runner(client, registry, { workerId: 'w', concurrency: 1, pollIntervalMs: 5 }).run(controller.signal)
    try {
      await vi.waitFor(() => expect(client.callCount()).toBe(2))
      expect(times.length).toBeGreaterThan(1)
      expect(times.every(time => time < deadline)).toBe(true)
      expect(handler).toHaveBeenCalledTimes(2)
      expect(client.failJob).not.toHaveBeenCalled()
    } finally { controller.abort(); await run }
  })

  it('retains a slow handler slot beyond lease expiry and sends no stale completion', async () => {
    const client = makeFakeClient([[makeJob({ lockExpiresAt: new Date(Date.now() + 20).toISOString() })], [makeJob({ id: 'j2' })]])
    const registry = new HandlerRegistry()
    let finish!: () => void
    registry.register('noop', async () => { await new Promise<void>(resolve => { finish = resolve }); return {} })
    const controller = new AbortController()
    const run = new Runner(client, registry, { workerId: 'w', concurrency: 1 }).run(controller.signal)
    await vi.waitFor(() => expect(finish).toBeTypeOf('function'))
    await new Promise(resolve => setTimeout(resolve, 30))
    expect(client.callCount()).toBe(1)
    controller.abort(); finish(); await run
    expect(client.completeJob).not.toHaveBeenCalled()
    expect(client.failJob).not.toHaveBeenCalled()
  })

  it('makes only one callback attempt for a legacy custom client without a lease', async () => {
    const client = makeFakeClient([[makeJob()]])
    const registry = new HandlerRegistry()
    registry.register('noop', async () => ({}))
    client.completeJob.mockRejectedValue(new Error('unavailable'))
    const controller = new AbortController()
    const run = new Runner(client, registry, { workerId: 'w' }).run(controller.signal)
    await vi.waitFor(() => expect(client.completeJob).toHaveBeenCalledTimes(1))
    controller.abort(); await run
    expect(client.completeJob).toHaveBeenCalledTimes(1)
    expect(client.failJob).not.toHaveBeenCalled()
  })

  it.each([false, true])('retains failure retryability=%s and releases pending metrics on shutdown', async retryable => {
    const client = makeFakeClient([[makeJob({ lockExpiresAt: new Date(Date.now() + 3000).toISOString() })]])
    const registry = new HandlerRegistry()
    const handler = vi.fn(async () => { throw retryable ? new Error('business') : new NonRetryableError('business') })
    registry.register('noop', handler)
    client.failJob.mockRejectedValue(new Error('unavailable'))
    const controller = new AbortController()
    const runner = new Runner(client, registry, { workerId: 'w', concurrency: 1 })
    const run = runner.run(controller.signal)
    try {
      await vi.waitFor(() => expect(client.failJob.mock.calls.length).toBeGreaterThanOrEqual(2))
      expect(client.failJob.mock.calls.every(call => call[2] === 'business' && call[3] === retryable)).toBe(true)
      expect(handler).toHaveBeenCalledTimes(1)
    } finally { controller.abort(); await run }
    expect((await runner.metrics.callbackPending.get()).values.every(value => value.value === 0)).toBe(true)
    expect(client.completeJob).not.toHaveBeenCalled()
  })
})
