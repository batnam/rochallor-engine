import * as grpc from '@grpc/grpc-js'
import type { EngineClient, Job, PollJobsRequest } from './types.js'
import {
  WorkflowEngineClient as GrpcStub,
  type WorkflowEngineClient as IWorkflowEngineClient,
  type PollJobsResponse,
} from '../generated/workflow/v1/engine.js'

/**
 * gRPC transport for the workflow engine.
 *
 * Uses generated stubs from src/generated/workflow/v1/engine.ts
 * (generated from proto/workflow/v1/engine.proto via `npm run proto-gen`).
 *
 * Uses insecure (plaintext) credentials — suitable for internal/development use.
 */
export class GrpcEngineClient implements EngineClient {
  private readonly stub: IWorkflowEngineClient

  constructor(target: string, credentials?: grpc.ChannelCredentials) {
    this.stub = new GrpcStub(target, credentials ?? grpc.credentials.createInsecure())
  }

  async pollJobs(req: PollJobsRequest): Promise<Job[]> {
    const resp = await callUnary<PollJobsResponse>((cb) =>
      this.stub.pollJobs(
        {
          workerId: req.workerId,
          jobTypes: req.jobTypes,
          maxJobs: req.maxJobs,
          leaseSeconds: 0,
        },
        cb,
      ),
    )
    return (resp.jobs ?? []).map(protoJobToSDK)
  }

  async completeJob(
    jobId: string,
    workerId: string,
    variables?: Record<string, unknown>,
  ): Promise<void> {
    await callUnary((cb) =>
      this.stub.completeJob(
        {
          jobId,
          workerId,
          variablesToSet: variables ?? undefined,
        },
        cb,
      ),
    )
  }

  async failJob(
    jobId: string,
    workerId: string,
    errorMessage: string,
    retryable: boolean,
  ): Promise<void> {
    await callUnary((cb) =>
      this.stub.failJob(
        {
          jobId,
          workerId,
          errorMessage,
          retryable,
        },
        cb,
      ),
    )
  }

  /** Closes the underlying gRPC channel. */
  close(): void {
    this.stub.close()
  }
}

// ── Helpers ────────────────────────────────────────────────────────────────

/**
 * Wraps a gRPC unary callback call in a Promise.
 */
function callUnary<T>(
  fn: (cb: (err: grpc.ServiceError | null, res: T) => void) => void,
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    fn((err, res) => {
      if (err) reject(err)
      else resolve(res)
    })
  })
}

/**
 * Maps a proto Job (from generated stubs) to the SDK Job interface.
 * The generated stub already unwraps Struct → plain JS object for `variables`.
 */
function protoJobToSDK(proto: {
  id: string
  jobType: string
  instanceId: string
  stepExecutionId: string
  retriesRemaining: number
  variables?: Record<string, unknown>
}): Job {
  return {
    id: proto.id,
    jobType: proto.jobType,
    instanceId: proto.instanceId,
    stepExecutionId: proto.stepExecutionId,
    retriesRemaining: proto.retriesRemaining,
    variables: proto.variables,
  }
}
