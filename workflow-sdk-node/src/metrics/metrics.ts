import { Counter, Gauge, Histogram, Registry } from 'prom-client'

/** Prometheus metrics for the Node/TS SDK. */
export class Metrics {
  readonly heartbeats: Counter<'result'>
  readonly callbackRetries: Counter<'dispatch' | 'callback'>
  readonly callbackRejected: Counter<'dispatch' | 'callback'>
  readonly callbackPending: Gauge<'dispatch' | 'callback'>
  readonly callbackOldest: Gauge<'dispatch' | 'callback'>
  readonly kafkaRecordErrors: Counter
  private readonly pending = new Map<string, Map<symbol, number>>()
  readonly pollLatency: Histogram
  readonly lockConflicts: Counter
  readonly handlerLatency: Histogram<'job_type'>
  readonly retriesTotal: Counter<'job_type'>
  readonly jobsCompleted: Counter<'job_type' | 'outcome'>

  constructor(registry: Registry = new Registry()) {
    this.heartbeats = new Counter({ name: 'workflow_sdk_heartbeat_total', help: 'Lease renewals', labelNames: ['result'], registers: [registry] })
    const labels = ['dispatch', 'callback'] as const
    this.callbackRetries = new Counter({ name: 'workflow_sdk_callback_retries_total', help: 'Callback transport retries', labelNames: labels, registers: [registry] })
    this.callbackRejected = new Counter({ name: 'workflow_sdk_callback_rejected_total', help: 'Permanently rejected callbacks', labelNames: labels, registers: [registry] })
    this.callbackPending = new Gauge({ name: 'workflow_sdk_callback_pending', help: 'Unacknowledged callbacks', labelNames: labels, registers: [registry] })
    const pending = this.pending
    this.callbackOldest = new Gauge({ name: 'workflow_sdk_callback_oldest_pending_seconds', help: 'Age of oldest unacknowledged callback', labelNames: labels, registers: [registry], collect() {
      for (const [key, starts] of pending) {
        const [dispatch, callback] = key.split(':')
        this.set({ dispatch, callback }, starts.size ? (Date.now() - Math.min(...starts.values())) / 1000 : 0)
      }
    } })
    this.kafkaRecordErrors = new Counter({ name: 'workflow_sdk_kafka_record_errors_total', help: 'Invalid Kafka records blocking delivery', registers: [registry] })
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

  trackCallback(dispatch: 'polling' | 'kafka', callback: 'complete' | 'fail'): () => void {
    const key = `${dispatch}:${callback}`
    let starts = this.pending.get(key)
    if (!starts) { starts = new Map(); this.pending.set(key, starts) }
    const token = Symbol()
    starts.set(token, Date.now())
    this.callbackPending.inc({ dispatch, callback })
    return () => { starts.delete(token); this.callbackPending.dec({ dispatch, callback }) }
  }
}
