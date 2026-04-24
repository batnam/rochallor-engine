import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { WorkflowEngineClient as IWorkflowEngineClient } from '../../src/generated/workflow/v1/engine.js'

// Mock the generated gRPC stub so tests don't require a live gRPC server.
vi.mock('../../src/generated/workflow/v1/engine.js', () => {
  const mockStub: Partial<IWorkflowEngineClient> = {
    pollJobs: vi.fn(),
    completeJob: vi.fn(),
    failJob: vi.fn(),
    close: vi.fn(),
  }
  return {
    WorkflowEngineClient: vi.fn().mockImplementation(() => mockStub),
  }
})

// Import after mock setup.
import { GrpcEngineClient } from '../../src/client/grpc.js'
import { WorkflowEngineClient as MockStubConstructor } from '../../src/generated/workflow/v1/engine.js'

function getStub(): Partial<IWorkflowEngineClient> {
  // @ts-ignore — access the mock instance
  return (MockStubConstructor as ReturnType<typeof vi.fn>).mock.results[0]?.value
}

/**
 * Configures a mocked stub method to call its callback with the given response.
 */
function stubReturns(method: keyof IWorkflowEngineClient, response: unknown): void {
  const stub = getStub()
  ;(stub[method] as ReturnType<typeof vi.fn>).mockImplementation(
    (_req: unknown, cb: (err: null, res: unknown) => void) => {
      cb(null, response)
      return {} // ClientUnaryCall stub
    },
  )
}

describe('GrpcEngineClient', () => {
  let client: GrpcEngineClient

  beforeEach(() => {
    vi.clearAllMocks()
    client = new GrpcEngineClient('localhost:9090')
  })

  describe('pollJobs', () => {
    it('returns mapped jobs from the server response', async () => {
      stubReturns('pollJobs', {
        jobs: [
          {
            id: 'j1',
            jobType: 'my-job',
            instanceId: 'i1',
            stepExecutionId: 'se1',
            retriesRemaining: 2,
            variables: undefined,
          },
        ],
      })

      const jobs = await client.pollJobs({ workerId: 'w1', jobTypes: ['my-job'], maxJobs: 1 })

      expect(jobs).toHaveLength(1)
      expect(jobs[0].id).toBe('j1')
      expect(jobs[0].jobType).toBe('my-job')
      expect(jobs[0].retriesRemaining).toBe(2)
    })

    it('populates variables when server returns them', async () => {
      stubReturns('pollJobs', {
        jobs: [
          {
            id: 'j2',
            jobType: 'loan-check',
            instanceId: 'i2',
            stepExecutionId: 'se2',
            retriesRemaining: 3,
            variables: { loanId: 'L-001', amount: 5000 },
          },
        ],
      })

      const jobs = await client.pollJobs({ workerId: 'w1', jobTypes: ['loan-check'], maxJobs: 1 })

      expect(jobs[0].variables).toBeDefined()
      expect(jobs[0].variables?.['loanId']).toBe('L-001')
    })

    it('returns empty array when server returns no jobs', async () => {
      stubReturns('pollJobs', { jobs: [] })
      const jobs = await client.pollJobs({ workerId: 'w1', jobTypes: ['x'], maxJobs: 1 })
      expect(jobs).toHaveLength(0)
    })
  })

  describe('completeJob', () => {
    it('calls through without error', async () => {
      stubReturns('completeJob', {})
      await expect(
        client.completeJob('j1', 'w1', { result: 'ok' }),
      ).resolves.toBeUndefined()
    })
  })

  describe('failJob', () => {
    it('calls through without error', async () => {
      stubReturns('failJob', {})
      await expect(
        client.failJob('j1', 'w1', 'boom', true),
      ).resolves.toBeUndefined()
    })
  })
})
