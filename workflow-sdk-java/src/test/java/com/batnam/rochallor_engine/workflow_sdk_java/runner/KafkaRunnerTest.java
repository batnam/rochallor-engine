package com.batnam.rochallor_engine.workflow_sdk_java.runner;

import com.batnam.rochallor_engine.workflow_sdk_java.client.EngineClient;
import com.batnam.rochallor_engine.workflow_sdk_java.client.Job;
import com.batnam.rochallor_engine.workflow_sdk_java.handler.HandlerRegistry;
import com.batnam.workflow.api.v1.JobDispatchEvent;
import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.junit.jupiter.api.Test;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import static org.junit.jupiter.api.Assertions.*;

class KafkaRunnerTest {
    @Test
    void callbackRetriesWithoutRerunningHandlerOrSendingFailure() throws Exception {
        AtomicInteger callbacks = new AtomicInteger(), failures = new AtomicInteger(), handlers = new AtomicInteger();
        EngineClient engine = new EngineClient() {
            public List<Job> pollJobs(String worker, List<String> types, int max) { return List.of(); }
            public void completeJob(String id, String worker, Map<String,Object> vars) {
                assertEquals(1, vars.get("v"));
                vars.put("v", 9);
                if (callbacks.incrementAndGet() == 1) throw new RuntimeException("lost response");
            }
            public void failJob(String id, String worker, String reason, boolean retryable) { failures.incrementAndGet(); }
        };
        HandlerRegistry registry = new HandlerRegistry();
        registry.register("test", ctx -> { handlers.incrementAndGet(); return new java.util.HashMap<>(Map.of("v", 1)); });
        try (KafkaRunner runner = new KafkaRunner("w", "localhost:1", engine, registry, null)) {
            var method = KafkaRunner.class.getDeclaredMethod("dispatch", ConsumerRecord.class);
            method.setAccessible(true);
            byte[] bytes = JobDispatchEvent.newBuilder().setJobId("j1").setJobType("test").setDedupId("delivery").build().toByteArray();
            method.invoke(runner, new ConsumerRecord<>("workflow.jobs.test", 0, 10, "key", bytes));
            method.invoke(runner, new ConsumerRecord<>("workflow.jobs.test", 0, 11, "key", bytes));
            assertEquals(2, callbacks.get());
            assertEquals(0, failures.get());
            assertEquals(1, handlers.get());
        }
    }

    @Test void pendingRecordBlocksLaterCommitWhilePollingContinues() throws Exception { exercise("retry"); }
    @Test void revocationFencesOldOwnerAndRedelivers() throws Exception { exercise("revoke"); }
    @Test void shutdownDoesNotCommitPendingRecord() throws Exception { exercise("shutdown"); }
    @Test void poisonRecordStopsWithoutCommit() throws Exception { exercise("poison"); }
    @Test void permanentRejectionStopsWithoutCommit() throws Exception { exercise("permanent"); }

    private void exercise(String mode) throws Exception {
        var partition = new org.apache.kafka.common.TopicPartition("workflow.jobs.test", 0);
        var committed = new java.util.concurrent.CopyOnWriteArrayList<Long>();
        var callbackStarted = new java.util.concurrent.CountDownLatch(1);
        var acknowledged = new java.util.concurrent.CountDownLatch(1);
        AtomicInteger callbacks = new AtomicInteger(), handlers = new AtomicInteger(), polls = new AtomicInteger();
        EngineClient engine = new EngineClient() {
            public List<Job> pollJobs(String worker, List<String> types, int max) { return List.of(); }
            public void completeJob(String id, String worker, Map<String,Object> vars) {
                int count = callbacks.incrementAndGet(); callbackStarted.countDown();
                if (mode.equals("permanent")) throw new com.batnam.rochallor_engine.workflow_sdk_java.client.HttpError("denied", 403);
                if (count == 1 || mode.equals("shutdown")) throw new RuntimeException("unavailable");
            }
            public void failJob(String id, String worker, String reason, boolean retryable) { fail("unexpected business failure"); }
        };
        HandlerRegistry registry = new HandlerRegistry();
        registry.register("test", ctx -> { handlers.incrementAndGet(); return Map.of(); });
        var consumer = new org.apache.kafka.clients.consumer.MockConsumer<String,byte[]>(org.apache.kafka.clients.consumer.OffsetResetStrategy.EARLIEST) {
            @Override public synchronized org.apache.kafka.clients.consumer.ConsumerRecords<String,byte[]> poll(java.time.Duration timeout) {
                polls.incrementAndGet();
                try { Thread.sleep(2); } catch (InterruptedException e) { Thread.currentThread().interrupt(); }
                return super.poll(timeout);
            }
            @Override public synchronized void commitSync(Map<org.apache.kafka.common.TopicPartition,org.apache.kafka.clients.consumer.OffsetAndMetadata> offsets, java.time.Duration timeout) {
                offsets.values().forEach(offset -> committed.add(offset.offset()));
                super.commitSync(offsets, timeout);
                acknowledged.countDown();
            }
        };
        byte[] data = mode.equals("poison") ? new byte[]{(byte) 0xff} : JobDispatchEvent.newBuilder().setJobId("j1").setJobType("test").setDedupId("delivery").build().toByteArray();
        consumer.schedulePollTask(() -> {
            consumer.rebalance(List.of(partition));
            consumer.updateBeginningOffsets(Map.of(partition, 10L));
            consumer.addRecord(new ConsumerRecord<>(partition.topic(), 0, 10, "key", data));
        });
        try (KafkaRunner runner = new KafkaRunner("w", "localhost:1", engine, registry, null)) {
            Thread loop = Thread.ofPlatform().start(() -> runner.consume(consumer, partition.topic()));
            try {
                if (mode.equals("poison") || mode.equals("permanent")) {
                    loop.join(2000);
                    assertFalse(loop.isAlive()); assertTrue(committed.isEmpty());
                    return;
                }
                assertTrue(callbackStarted.await(1, java.util.concurrent.TimeUnit.SECONDS));
                int before = polls.get();
                Thread.sleep(25);
                assertTrue(polls.get() > before);
                assertTrue(committed.isEmpty());
                if (mode.equals("revoke")) {
                    consumer.schedulePollTask(() -> {
                        consumer.rebalance(List.of());
                        consumer.rebalance(List.of(partition));
                        consumer.updateBeginningOffsets(Map.of(partition, 10L));
                        consumer.seek(partition, 10);
                        consumer.addRecord(new ConsumerRecord<>(partition.topic(), 0, 10, "key", data));
                    });
                }
                if (mode.equals("shutdown")) {
                    runner.close(); loop.join(1000); assertTrue(committed.isEmpty());
                } else {
                    assertTrue(acknowledged.await(2, java.util.concurrent.TimeUnit.SECONDS));
                    assertEquals(List.of(11L), committed);
                    assertEquals(mode.equals("revoke") ? 2 : 1, handlers.get());
                }
            } finally { runner.close(); loop.join(1000); }
            assertFalse(loop.isAlive());
        }
    }
}
