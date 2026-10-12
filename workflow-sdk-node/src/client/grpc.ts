import * as grpc from '@grpc/grpc-js'
import { HeartbeatUnsupportedError, LeaseLostError, type CallOptions, type EngineClient, type Job, type LeaseExtender, type PollJobsRequest } from './types.js'
import {
  WorkflowEngineClient as GrpcStub,
  type WorkflowEngineClient as IWorkflowEngineClient,
  type PollJobsResponse,
  type ExtendJobLeaseResponse,
} from '../generated/workflow/v1/engine.js'

/**
 * gRPC transport for the workflow engine.
 *
 * Uses generated stubs from src/generated/workflow/v1/engine.ts
 * (generated from proto/workflow/v1/engine.proto via `npm run proto-gen`).
 *
 * Uses insecure (plaintext) credentials — suitable for internal/development use.
 */
export class GrpcEngineClient implements EngineClient, LeaseExtender {
  private readonly stub: IWorkflowEngineClient

  constructor(target: string, credentials?: grpc.ChannelCredentials) {
    this.stub = new GrpcStub(target, credentials ?? grpc.credentials.createInsecure())
  }

  async pollJobs(req: PollJobsRequest, options?: CallOptions): Promise<Job[]> {
    const resp = await callUnary<PollJobsResponse>((cb) =>
      this.stub.pollJobs(
        {
          workerId: req.workerId,
          jobTypes: req.jobTypes,
          maxJobs: req.maxJobs,
          leaseSeconds: req.leaseSeconds ?? 0,
        },
        cb,
      ),
      options?.signal,
    )
    return (resp.jobs ?? []).map(protoJobToSDK)
  }

  async extendJobLease(jobId: string, workerId: string, leaseSeconds = 0, options?: CallOptions): Promise<string> {
    try {
      const result = await callUnary<ExtendJobLeaseResponse>(cb => this.stub.extendJobLease({ jobId, workerId, leaseSeconds }, cb), options?.signal)
      if (!result.accepted || !result.lockExpiresAt || !Number.isFinite(result.lockExpiresAt.getTime())) throw new Error('invalid heartbeat response')
      return result.lockExpiresAt.toISOString()
    } catch (error) {
      if (typeof error === 'object' && error !== null && 'code' in error) {
        if (error.code === grpc.status.UNIMPLEMENTED) throw new HeartbeatUnsupportedError()
        if (error.code === grpc.status.FAILED_PRECONDITION) throw new LeaseLostError()
      }
      throw error
    }
  }

  async completeJob(
    jobId: string,
    workerId: string,
    variables?: Record<string, unknown>,
    options?: CallOptions,
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
      options?.signal,
    )
  }

  async failJob(
    jobId: string,
    workerId: string,
    errorMessage: string,
    retryable: boolean,
    options?: CallOptions,
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
      options?.signal,
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
  fn: (cb: (err: grpc.ServiceError | null, res: T) => void) => grpc.ClientUnaryCall,
  signal?: AbortSignal,
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    if (signal?.aborted) { reject(signal.reason); return }
    let call: grpc.ClientUnaryCall | undefined
    const abort = () => { call?.cancel(); reject(signal?.reason) }
    signal?.addEventListener('abort', abort, { once: true })
    try {
      call = fn((err, res) => {
        signal?.removeEventListener('abort', abort)
        if (err) reject(err)
        else resolve(res)
      })
      if (signal?.aborted) call.cancel()
    } catch (err) {
      signal?.removeEventListener('abort', abort)
      reject(err)
    }
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
  lockExpiresAt?: Date
}): Job {
  return {
    id: proto.id,
    jobType: proto.jobType,
    instanceId: proto.instanceId,
    stepExecutionId: proto.stepExecutionId,
    retriesRemaining: proto.retriesRemaining,
    variables: proto.variables,
    lockExpiresAt: proto.lockExpiresAt?.toISOString(),
  }
}
