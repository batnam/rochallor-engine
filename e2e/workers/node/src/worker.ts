import { RestEngineClient } from '../sdk-src/client/rest'
import { HandlerRegistry } from '../sdk-src/handler/registry'
import { Runner } from '../sdk-src/runner/runner'
import { KafkaRunner } from '../sdk-src/runner/kafka_runner'

const engineUrl = process.env['ENGINE_REST_URL'] ?? 'http://localhost:8080'
const workerId = process.env['WORKER_ID'] ?? 'worker-node-1'

const client = new RestEngineClient(engineUrl)
const registry = new HandlerRegistry()

// Linear scenario handlers
registry.register('node-step-a', async () => ({ variablesToSet: { stepA: 'done' } }))
registry.register('node-step-b', async () => ({ variablesToSet: { stepB: 'done' } }))
registry.register('node-step-c', async () => ({ variablesToSet: { stepC: 'done' } }))

// Decision scenario handlers
registry.register('node-prepare', async () => ({ variablesToSet: { result: 'approved' } }))
registry.register('node-handle-approved', async () => ({ variablesToSet: { handled: 'approved' } }))
registry.register('node-handle-rejected', async () => ({ variablesToSet: { handled: 'rejected' } }))

// Parallel scenario handlers
registry.register('node-branch-left', async () => ({ variablesToSet: { branchLeft: 'done' } }))
registry.register('node-branch-right', async () => ({ variablesToSet: { branchRight: 'done' } }))
registry.register('node-merge-done', async () => ({ variablesToSet: { merged: 'done' } }))

// User-task scenario handlers
registry.register('node-before-review', async () => ({ variablesToSet: { beforeReview: 'done' } }))
registry.register('node-after-review', async () => ({ variablesToSet: { afterReview: 'done' } }))

// Timer scenario handlers
registry.register('node-before-wait', async () => ({ variablesToSet: { beforeWait: 'done' } }))
registry.register('node-timer-fired', async () => ({ variablesToSet: { timerFired: 'done' } }))

// Retry-fail scenario handler: fails on first attempt (retriesRemaining == 2 == retryCount)
registry.register('node-flaky', async (ctx) => {
  if (ctx.retriesRemaining === 2) {
    throw new Error('simulated transient failure')
  }
  return { variablesToSet: { flaky: 'done' } }
})

// Signal-user-task scenario handlers
registry.register('node-signalwaitstep-completeusertask-start', async () => ({ variablesToSet: { started: true } }))
registry.register('node-signalwaitstep-completeusertask-end', async () => ({ variablesToSet: { ended: true } }))

// Chaining scenario handlers
registry.register('node-chain-start', async () => ({ variablesToSet: { applicantId: '123', amount: 100 } }))
registry.register('node-chain-finalize', async () => ({ variablesToSet: { finalized: true } }))

// Loan approval scenario handlers
registry.register('validate-application', async () => ({ variablesToSet: { applicationValidated: true } }))
registry.register('credit-score', async () => ({ variablesToSet: { creditScoreChecked: true } }))
registry.register('fraud-screen', async () => ({ variablesToSet: { fraudScreened: true } }))
registry.register('escalate-review', async () => ({ variablesToSet: { reviewEscalated: true } }))
registry.register('approve-loan', async () => ({ variablesToSet: { loanApproved: true } }))
registry.register('notify-approval-overdue', async () => ({ variablesToSet: { approvalOverdueNotified: true } }))
registry.register('prepare-disbursement', async () => ({ variablesToSet: { disbursementPrepared: true } }))
registry.register('transfer-funds', async () => ({ variablesToSet: { fundsTransferred: true } }))
registry.register('notify-disbursement', async () => ({ variablesToSet: { disbursementNotified: true } }))

const controller = new AbortController()
process.on('SIGTERM', () => controller.abort())
process.on('SIGINT', () => controller.abort())

async function main() {
  const mode = process.env['WE_DISPATCH_MODE'] ?? 'polling'

  if (mode === 'kafka_outbox') {
    const brokers = (process.env['WE_KAFKA_SEED_BROKERS'] ?? 'localhost:9092').split(',')
    console.info(`worker starting (kafka mode): brokers=${brokers} workerID=${workerId}`)
    const runner = new KafkaRunner({ workerId, brokers }, client, registry)
    try {
      await runner.start()
    } catch (err: unknown) {
      console.error('[worker-node] KafkaRunner fatal error:', err)
      process.exit(1)
    }
  } else {
    console.info(`worker starting (polling mode): engine=${engineUrl} workerID=${workerId}`)
    const runner = new Runner(client, registry, { workerId })
    try {
      await runner.run(controller.signal)
    } catch (err: unknown) {
      console.error('[worker-node] Runner fatal error:', err)
      process.exit(1)
    }
  }

  console.info('worker stopped')
}

main().catch(err => {
  console.error('[worker-node] Unhandled error:', err)
  process.exit(1)
})
