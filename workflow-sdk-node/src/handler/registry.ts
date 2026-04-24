import type { Job } from '../client/types.js'

/** Context passed to handler functions. */
export interface JobContext {
  jobId: string
  instanceId: string
  jobType: string
  retriesRemaining: number
  variables: Record<string, unknown>
}

/** Result returned by a handler on success. */
export interface HandlerResult {
  variablesToSet?: Record<string, unknown>
}

/** A job handler function. Throw NonRetryableError to skip the retry budget. */
export type Handler = (ctx: JobContext) => Promise<HandlerResult>

/** Throw from a Handler to bypass the retry budget (retryable=false). */
export class NonRetryableError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'NonRetryableError'
  }
}

/**
 * Registry maps jobType strings to Handler functions (R-010).
 */
export class HandlerRegistry {
  private readonly handlers = new Map<string, Handler>()

  /** Register a handler for a jobType. */
  register(jobType: string, handler: Handler): void {
    if (!jobType) throw new Error('jobType must not be empty')
    this.handlers.set(jobType, handler)
  }

  /** Returns the handler for jobType, or undefined. */
  get(jobType: string): Handler | undefined {
    return this.handlers.get(jobType)
  }

  /** Returns all registered jobType strings. */
  jobTypes(): string[] {
    return [...this.handlers.keys()]
  }

  /** Build a JobContext from an engine Job. */
  static contextFrom(job: Job): JobContext {
    return {
      jobId: job.id,
      instanceId: job.instanceId,
      jobType: job.jobType,
      retriesRemaining: job.retriesRemaining,
      variables: (job.variables as Record<string, unknown>) ?? {},
    }
  }
}
