import { afterEach, expect, it, vi } from 'vitest'
import { Runner } from '../../src/runner/runner.js'
import { HandlerRegistry } from '../../src/handler/registry.js'
import type { EngineClient, Job, LeaseExtender } from '../../src/client/types.js'

const pause = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms))
afterEach(() => vi.restoreAllMocks())

function setup() {
  const job: Job = { id: 'job', instanceId: 'instance', stepExecutionId: 'step', jobType: 'work', retriesRemaining: 2,
    lockExpiresAt: new Date(Date.now() + 150).toISOString() }
  const engine: EngineClient & LeaseExtender = {
    pollJobs: vi.fn().mockResolvedValueOnce([job]).mockResolvedValue([]),
    completeJob: vi.fn().mockResolvedValue(undefined), failJob: vi.fn().mockResolvedValue(undefined),
    extendJobLease: vi.fn(async () => new Date(Date.now() + 150).toISOString()),
  }
  const registry = new HandlerRegistry()
  const controller = new AbortController()
  const runner = new Runner(engine, registry, { workerId: 'worker', concurrency: 1, pollIntervalMs: 5,
    heartbeat: true, heartbeatIntervalMs: 20, shutdownTimeoutMs: 50 })
  return { engine, registry, controller, runner }
}

it('renews throughout a handler and pending immutable completion without consuming retry budget', async () => {
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  const { engine, registry, controller, runner } = setup()
  const handler = vi.fn(async () => { await pause(250); return { variablesToSet: { frozen: 1 } } })
  registry.register('work', handler)
  const payloads: unknown[] = []
  const releaseAt = Date.now() + 430
  vi.mocked(engine.completeJob).mockImplementation(async (_id, _worker, variables) => {
    payloads.push(structuredClone(variables))
    variables!.frozen = 9
    if (Date.now() < releaseAt) throw new Error('unavailable')
    controller.abort()
  })
  const run = runner.run(controller.signal)
  await run
  expect(handler).toHaveBeenCalledTimes(1)
  expect(vi.mocked(engine.extendJobLease).mock.calls.length).toBeGreaterThanOrEqual(10)
  expect(payloads.length).toBeGreaterThan(1)
  expect(payloads.every(value => JSON.stringify(value) === '{"frozen":1}')).toBe(true)
  expect(engine.failJob).not.toHaveBeenCalled()
})

it('bounds shutdown while retaining an uncooperative handler slot and preventing restart', async () => {
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  const { engine, registry, controller, runner } = setup()
  let finish!: () => void
  let owned: AbortSignal | undefined
  registry.register('work', async context => {
    owned = context.signal
    await new Promise<void>(resolve => { finish = resolve })
    return {}
  })
  const run = runner.run(controller.signal)
  const settled = expect(run).rejects.toThrow('shutdown timed out')
  await vi.waitFor(() => expect(finish).toBeTypeOf('function'))
  controller.abort()
  await settled
  expect(owned?.aborted).toBe(true)
  expect(engine.pollJobs).toHaveBeenCalledTimes(1)
  await expect(runner.run(new AbortController().signal)).rejects.toThrow('already running')
  finish()
  await pause(10)
  expect(engine.completeJob).not.toHaveBeenCalled()
  expect(engine.failJob).not.toHaveBeenCalled()
})

it('refuses heartbeat on a legacy custom client while preserving heartbeat-off compatibility', async () => {
  const engine: EngineClient = { pollJobs: async () => [], completeJob: async () => {}, failJob: async () => {} }
  const controller = new AbortController()
  controller.abort()
  await new Runner(engine, new HandlerRegistry(), { workerId: 'w' }).run(controller.signal)
  await expect(new Runner(engine, new HandlerRegistry(), { workerId: 'w', heartbeat: true }).run(controller.signal))
    .rejects.toThrow('extendJobLease')
})

it('does not start a handler before an unsupported engine is reported', async () => {
  const { engine, registry, controller, runner } = setup()
  const { HeartbeatUnsupportedError } = await import('../../src/client/types.js')
  vi.mocked(engine.extendJobLease).mockRejectedValue(new HeartbeatUnsupportedError())
  const handler = vi.fn(async () => ({}))
  registry.register('work', handler)
  await expect(runner.run(controller.signal)).rejects.toThrow('heartbeat')
  expect(handler).not.toHaveBeenCalled()
  expect(engine.failJob).not.toHaveBeenCalled()
})

it('expires ownership even when a custom heartbeat transport ignores cancellation', async () => {
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  const { engine, registry, controller, runner } = setup()
  vi.mocked(engine.extendJobLease).mockImplementation(() => new Promise(() => {}))
  const handler = vi.fn(async () => ({}))
  registry.register('work', handler)
  const run = runner.run(controller.signal)
  await pause(220)
  controller.abort()
  await run
  expect(handler).not.toHaveBeenCalled()
  expect(engine.extendJobLease).toHaveBeenCalledTimes(1)
  expect(engine.failJob).not.toHaveBeenCalled()
})

it('keeps a lease-lost handler in capacity until it settles and sends no failure callback', async () => {
  const { LeaseLostError } = await import('../../src/client/types.js')
  const { engine, registry, controller, runner } = setup()
  vi.mocked(engine.extendJobLease).mockResolvedValueOnce(new Date(Date.now() + 150).toISOString()).mockRejectedValue(new LeaseLostError())
  let finish!: () => void
  let owned: AbortSignal | undefined
  registry.register('work', async context => {
    owned = context.signal
    await new Promise<void>(resolve => { finish = resolve })
    return {}
  })
  const run = runner.run(controller.signal)
  await vi.waitFor(() => expect(owned?.aborted).toBe(true))
  expect(engine.pollJobs).toHaveBeenCalledTimes(1)
  controller.abort(); finish(); await run
  expect(engine.completeJob).not.toHaveBeenCalled()
  expect(engine.failJob).not.toHaveBeenCalled()
})

it('fences a CPU-bound handler after it blocks the event loop past the lease', async () => {
  const { engine, registry, controller, runner } = setup()
  registry.register('work', async () => {
    const until = Date.now() + 200
    while (Date.now() < until) { /* Deliberately starve heartbeat timers. */ }
    setTimeout(() => controller.abort(), 10)
    return { variablesToSet: { stale: true } }
  })
  await runner.run(controller.signal)
  expect(engine.extendJobLease).toHaveBeenCalledTimes(1)
  expect(engine.completeJob).not.toHaveBeenCalled()
  expect(engine.failJob).not.toHaveBeenCalled()
})

it.each([{ leaseSeconds: 4 }, { leaseSeconds: 3601 }, { heartbeatIntervalMs: 10001 }, { shutdownTimeoutMs: 0 }, { shutdownTimeoutMs: Infinity }])
  ('rejects invalid heartbeat configuration %j', config => {
    const { engine, registry } = setup()
    expect(() => new Runner(engine, registry, { workerId: 'w', heartbeat: true, ...config })).toThrow()
  })
