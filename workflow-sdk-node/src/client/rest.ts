import { fetch } from 'undici'
import type { EngineClient, Job, PollJobsRequest } from './types.js'

/**
 * REST transport implementation of {@link EngineClient}.
 * Uses `undici` fetch for HTTP/1.1 requests.
 */
export class RestEngineClient implements EngineClient {
  private readonly baseUrl: string

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl.replace(/\/$/, '')
  }

  async pollJobs(req: PollJobsRequest): Promise<Job[]> {
    const resp = await fetch(`${this.baseUrl}/v1/jobs/poll`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workerId: req.workerId,
        jobTypes: req.jobTypes,
        maxJobs: req.maxJobs,
      }),
    })
    if (!resp.ok) {
      throw new Error(`pollJobs: engine returned ${resp.status}`)
    }
    const data = (await resp.json()) as { jobs?: Job[] }
    return data.jobs ?? []
  }

  async completeJob(
    jobId: string,
    workerId: string,
    variables?: Record<string, unknown>,
  ): Promise<void> {
    const resp = await fetch(`${this.baseUrl}/v1/jobs/${jobId}/complete`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workerId, variables }),
    })
    if (!resp.ok && resp.status !== 204) {
      throw new Error(`completeJob ${jobId}: engine returned ${resp.status}`)
    }
  }

  async failJob(
    jobId: string,
    workerId: string,
    errorMessage: string,
    retryable: boolean,
  ): Promise<void> {
    const resp = await fetch(`${this.baseUrl}/v1/jobs/${jobId}/fail`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workerId, errorMessage, retryable }),
    })
    if (!resp.ok && resp.status !== 204) {
      throw new Error(`failJob ${jobId}: engine returned ${resp.status}`)
    }
  }
}
