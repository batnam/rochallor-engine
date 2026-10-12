package com.batnam.rochallor_engine.workflow_sdk_java.runner;

import com.batnam.workflow.api.v1.JobDispatchEvent;
import com.batnam.rochallor_engine.workflow_sdk_java.client.EngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.client.Job;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.HandlerRegistry;
import com.batnam.rochallor_engine.workflow_sdk_java.metrics.Metrics;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import org.apache.kafka.clients.consumer.Consumer;
import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.apache.kafka.clients.consumer.ConsumerRebalanceListener;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.clients.consumer.OffsetAndMetadata;
import org.apache.kafka.common.TopicPartition;
import org.apache.kafka.common.errors.WakeupException;
import org.apache.kafka.common.serialization.ByteArrayDeserializer;
import org.apache.kafka.common.serialization.StringDeserializer;

import java.time.Duration;
import java.util.Collection;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Properties;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.logging.Level;
import java.util.logging.Logger;

/** Poll membership independently of handlers; commit only acknowledged records. */
public class KafkaRunner implements AutoCloseable {
    private static final Logger LOG = Logger.getLogger(KafkaRunner.class.getName());
    private static final ObjectMapper JSON = new ObjectMapper();
    private final String workerId;
    private final EngineClient engine;
    private final HandlerRegistry registry;
    private final Metrics metrics;
    private final DeduplicationCache dedupCache = new DeduplicationCache(Duration.ofMinutes(10));
    private final Properties kafkaProps = new Properties();
    private final Set<Consumer<String, byte[]>> consumers = ConcurrentHashMap.newKeySet();
    private final ExecutorService loops = Executors.newVirtualThreadPerTaskExecutor();
    private final ExecutorService handlers = Executors.newVirtualThreadPerTaskExecutor();
    private final AtomicBoolean stopping = new AtomicBoolean();

    private record Pending(ConsumerRecord<String, byte[]> record, Future<?> work, AtomicBoolean owner) { }

    public KafkaRunner(String workerId, String brokers, EngineClient engine, HandlerRegistry registry, Properties extraKafkaProps) {
        this(workerId, brokers, engine, registry, extraKafkaProps, new Metrics(new SimpleMeterRegistry()));
    }

    public KafkaRunner(String workerId, String brokers, EngineClient engine, HandlerRegistry registry, Properties extraKafkaProps, Metrics metrics) {
        this.workerId = workerId;
        this.engine = engine;
        this.registry = registry;
        this.metrics = metrics;
        kafkaProps.put(ConsumerConfig.BOOTSTRAP_SERVERS_CONFIG, brokers);
        kafkaProps.put(ConsumerConfig.AUTO_OFFSET_RESET_CONFIG, "earliest");
        kafkaProps.put(ConsumerConfig.PARTITION_ASSIGNMENT_STRATEGY_CONFIG, "org.apache.kafka.clients.consumer.CooperativeStickyAssignor");
        if (extraKafkaProps != null) kafkaProps.putAll(extraKafkaProps);
        kafkaProps.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class.getName());
        kafkaProps.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG, ByteArrayDeserializer.class.getName());
        kafkaProps.put(ConsumerConfig.ENABLE_AUTO_COMMIT_CONFIG, "false");
        kafkaProps.put(ConsumerConfig.MAX_POLL_RECORDS_CONFIG, "1");
    }

    public void start() {
        for (String jobType : registry.jobTypes()) {
            loops.submit(() -> {
                Properties props = new Properties(); props.putAll(kafkaProps);
                props.put(ConsumerConfig.GROUP_ID_CONFIG, "workflow.workers." + jobType);
                try { consume(new KafkaConsumer<>(props), "workflow.jobs." + jobType); }
                catch (Exception error) { fail("Kafka consumer initialization failed", error); }
            });
        }
    }

    // The package-private loop also accepts Kafka's MockConsumer for protocol tests.
    void consume(Consumer<String, byte[]> consumer, String topic) {
        Map<TopicPartition, AtomicBoolean> owners = new HashMap<>();
        Map<TopicPartition, Pending> pending = new HashMap<>();
        consumers.add(consumer);
        ConsumerRebalanceListener assignments = new ConsumerRebalanceListener() {
            @Override public void onPartitionsAssigned(Collection<TopicPartition> partitions) {
                for (var partition : partitions) owners.computeIfAbsent(partition, key -> new AtomicBoolean(true));
                var retained = partitions.stream().filter(pending::containsKey).toList();
                if (!retained.isEmpty()) consumer.pause(retained);
            }
            @Override public void onPartitionsRevoked(Collection<TopicPartition> partitions) {
                for (var partition : partitions) {
                    var owner = owners.remove(partition);
                    if (owner != null) owner.set(false);
                    var work = pending.remove(partition);
                    if (work != null) work.work().cancel(true);
                }
            }
            @Override public void onPartitionsLost(Collection<TopicPartition> partitions) { onPartitionsRevoked(partitions); }
        };
        try {
            consumer.subscribe(List.of(topic), assignments);
            while (!stopping.get()) {
                var completed = pending.entrySet().iterator();
                while (completed.hasNext()) {
                    var entry = completed.next(); var work = entry.getValue();
                    if (!work.work().isDone()) continue;
                    if (!work.owner().get() || owners.get(entry.getKey()) != work.owner()) { completed.remove(); continue; }
                    work.work().get(); // Permanent/decoding failure stops without committing.
                    if (stopping.get()) break;
                    consumer.commitSync(Map.of(entry.getKey(), new OffsetAndMetadata(work.record().offset() + 1)), Duration.ofSeconds(5));
                    consumer.resume(Set.of(entry.getKey()));
                    completed.remove();
                }
                if (stopping.get()) break;
                for (var record : consumer.poll(Duration.ofMillis(100))) {
                    var partition = new TopicPartition(record.topic(), record.partition());
                    var owner = owners.get(partition);
                    if (owner == null || !owner.get() || pending.containsKey(partition)) {
                        throw new IllegalStateException("record has no available partition owner: " + partition);
                    }
                    consumer.pause(Set.of(partition));
                    LOG.info("kafkarunner: topic=" + record.topic() + " partition=" + record.partition() + " offset=" + record.offset());
                    var work = handlers.submit(() -> { dispatch(record, owner); return null; });
                    pending.put(partition, new Pending(record, work, owner));
                }
            }
        } catch (WakeupException stopped) {
            if (!stopping.get()) fail("Kafka consumer woke unexpectedly topic=" + topic, stopped);
        } catch (Exception error) {
            fail("Kafka stopped with unacknowledged work topic=" + topic, error);
        } finally {
            owners.values().forEach(owner -> owner.set(false));
            pending.values().forEach(work -> work.work().cancel(true));
            consumers.remove(consumer);
            consumer.close(Duration.ofSeconds(5));
        }
    }

    private void fail(String message, Exception error) {
        LOG.log(Level.SEVERE, message, error);
        stopping.set(true);
        consumers.forEach(Consumer::wakeup);
    }

    private void dispatch(ConsumerRecord<String, byte[]> record) throws Exception {
        dispatch(record, new AtomicBoolean(true));
    }

    private void dispatch(ConsumerRecord<String, byte[]> record, AtomicBoolean owner) throws Exception {
        if (stopping.get() || !owner.get()) throw new InterruptedException("partition ownership ended");
        JobDispatchEvent event;
        try {
            if (record.value() == null) throw new IllegalArgumentException("empty Kafka record");
            event = JobDispatchEvent.parseFrom(record.value());
            if (event.getJobId().isEmpty()) throw new IllegalArgumentException("Kafka record has no job ID");
        } catch (Exception invalid) { metrics.kafkaRecordErrors.increment(); throw invalid; }
        if (!event.getDedupId().isEmpty() && dedupCache.contains(event.getDedupId())) return;
        LOG.info("kafkarunner: job_id=" + event.getJobId() + " dedup_id=" + event.getDedupId());
        Map<String, Object> variables = Map.of();
        Callbacks.Outcome invalid = null;
        try {
            if (!event.getJobPayload().isEmpty()) {
                variables = JSON.readValue(event.getJobPayload().toByteArray(), new TypeReference<>() {});
                if (variables == null) throw new IllegalArgumentException("payload must be an object");
            }
        } catch (Exception malformed) { invalid = new Callbacks.Outcome("fail", "invalid job payload", false); }
        Job job = new Job(); job.id = event.getJobId(); job.jobType = event.getJobType();
        job.instanceId = event.getInstanceId(); job.stepExecutionId = event.getStepExecutionId();
        job.retriesRemaining = event.getRetriesRemaining(); job.variables = variables;
        var outcome = invalid != null ? invalid : Callbacks.execute(registry, job, metrics);
        Callbacks.deliver(engine, workerId, job.id, outcome, () -> !stopping.get() && owner.get(), null, true, metrics, "kafka");
        dedupCache.mark(event.getDedupId());
    }

    @Override public void close() {
        stopping.set(true);
        consumers.forEach(Consumer::wakeup);
        long end = System.nanoTime() + TimeUnit.SECONDS.toNanos(30);
        loops.shutdown();
        try {
            if (!loops.awaitTermination(30, TimeUnit.SECONDS)) loops.shutdownNow();
            handlers.shutdown();
            if (!handlers.awaitTermination(Math.max(0, end - System.nanoTime()), TimeUnit.NANOSECONDS)) handlers.shutdownNow();
        } catch (InterruptedException interrupted) {
            loops.shutdownNow(); handlers.shutdownNow(); Thread.currentThread().interrupt();
        }
        dedupCache.close();
    }
}
