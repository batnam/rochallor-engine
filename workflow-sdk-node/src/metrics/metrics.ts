import { Counter, Histogram, Registry } from 'prom-client'

/** Prometheus metrics for the Node/TS SDK. */
export class Metrics {
  readonly pollLatency: Histogram
  readonly lockConflicts: Counter
  readonly handlerLatency: Histogram<'job_type'>
  readonly retriesTotal: Counter<'job_type'>
  readonly jobsCompleted: Counter<'job_type' | 'outcome'>

  constructor(registry: Registry = new Registry()) {
    this.pollLatency = new Histogram({
      name: 'workflow_sdk_poll_latency_seconds',
      help: 'Latency of pollJobs calls',
      registers: [registry],
    })

    this.lockConflicts = new Counter({
      name: 'workflow_sdk_lock_conflicts_total',
      help: 'Poll rounds that returned zero jobs',
      registers: [registry],
    })

    this.handlerLatency = new Histogram({
      name: 'workflow_sdk_handler_latency_seconds',
      help: 'Duration of handler executions',
      labelNames: ['job_type'],
      registers: [registry],
    })

    this.retriesTotal = new Counter({
      name: 'workflow_sdk_retries_total',
      help: 'Jobs retried by the SDK runner',
      labelNames: ['job_type'],
      registers: [registry],
    })

    this.jobsCompleted = new Counter({
      name: 'workflow_sdk_jobs_completed_total',
      help: 'Jobs completed by the SDK runner',
      labelNames: ['job_type', 'outcome'],
      registers: [registry],
    })
  }
}
