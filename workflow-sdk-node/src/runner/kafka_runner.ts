import { KafkaJS } from '@confluentinc/kafka-javascript';
const { Kafka } = KafkaJS;

import { EngineClient, Job } from '../client/types';
import { HandlerRegistry } from '../handler/registry';
import { DeduplicationCache } from './deduplication_cache.js';

// @ts-ignore: JobDispatchEvent is generated during build
import { JobDispatchEvent } from '../generated/workflow/v1/engine';

export interface KafkaRunnerConfig {
  workerId: string;
  brokers: string[];
  clientId?: string;
  dedupWindowMs?: number;
}

export class KafkaRunner {
  private kafka: KafkaJS.Kafka;
  private consumers: KafkaJS.Consumer[] = [];
  private dedupCache: DeduplicationCache;
  private running = false;

  constructor(
    private config: KafkaRunnerConfig,
    private engine: EngineClient,
    private registry: HandlerRegistry
  ) {
    this.kafka = new Kafka({
      kafkaJS: {
        clientId: config.clientId || config.workerId,
        brokers: config.brokers,
      }
    });
    this.dedupCache = new DeduplicationCache(config.dedupWindowMs);
  }

  async start(): Promise<void> {
    this.running = true;
    const jobTypes = this.registry.jobTypes();
    console.log(`KafkaRunner starting: workerId=${this.config.workerId} jobTypes=${jobTypes}`);

    for (const jobType of jobTypes) {
      await this.startConsumer(jobType);
    }
  }

  private async startConsumer(jobType: string): Promise<void> {
    const topic = `workflow.jobs.${jobType}`;
    const groupId = `workflow.workers.${jobType}`;

    const consumer = this.kafka.consumer({
      kafkaJS: {
        groupId,
        partitionAssigners: [KafkaJS.PartitionAssigners.cooperativeSticky],
        fromBeginning: true,
      }
    });
    this.consumers.push(consumer);

    await consumer.connect();
    await consumer.subscribe({ topics: [topic] });

    await consumer.run({
      eachBatch: async (payload: KafkaJS.EachBatchPayload) => {
        for (const record of payload.batch.messages) {
          if (!this.running) break;
          if (record.value) {
            try {
              await this.dispatch(record.value);
            } catch (err) {
              console.error(`Error dispatching record from ${topic}:`, err);
            }
          }
        }
        await payload.resolveOffset(payload.batch.messages[payload.batch.messages.length - 1].offset);
        await payload.commitOffsetsIfNecessary();
      },
    });
  }

  private async dispatch(data: Buffer): Promise<void> {
    const event = JobDispatchEvent.decode(data);

    if (event.dedupId && this.dedupCache.seenRecently(event.dedupId)) {
      console.debug(`kafkarunner: skipping duplicate job_id=${event.jobId} dedup_id=${event.dedupId}`);
      return;
    }

    console.info(`kafkarunner: dispatching job_id=${event.jobId} job_type=${event.jobType}`);

    const handler = this.registry.get(event.jobType);
    if (!handler) {
      console.warn(`kafkarunner: no handler for jobType=${event.jobType} jobId=${event.jobId}`);
      await this.engine.failJob(event.jobId, this.config.workerId, `no handler registered for ${event.jobType}`, false);
      return;
    }

    let variables: Record<string, unknown> = {};
    if (event.jobPayload && event.jobPayload.length > 0) {
      try {
        variables = JSON.parse(Buffer.from(event.jobPayload).toString());
      } catch (e) {
        console.error(`kafkarunner: failed to parse job payload for ${event.jobId}:`, e);
      }
    }

    const job: Job = {
      id: event.jobId,
      jobType: event.jobType,
      instanceId: event.instanceId,
      stepExecutionId: event.stepExecutionId,
      retriesRemaining: event.retriesRemaining,
      variables,
    };

    try {
      const ctx = HandlerRegistry.contextFrom(job);
      const result = await handler(ctx);
      console.info(`kafkarunner: job completed job_id=${job.id}`);
      await this.engine.completeJob(job.id, this.config.workerId, result.variablesToSet || {});
    } catch (err: any) {
      const retryable = !(err.name === 'NonRetryableError' || err.isNonRetryable);
      if (retryable) {
        console.warn(`kafkarunner: job failed (retryable) job_id=${job.id}:`, err);
      } else {
        console.warn(`kafkarunner: job failed (non-retryable) job_id=${job.id}:`, err);
      }
      await this.engine.failJob(job.id, this.config.workerId, err.message, retryable);
    }
  }

  async stop(): Promise<void> {
    this.running = false;
    for (const consumer of this.consumers) {
      await consumer.disconnect();
    }
    this.dedupCache.close();
  }
}
