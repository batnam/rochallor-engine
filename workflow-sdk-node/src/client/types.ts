/** A job returned by the Engine poll endpoint. */
export interface Job {
  id: string
  jobType: string
  instanceId: string
  stepExecutionId: string
  retriesRemaining: number
  variables?: Record<string, unknown>
  /** Engine lease deadline for polling. Absent for Kafka/custom clients. */
  lockExpiresAt?: string
}

/** Input for PollJobs. */
export interface PollJobsRequest {
  workerId: string
  jobTypes: string[]
  maxJobs: number
  /** 0/omitted uses the engine default (30s); otherwise 5–3600 seconds. */
  leaseSeconds?: number
}

/** The EngineClient interface — satisfied by both REST and gRPC implementations. */
export interface EngineClient {
  pollJobs(req: PollJobsRequest, options?: CallOptions): Promise<Job[]>
  completeJob(jobId: string, workerId: string, variables?: Record<string, unknown>, options?: CallOptions): Promise<void>
  failJob(jobId: string, workerId: string, errorMessage: string, retryable: boolean, options?: CallOptions): Promise<void>
}

/** Optional transport cancellation; existing clients with fewer parameters remain compatible. */
export interface CallOptions { signal?: AbortSignal }

/** Optional capability; custom EngineClient implementations need not implement it. */
export interface LeaseExtender {
  extendJobLease(jobId: string, workerId: string, leaseSeconds?: number, options?: CallOptions): Promise<string>
}

export class HeartbeatUnsupportedError extends Error {
  constructor() { super('heartbeat requires an engine/client supporting extendJobLease') }
}

export class LeaseLostError extends Error {
  constructor() { super('job lease lost') }
}

export class HttpError extends Error {
  constructor(message: string, readonly statusCode: number) { super(message) }
}
