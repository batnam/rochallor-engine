/** A job returned by the Engine poll endpoint. */
export interface Job {
  id: string
  jobType: string
  instanceId: string
  stepExecutionId: string
  retriesRemaining: number
  variables?: Record<string, unknown>
}

/** Input for PollJobs. */
export interface PollJobsRequest {
  workerId: string
  jobTypes: string[]
  maxJobs: number
}

/** The EngineClient interface — satisfied by both REST and gRPC implementations. */
export interface EngineClient {
  pollJobs(req: PollJobsRequest): Promise<Job[]>
  completeJob(jobId: string, workerId: string, variables?: Record<string, unknown>): Promise<void>
  failJob(jobId: string, workerId: string, errorMessage: string, retryable: boolean): Promise<void>
}
