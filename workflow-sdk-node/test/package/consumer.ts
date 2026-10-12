import {
  RestEngineClient, GrpcEngineClient, HandlerRegistry, NonRetryableError,
  Runner, KafkaRunner, Metrics,
  type EngineClient, type Job, type PollJobsRequest,
  type Handler, type JobContext, type HandlerResult,
  type RunnerConfig, type KafkaRunnerConfig,
} from 'rochallor-workflow-sdk-node'

const job: Job = {
  id: 'job', jobType: 'smoke', instanceId: 'instance',
  stepExecutionId: 'step', retriesRemaining: 1, variables: { value: 42 },
}
const request: PollJobsRequest = { workerId: 'smoke', jobTypes: [job.jobType], maxJobs: 1 }
const handler: Handler = async (context: JobContext): Promise<HandlerResult> => ({
  variablesToSet: context.variables,
})
const registry = new HandlerRegistry()
registry.register(request.jobTypes[0], handler)
const result = await registry.get(job.jobType)!(HandlerRegistry.contextFrom(job))
if (result.variablesToSet?.value !== 42) throw new Error('Handler public API failed')
if (new NonRetryableError('smoke').name !== 'NonRetryableError') throw new Error('Error export failed')

const rest: EngineClient = new RestEngineClient('http://127.0.0.1:1')
const grpc = new GrpcEngineClient('127.0.0.1:1')
const clients: EngineClient[] = [rest, grpc]
// A pre-heartbeat custom client still implements the public interface unchanged.
clients.push({ pollJobs: async () => [], completeJob: async () => {}, failJob: async () => {} })
const config: RunnerConfig = { workerId: request.workerId, concurrency: 1 }
const kafkaConfig: KafkaRunnerConfig = { workerId: request.workerId, brokers: ['127.0.0.1:1'] }
try {
  for (const client of clients) {
    await new Runner(client, registry, config).run(AbortSignal.abort())
    await new KafkaRunner(kafkaConfig, client, registry).stop()
  }
  new Metrics().jobsCompleted.inc({ job_type: job.jobType, outcome: 'completed' })
} finally {
  grpc.close()
}
