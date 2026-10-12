import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { MockAgent, setGlobalDispatcher, getGlobalDispatcher, type Dispatcher } from 'undici'
import { RestEngineClient } from '../../src/client/rest.js'
import type { Job } from '../../src/client/types.js'
import { HeartbeatUnsupportedError, HttpError, LeaseLostError } from '../../src/client/types.js'

const BASE = 'http://engine.test'
const HOST = 'http://engine.test'

const JOB: Job = {
  id: 'job-1',
  lockExpiresAt: '2026-10-11T00:00:30.000Z',
  jobType: 'send-email',
  instanceId: 'inst-1',
  stepExecutionId: 'se-1',
  retriesRemaining: 3,
  variables: { to: 'a@b.com' },
}

let agent: MockAgent
let originalDispatcher: Dispatcher

beforeEach(() => {
  originalDispatcher = getGlobalDispatcher()
  agent = new MockAgent()
  agent.disableNetConnect()
  setGlobalDispatcher(agent)
})

afterEach(async () => {
  await agent.close()
  setGlobalDispatcher(originalDispatcher)
})

describe('RestEngineClient.pollJobs', () => {
  it('returns jobs on 200', async () => {
    const pool = agent.get(HOST)
    pool.intercept({ path: '/v1/jobs/poll', method: 'POST' }).reply(
      200,
      JSON.stringify({ jobs: [JOB] }),
      { headers: { 'content-type': 'application/json' } },
    )

    const client = new RestEngineClient(BASE)
    const jobs = await client.pollJobs({ workerId: 'w1', jobTypes: ['send-email'], maxJobs: 10 })
    expect(jobs).toHaveLength(1)
    expect(jobs[0]!.id).toBe('job-1')
    expect(jobs[0]!.lockExpiresAt).toBe(JOB.lockExpiresAt)
  })

  it('returns empty array when jobs key is absent', async () => {
    const pool = agent.get(HOST)
    pool.intercept({ path: '/v1/jobs/poll', method: 'POST' }).reply(
      200,
      JSON.stringify({}),
      { headers: { 'content-type': 'application/json' } },
    )

    const client = new RestEngineClient(BASE)
    const jobs = await client.pollJobs({ workerId: 'w1', jobTypes: [], maxJobs: 10 })
    expect(jobs).toEqual([])
  })

  it('throws on 5xx', async () => {
    const pool = agent.get(HOST)
    pool.intercept({ path: '/v1/jobs/poll', method: 'POST' }).reply(503, '')

    const client = new RestEngineClient(BASE)
    await expect(
      client.pollJobs({ workerId: 'w1', jobTypes: [], maxJobs: 10 }),
    ).rejects.toThrow('503')
  })
})

describe('RestEngineClient.completeJob', () => {
  it('resolves on 200', async () => {
    const pool = agent.get(HOST)
    pool.intercept({ path: '/v1/jobs/job-1/complete', method: 'POST' }).reply(
      200,
      JSON.stringify({ ok: true }),
      { headers: { 'content-type': 'application/json' } },
    )

    const client = new RestEngineClient(BASE)
    await expect(
      client.completeJob('job-1', 'w1', { output: 42 }),
    ).resolves.toBeUndefined()
  })

  it('resolves on 204', async () => {
    const pool = agent.get(HOST)
    pool.intercept({ path: '/v1/jobs/job-1/complete', method: 'POST' }).reply(204, '')

    const client = new RestEngineClient(BASE)
    await expect(client.completeJob('job-1', 'w1')).resolves.toBeUndefined()
  })

  it('throws on 4xx', async () => {
    const pool = agent.get(HOST)
    pool.intercept({ path: '/v1/jobs/job-1/complete', method: 'POST' }).reply(409, '')

    const client = new RestEngineClient(BASE)
    await expect(client.completeJob('job-1', 'w1')).rejects.toThrow('409')
  })
})

describe('RestEngineClient.failJob', () => {
  it('resolves on 200', async () => {
    const pool = agent.get(HOST)
    pool.intercept({ path: '/v1/jobs/job-1/fail', method: 'POST' }).reply(
      200,
      JSON.stringify({ ok: true }),
      { headers: { 'content-type': 'application/json' } },
    )

    const client = new RestEngineClient(BASE)
    await expect(
      client.failJob('job-1', 'w1', 'handler exploded', true),
    ).resolves.toBeUndefined()
  })

  it('throws on 5xx', async () => {
    const pool = agent.get(HOST)
    pool.intercept({ path: '/v1/jobs/job-1/fail', method: 'POST' }).reply(500, '')

    const client = new RestEngineClient(BASE)
    await expect(
      client.failJob('job-1', 'w1', 'boom', false),
    ).rejects.toThrow('500')
  })
})

it('aborts a pending REST callback within its signal deadline', async () => {
  agent.get(HOST).intercept({ path: '/v1/jobs/job-1/complete', method: 'POST' }).reply(200, '{}').delay(200)
  await expect(new RestEngineClient(BASE).completeJob('job-1', 'w1', {}, { signal: AbortSignal.timeout(20) })).rejects.toThrow()
})

it('sends the requested lease and decodes a heartbeat deadline', async () => {
  const client = new RestEngineClient(BASE)
  agent.get(HOST).intercept({ path: '/v1/jobs/poll', method: 'POST', body: JSON.stringify({ workerId: 'w', jobTypes: ['work'], maxJobs: 1, leaseSeconds: 60 }) })
    .reply(200, { jobs: [] })
  await client.pollJobs({ workerId: 'w', jobTypes: ['work'], maxJobs: 1, leaseSeconds: 60 })
  agent.get(HOST).intercept({ path: '/v1/jobs/job/heartbeat', method: 'POST', body: JSON.stringify({ workerId: 'w', leaseSeconds: 60 }) })
    .reply(200, { accepted: true, lockExpiresAt: JOB.lockExpiresAt })
  expect(await client.extendJobLease('job', 'w', 60)).toBe(JOB.lockExpiresAt)
})

it.each([[404, HeartbeatUnsupportedError], [405, HeartbeatUnsupportedError], [501, HeartbeatUnsupportedError], [409, LeaseLostError], [503, HttpError]])
  ('maps REST heartbeat status %s', async (status, error) => {
    agent.get(HOST).intercept({ path: '/v1/jobs/job/heartbeat', method: 'POST' }).reply(Number(status), '')
    await expect(new RestEngineClient(BASE).extendJobLease('job', 'w')).rejects.toBeInstanceOf(error)
  })

it.each([{ accepted: false }, { accepted: true, lockExpiresAt: 'invalid' }])('rejects malformed heartbeat acceptance', async body => {
  agent.get(HOST).intercept({ path: '/v1/jobs/job/heartbeat', method: 'POST' }).reply(200, body)
  await expect(new RestEngineClient(BASE).extendJobLease('job', 'w')).rejects.toThrow('invalid heartbeat')
})
