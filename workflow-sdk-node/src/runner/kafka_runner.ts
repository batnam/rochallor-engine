import { setImmediate as nextTurn } from 'node:timers/promises'
import { KafkaJS } from '@confluentinc/kafka-javascript'
import type { EngineClient, Job } from '../client/types.js'
import { HandlerRegistry } from '../handler/registry.js'
import { Metrics } from '../metrics/metrics.js'
import { DeduplicationCache } from './deduplication_cache.js'
import { JobDispatchEvent } from '../generated/workflow/v1/engine.js'
import { deliver, execute, type Outcome } from './callback.js'

export interface KafkaRunnerConfig {
  workerId: string
  brokers: string[]
  clientId?: string
  dedupWindowMs?: number
  /** Enable TLS for broker connections. Required with SASL/PLAIN in production. */
  ssl?: boolean
  sasl?: KafkaJS.SASLOptions
}

export class KafkaRunner {
  private readonly kafka: KafkaJS.Kafka
  private readonly consumers: KafkaJS.Consumer[] = []
  private readonly dedupCache: DeduplicationCache
  private readonly shutdown = new AbortController()
  private readonly pending = new Set<Promise<void>>()

  constructor(private config: KafkaRunnerConfig, private engine: EngineClient, private registry: HandlerRegistry,
    readonly metrics = new Metrics()) {
    this.kafka = new KafkaJS.Kafka({ kafkaJS: {
      clientId: config.clientId || config.workerId, brokers: config.brokers,
      ...(config.ssl !== undefined && { ssl: config.ssl }), ...(config.sasl !== undefined && { sasl: config.sasl }),
    } })
    this.dedupCache = new DeduplicationCache(config.dedupWindowMs)
  }

  async start(): Promise<void> {
    try {
      for (const jobType of this.registry.jobTypes()) {
        if (this.shutdown.signal.aborted) break
        await this.startConsumer(jobType)
      }
    } catch (err) { await this.stop(); throw err }
  }

  private async startConsumer(jobType: string): Promise<void> {
    const topic = `workflow.jobs.${jobType}`
    const owners = new Map<number, AbortController>()
    const active = new Map<number, AbortController>()
    const commits = new Map<number, Promise<void>>()
    const consumer = this.kafka.consumer({
      kafkaJS: { groupId: `workflow.workers.${jobType}`, partitionAssigners: [KafkaJS.PartitionAssigners.cooperativeSticky], fromBeginning: true, autoCommit: false },
      // KafkaJS compatibility mode always disables native auto offset storage.
      'socket.timeout.ms': 5000,
      rebalance_cb: async (err: { code: number }, partitions: { partition: number }[]) => {
        for (const { partition } of partitions) {
          if (err.code === KafkaJS.ErrorCodes.ERR__ASSIGN_PARTITIONS) {
            if (!owners.has(partition)) owners.set(partition, new AbortController())
          } else {
            owners.get(partition)?.abort(new Error('partition revoked'))
            owners.delete(partition)
            // Finish an already submitted commit before allowing the handoff.
            await commits.get(partition)?.catch(() => {})
          }
        }
      },
    })
    this.consumers.push(consumer)
    await consumer.connect()
    await consumer.subscribe({ topics: [topic] })
    await consumer.run({ eachBatchAutoResolve: false, eachBatch: async payload => {
      const { partition, messages } = payload.batch
      const owner = owners.get(partition)
      const record = messages[0]
      if (!record || !owner || this.shutdown.signal.aborted || payload.isStale() || !payload.isRunning()) return
      if (active.get(partition) === owner) return
      active.set(partition, owner)
      // Return the unresolved batch immediately. The client rewinds it and keeps
      // polling while this partition is paused, including during callback retry.
      payload.pause()
      const signal = AbortSignal.any([owner.signal, this.shutdown.signal])
      const work = (async () => {
        await nextTurn()
        signal.throwIfAborted()
        console.info(`kafkarunner: topic=${topic} partition=${partition} offset=${record.offset}`)
        if (!record.value) { this.metrics.kafkaRecordErrors.inc(); throw new Error('empty Kafka record') }
        await this.dispatch(record.value, signal)
        signal.throwIfAborted()
        if (owners.get(partition) !== owner) return
        const offset = (BigInt(record.offset) + 1n).toString()
        const commit = consumer.commitOffsets([{ topic, partition, offset }])
        commits.set(partition, commit)
        try { await commit } finally { commits.delete(partition) }
        if (signal.aborted || owners.get(partition) !== owner) return
        consumer.seek({ topic, partition, offset })
        consumer.resume([{ topic, partitions: [partition] }])
      })().catch(err => {
        // Leave the partition paused on malformed records/permanent rejection.
        // Restart after repairing the cause; never advance over the record.
        console.error(`kafkarunner: unacknowledged topic=${topic} partition=${partition} offset=${record.offset}`, err)
      }).finally(() => {
        if (active.get(partition) === owner) active.delete(partition)
        this.pending.delete(work)
      })
      this.pending.add(work)
    } })
  }

  private async dispatch(data: Buffer, signal: AbortSignal): Promise<void> {
    let event: JobDispatchEvent
    try {
      event = JobDispatchEvent.decode(data)
      if (!event.jobId) throw new Error('Kafka record has no job ID')
    } catch (err) { this.metrics.kafkaRecordErrors.inc(); throw err }
    if (event.dedupId && this.dedupCache.has(event.dedupId)) return
    console.info(`kafkarunner: job_id=${event.jobId} dedup_id=${event.dedupId}`)
    let variables: Record<string, unknown> = {}
    let invalid: Outcome | undefined
    try {
      if (event.jobPayload.length) {
        variables = JSON.parse(Buffer.from(event.jobPayload).toString())
        if (variables === null || typeof variables !== 'object' || Array.isArray(variables)) throw new Error('payload must be an object')
      }
    } catch { invalid = { kind: 'fail', message: 'invalid job payload', retryable: false } }
    const job: Job = { id: event.jobId, jobType: event.jobType, instanceId: event.instanceId,
      stepExecutionId: event.stepExecutionId, retriesRemaining: event.retriesRemaining, variables }
    const outcome = invalid ?? await execute(this.registry, job)
    await deliver(this.engine, this.config.workerId, job.id, outcome, signal, this.metrics, 'kafka')
    signal.throwIfAborted()
    this.dedupCache.mark(event.dedupId)
  }

  async stop(): Promise<void> {
    this.shutdown.abort(new Error('runner stopped'))
    await Promise.allSettled(this.pending)
    await Promise.all(this.consumers.map(consumer => consumer.disconnect()))
    this.dedupCache.close()
  }
}
