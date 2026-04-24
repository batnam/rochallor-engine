package com.batnam.rochallor_engine.workflow_sdk_java.runner;

import com.batnam.workflow.api.v1.JobDispatchEvent;
import com.batnam.rochallor_engine.workflow_sdk_java.client.EngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.client.Job;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.HandlerRegistry;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.JobContext;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.JobHandler;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.NonRetryableException;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.apache.kafka.clients.consumer.ConsumerRecords;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.common.serialization.ByteArrayDeserializer;
import org.apache.kafka.common.serialization.StringDeserializer;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Properties;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * KafkaRunner implements the event-driven dispatch mode for the Java SDK.
 * It consumes JobDispatchEvent records from Kafka topics and executes handlers.
 */
public class KafkaRunner implements AutoCloseable {

    private static final Logger LOG = Logger.getLogger(KafkaRunner.class.getName());
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private final String workerId;
    private final String brokers;
    private final EngineClient engine;
    private final HandlerRegistry registry;
    private final DeduplicationCache dedupCache;
    private final List<KafkaConsumer<String, byte[]>> consumers = new ArrayList<>();
    private final ExecutorService executor;
    private final Properties kafkaProps;

    private volatile boolean running = false;

    public KafkaRunner(String workerId, String brokers, EngineClient engine, 
                       HandlerRegistry registry, Properties extraKafkaProps) {
        this.workerId = workerId;
        this.brokers = brokers;
        this.engine = engine;
        this.registry = registry;
        this.dedupCache = new DeduplicationCache(Duration.ofMinutes(10));
        
        this.kafkaProps = new Properties();
        this.kafkaProps.put(ConsumerConfig.BOOTSTRAP_SERVERS_CONFIG, brokers);
        this.kafkaProps.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class.getName());
        this.kafkaProps.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG, ByteArrayDeserializer.class.getName());
        this.kafkaProps.put(ConsumerConfig.ENABLE_AUTO_COMMIT_CONFIG, "false");
        this.kafkaProps.put(ConsumerConfig.AUTO_OFFSET_RESET_CONFIG, "earliest");
        this.kafkaProps.put(ConsumerConfig.PARTITION_ASSIGNMENT_STRATEGY_CONFIG, "org.apache.kafka.clients.consumer.CooperativeStickyAssignor");
        if (extraKafkaProps != null) {
            this.kafkaProps.putAll(extraKafkaProps);
        }

        this.executor = Executors.newVirtualThreadPerTaskExecutor();
    }

    public void start() {
        running = true;
        List<String> jobTypes = List.copyOf(registry.jobTypes());
        LOG.info("KafkaRunner starting: workerId=" + workerId + " jobTypes=" + jobTypes);

        for (String jobType : jobTypes) {
            executor.submit(() -> consumeLoop(jobType));
        }
    }

    private void consumeLoop(String jobType) {
        String topic = "workflow.jobs." + jobType;
        String group = "workflow.workers." + jobType;
        
        Properties props = new Properties();
        props.putAll(kafkaProps);
        props.put(ConsumerConfig.GROUP_ID_CONFIG, group);
        
        try (KafkaConsumer<String, byte[]> consumer = new KafkaConsumer<>(props)) {
            synchronized (consumers) {
                consumers.add(consumer);
            }
            consumer.subscribe(Collections.singletonList(topic));
            
            while (running) {
                ConsumerRecords<String, byte[]> records = consumer.poll(Duration.ofMillis(500));
                for (ConsumerRecord<String, byte[]> record : records) {
                    try {
                        dispatch(record);
                    } catch (Exception e) {
                        LOG.log(Level.SEVERE, "Error dispatching Kafka record from " + topic, e);
                    }
                }
                consumer.commitSync();
            }
        } catch (Exception e) {
            if (running) {
                LOG.log(Level.SEVERE, "Kafka consumer loop failed for " + topic, e);
            }
        }
    }

    private void dispatch(ConsumerRecord<String, byte[]> record) throws Exception {
        JobDispatchEvent event = JobDispatchEvent.parseFrom(record.value());
        
        if (event.getDedupId() != null && !event.getDedupId().isEmpty()) {
            if (dedupCache.seenRecently(event.getDedupId())) {
                LOG.fine("kafkarunner: skipping duplicate job_id=" + event.getJobId() + " dedup_id=" + event.getDedupId());
                return;
            }
        }

        LOG.info("kafkarunner: dispatching job_id=" + event.getJobId() + " job_type=" + event.getJobType());

        Optional<JobHandler> handlerOpt = registry.get(event.getJobType());
        if (handlerOpt.isEmpty()) {
            LOG.warning("kafkarunner: no handler for jobType=" + event.getJobType() + " jobId=" + event.getJobId());
            engine.failJob(event.getJobId(), workerId, "no handler registered for " + event.getJobType(), false);
            return;
        }

        Map<String, Object> variables = Collections.emptyMap();
        if (event.getJobPayload() != null && event.getJobPayload().size() > 0) {
            variables = MAPPER.readValue(event.getJobPayload().toByteArray(), new TypeReference<Map<String, Object>>() {});
        }

        Job job = new Job();
        job.id = event.getJobId();
        job.jobType = event.getJobType();
        job.instanceId = event.getInstanceId();
        job.stepExecutionId = event.getStepExecutionId();
        job.retriesRemaining = event.getRetriesRemaining();
        job.variables = variables;

        try {
            JobContext ctx = new JobContext(job);
            Map<String, Object> result = handlerOpt.get().handle(ctx);
            LOG.info("kafkarunner: job completed job_id=" + job.id);
            engine.completeJob(job.id, workerId, result);
        } catch (NonRetryableException e) {
            LOG.warning("kafkarunner: job failed (non-retryable) job_id=" + job.id + ": " + e.getMessage());
            engine.failJob(job.id, workerId, e.getMessage(), false);
        } catch (Exception e) {
            LOG.warning("kafkarunner: job failed (retryable) job_id=" + job.id + ": " + e.getMessage());
            engine.failJob(job.id, workerId, e.getMessage(), true);
        }
    }

    @Override
    public void close() {
        running = false;
        synchronized (consumers) {
            for (KafkaConsumer<?, ?> consumer : consumers) {
                consumer.wakeup();
            }
        }
        executor.shutdown();
        try {
            if (!executor.awaitTermination(30, TimeUnit.SECONDS)) {
                executor.shutdownNow();
            }
        } catch (InterruptedException e) {
            executor.shutdownNow();
            Thread.currentThread().interrupt();
        }
        dedupCache.close();
    }
}
