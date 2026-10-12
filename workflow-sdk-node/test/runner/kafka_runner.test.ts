import { describe, it, expect, vi } from 'vitest'
import { KafkaRunner } from '../../src/runner/kafka_runner.js'
import { HandlerRegistry } from '../../src/handler/registry.js'
import { JobDispatchEvent } from '../../src/generated/workflow/v1/engine.js'

const kafka = vi.hoisted(() => ({ consumer: vi.fn() }))
vi.mock('@confluentinc/kafka-javascript', () => ({ KafkaJS: { Kafka: vi.fn(() => kafka), PartitionAssigners: { cooperativeSticky: 'cooperative-sticky' }, ErrorCodes: { ERR__ASSIGN_PARTITIONS: -175, ERR__REVOKE_PARTITIONS: -174 } } }))
const event = (jobId = 'j1') => Buffer.from(JobDispatchEvent.encode(JobDispatchEvent.fromPartial({ jobId, jobType: 'test', dedupId: jobId, instanceId: 'i1', jobPayload: Buffer.from('{}') })).finish())

it('retries the same completion before confirming dedup', async () => {
  const registry = new HandlerRegistry()
  const handler = vi.fn(async () => ({ variablesToSet: { result: 7 } }))
  registry.register('test', handler)
  const engine = { pollJobs: vi.fn(), completeJob: vi.fn().mockRejectedValueOnce(new Error('lost response')).mockResolvedValue(undefined), failJob: vi.fn() }
  const runner = new KafkaRunner({ workerId: 'w', brokers: [], dedupWindowMs: 10 }, engine, registry)
  try {
    await (runner as any).dispatch(event(), new AbortController().signal)
    expect(engine.completeJob).toHaveBeenCalledTimes(2)
    expect(engine.failJob).not.toHaveBeenCalled()
    expect(handler).toHaveBeenCalledTimes(1)
  } finally { await runner.stop() }
})

describe('Kafka offset ownership', () => {
  async function setup() {
    let config: any, run: any
    const consumer = { connect: vi.fn(), subscribe: vi.fn(), disconnect: vi.fn(), commitOffsets: vi.fn().mockResolvedValue(undefined), seek: vi.fn(), resume: vi.fn(), run: vi.fn(async value => { run = value }) }
    kafka.consumer.mockImplementation(value => { config = value; return consumer })
    const registry = new HandlerRegistry()
    const handler = vi.fn(async () => ({ variablesToSet: { frozen: 3 } }))
    registry.register('test', handler)
    const engine = { pollJobs: vi.fn(), completeJob: vi.fn().mockResolvedValue(undefined), failJob: vi.fn().mockResolvedValue(undefined) }
    const runner = new KafkaRunner({ workerId: 'w', brokers: [], dedupWindowMs: 10 }, engine, registry)
    await runner.start()
    await config.rebalance_cb({ code: -175 }, [{ partition: 0 }])
    const payload = { batch: { topic: 'workflow.jobs.test', partition: 0, messages: [{ offset: '10', value: event() }, { offset: '11', value: event('j2') }] }, isStale: () => false, isRunning: () => true, pause: vi.fn(), resolveOffset: vi.fn() }
    return { config, run, consumer, runner, engine, handler, payload }
  }

  it('disables automatic commits/resolution and commits only the acknowledged record', async () => {
    const s = await setup()
    s.engine.completeJob.mockRejectedValueOnce(new Error('unavailable'))
    try {
      expect(s.config.kafkaJS.autoCommit).toBe(false)
      expect(s.config['enable.auto.offset.store']).toBeUndefined()
      expect(s.run.eachBatchAutoResolve).toBe(false)
      await s.run.eachBatch(s.payload)
      expect(s.consumer.commitOffsets).not.toHaveBeenCalled()
      await vi.waitFor(() => expect(s.consumer.commitOffsets).toHaveBeenCalledWith([{ topic: 'workflow.jobs.test', partition: 0, offset: '11' }]))
      expect(s.handler).toHaveBeenCalledTimes(1)
      expect(s.engine.completeJob).toHaveBeenCalledTimes(2)
      expect(s.engine.failJob).not.toHaveBeenCalled()
      expect(s.payload.resolveOffset).not.toHaveBeenCalled()
      expect(s.consumer.seek).toHaveBeenCalledWith({ topic: 'workflow.jobs.test', partition: 0, offset: '11' })
    } finally { await s.runner.stop() }
  })

  it('cancels a revoked owner without committing, then redelivers after reassignment', async () => {
    const s = await setup()
    s.engine.completeJob.mockRejectedValue(new Error('unavailable'))
    try {
      await s.run.eachBatch(s.payload)
      await vi.waitFor(() => expect(s.engine.completeJob).toHaveBeenCalledTimes(1))
      await s.config.rebalance_cb({ code: -174 }, [{ partition: 0 }])
      await new Promise(resolve => setTimeout(resolve, 150))
      expect(s.consumer.commitOffsets).not.toHaveBeenCalled()
      expect(s.consumer.resume).not.toHaveBeenCalled()
      s.engine.completeJob.mockResolvedValue(undefined)
      await s.config.rebalance_cb({ code: -175 }, [{ partition: 0 }])
      await s.run.eachBatch(s.payload)
      await vi.waitFor(() => expect(s.consumer.commitOffsets).toHaveBeenCalledTimes(1))
      expect(s.handler).toHaveBeenCalledTimes(2)
    } finally { await s.runner.stop() }
  })

  it.each(['poison', 'permanent', 'shutdown'])('leaves %s pending without skipping a later record', async kind => {
    const s = await setup()
    if (kind === 'poison') s.payload.batch.messages[0].value = Buffer.from([0xff])
    if (kind === 'permanent') s.engine.completeJob.mockRejectedValue(Object.assign(new Error('denied'), { code: 7 }))
    if (kind === 'shutdown') s.engine.completeJob.mockRejectedValue(new Error('unavailable'))
    try {
      await s.run.eachBatch(s.payload)
      await new Promise(resolve => setTimeout(resolve, 30))
      await s.runner.stop()
      expect(s.consumer.commitOffsets).not.toHaveBeenCalled()
      expect(s.consumer.resume).not.toHaveBeenCalled()
      expect(s.handler.mock.calls.length).toBeLessThanOrEqual(1)
      expect(s.engine.failJob).not.toHaveBeenCalled()
    } finally { await s.runner.stop() }
  })
})

it('retries a business failure without changing its outcome and deduplicates only after acknowledgement', async () => {
  const registry = new HandlerRegistry()
  const handler = vi.fn(async () => { throw new Error('business') })
  registry.register('test', handler)
  const engine = { pollJobs: vi.fn(), completeJob: vi.fn(), failJob: vi.fn().mockRejectedValueOnce(new Error('lost response')).mockResolvedValue(undefined) }
  const runner = new KafkaRunner({ workerId: 'w', brokers: [] }, engine, registry)
  try {
    await (runner as any).dispatch(event(), new AbortController().signal)
    await (runner as any).dispatch(event(), new AbortController().signal)
    expect(handler).toHaveBeenCalledTimes(1)
    expect(engine.failJob).toHaveBeenCalledTimes(2)
    expect(engine.failJob.mock.calls.every(call => call[2] === 'business' && call[3] === true)).toBe(true)
    expect(engine.completeJob).not.toHaveBeenCalled()
  } finally { await runner.stop() }
})

it('reports invalid payload/missing handler as a permanent failure and retries its delivery', async () => {
  const registry = new HandlerRegistry()
  const engine = { pollJobs: vi.fn(), completeJob: vi.fn(), failJob: vi.fn().mockRejectedValueOnce(new Error('unavailable')).mockResolvedValue(undefined) }
  const runner = new KafkaRunner({ workerId: 'w', brokers: [] }, engine, registry)
  try {
    for (const payload of [Buffer.from('[]'), Buffer.from('{}')]) {
      const data = Buffer.from(JobDispatchEvent.encode(JobDispatchEvent.fromPartial({ jobId: 'j1', jobType: 'missing', jobPayload: payload })).finish())
      await (runner as any).dispatch(data, new AbortController().signal)
    }
    expect(engine.failJob).toHaveBeenCalledTimes(3)
    expect(engine.failJob.mock.calls.every(call => call[3] === false)).toBe(true)
    expect(engine.completeJob).not.toHaveBeenCalled()
  } finally { await runner.stop() }
})
