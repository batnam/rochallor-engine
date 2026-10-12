import { fetch } from 'undici'
import { HeartbeatUnsupportedError, HttpError, LeaseLostError, type CallOptions, type EngineClient, type Job, type LeaseExtender, type PollJobsRequest } from './types.js'

/**
 * REST transport implementation of {@link EngineClient}.
 * Uses `undici` fetch for HTTP/1.1 requests.
 */
export class RestEngineClient implements EngineClient, LeaseExtender {
  private readonly baseUrl: string

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl.replace(/\/$/, '')
  }

  async pollJobs(req: PollJobsRequest, options?: CallOptions): Promise<Job[]> {
    const resp = await fetch(`${this.baseUrl}/v1/jobs/poll`, {
      signal: options?.signal,
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workerId: req.workerId,
        jobTypes: req.jobTypes,
        maxJobs: req.maxJobs,
        leaseSeconds: req.leaseSeconds,
      }),
    })
    if (!resp.ok) {
      throw new Error(`pollJobs: engine returned ${resp.status}`)
    }
    const data = (await resp.json()) as { jobs?: Job[] }
    return data.jobs ?? []
  }

  async extendJobLease(jobId: string, workerId: string, leaseSeconds = 0, options?: CallOptions): Promise<string> {
    const response = await fetch(`${this.baseUrl}/v1/jobs/${jobId}/heartbeat`, {
      method: 'POST', signal: options?.signal, headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workerId, leaseSeconds }),
    })
    if (!response.ok) {
      await response.body?.cancel()
      if ([404, 405, 501].includes(response.status)) throw new HeartbeatUnsupportedError()
      if (response.status === 409) throw new LeaseLostError()
      throw new HttpError(`heartbeat ${jobId}: engine returned ${response.status}`, response.status)
    }
    const result = await response.json() as { accepted?: boolean; lockExpiresAt?: string }
    if (!result.accepted || !result.lockExpiresAt || !Number.isFinite(Date.parse(result.lockExpiresAt))) throw new Error('invalid heartbeat response')
    return result.lockExpiresAt
  }

  async completeJob(
    jobId: string,
    workerId: string,
    variables?: Record<string, unknown>,
    options?: CallOptions,
  ): Promise<void> {
    const resp = await fetch(`${this.baseUrl}/v1/jobs/${jobId}/complete`, {
      signal: options?.signal,
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workerId, variables }),
    })
    await resp.body?.cancel()
    if (!resp.ok && resp.status !== 204) {
      throw new HttpError(`completeJob ${jobId}: engine returned ${resp.status}`, resp.status)
    }
  }

  async failJob(
    jobId: string,
    workerId: string,
    errorMessage: string,
    retryable: boolean,
    options?: CallOptions,
  ): Promise<void> {
    const resp = await fetch(`${this.baseUrl}/v1/jobs/${jobId}/fail`, {
      signal: options?.signal,
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workerId, errorMessage, retryable }),
    })
    await resp.body?.cancel()
    if (!resp.ok && resp.status !== 204) {
      throw new HttpError(`failJob ${jobId}: engine returned ${resp.status}`, resp.status)
    }
  }
}
