import json
import logging
import threading
import signal
from typing import List, Optional, Dict, Any
from confluent_kafka import Consumer, KafkaError, KafkaException

from workflow_sdk.client.interface import EngineClient
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.runner.deduplication_cache import DeduplicationCache
from workflow_sdk.errors import NonRetryableError

# Try to import generated proto, fallback to a dummy if not found during implementation
try:
    from workflow_sdk.internal.gen import engine_pb2
except ImportError:
    # Dummy class to avoid immediate crash if stubs aren't generated yet
    class engine_pb2:
        class JobDispatchEvent:
            @staticmethod
            def IsDefined(): return False

log = logging.getLogger(__name__)

class KafkaRunner:
    def __init__(
        self,
        worker_id: str,
        brokers: str,
        client: EngineClient,
        registry: HandlerRegistry,
        dedup_window_seconds: float = 600.0,
        extra_kafka_config: Optional[Dict[str, Any]] = None
    ):
        self.worker_id = worker_id
        self.brokers = brokers
        self.client = client
        self.registry = registry
        self.dedup_cache = DeduplicationCache(dedup_window_seconds)
        self.running = False
        self.consumers: List[Consumer] = []
        
        self.kafka_config = {
            'bootstrap.servers': brokers,
            'auto.offset.reset': 'earliest',
            'enable.auto.commit': False,
            'partition.assignment.strategy': 'cooperative-sticky',
        }
        if extra_kafka_config:
            self.kafka_config.update(extra_kafka_config)

    def run(self):
        self.running = True
        job_types = self.registry.job_types()
        log.info("KafkaRunner starting: workerId=%s jobTypes=%s", self.worker_id, job_types)

        threads = []
        for job_type in job_types:
            t = threading.Thread(target=self._consume_loop, args=(job_type,), name=f"kafka-consumer-{job_type}")
            t.start()
            threads.append(t)

        # Handle termination signals
        def handle_sig(sig, frame):
            log.info("Signal received, stopping...")
            self.stop()

        signal.signal(signal.SIGINT, handle_sig)
        signal.signal(signal.SIGTERM, handle_sig)

        for t in threads:
            t.join()
        
        self.dedup_cache.close()

    def stop(self):
        self.running = False

    def _consume_loop(self, job_type: str):
        topic = f"workflow.jobs.{job_type}"
        group = f"workflow.workers.{job_type}"
        
        conf = self.kafka_config.copy()
        conf['group.id'] = group
        
        consumer = Consumer(conf)
        self.consumers.append(consumer)
        
        try:
            consumer.subscribe([topic])
            
            while self.running:
                msg = consumer.poll(timeout=0.5)
                if msg is None:
                    continue
                
                if msg.error():
                    if msg.error().code() == KafkaError._PARTITION_EOF:
                        continue
                    else:
                        log.error("Kafka error: %s", msg.error())
                        continue

                try:
                    self._dispatch(msg.value())
                    consumer.commit(msg)
                except Exception as e:
                    log.exception("Error dispatching Kafka record from %s: %s", topic, e)
        finally:
            consumer.close()

    def _dispatch(self, data: bytes):
        if not hasattr(engine_pb2, 'JobDispatchEvent'):
            log.error("JobDispatchEvent proto not found. Cannot decode Kafka message.")
            return

        event = engine_pb2.JobDispatchEvent()
        event.ParseFromString(data)
        
        if event.dedup_id and self.dedup_cache.seen_recently(event.dedup_id):
            log.debug("kafkarunner: skipping duplicate job_id=%s dedup_id=%s", event.job_id, event.dedup_id)
            return

        log.info("kafkarunner: dispatching job_id=%s job_type=%s", event.job_id, event.job_type)

        handler = self.registry.get(event.job_type)
        if not handler:
            log.warning("kafkarunner: no handler for jobType=%s jobId=%s", event.job_type, event.job_id)
            self.client.fail_job(event.job_id, self.worker_id, f"no handler registered for {event.job_type}", False)
            return

        variables = {}
        if event.job_payload:
            try:
                variables = json.loads(event.job_payload.decode('utf-8'))
            except Exception as e:
                log.error("kafkarunner: failed to parse job payload for %s: %s", event.job_id, e)

        ctx = {
            "id": event.job_id,
            "jobType": event.job_type,
            "instanceId": event.instance_id,
            "stepExecutionId": event.step_execution_id,
            "retriesRemaining": event.retries_remaining,
            "variables": variables,
        }

        try:
            result = handler(ctx)
            log.info("kafkarunner: job completed job_id=%s", event.job_id)
            self.client.complete_job(event.job_id, self.worker_id, result or {})
        except NonRetryableError as e:
            log.warning("kafkarunner: job failed (non-retryable) job_id=%s: %s", event.job_id, e)
            self.client.fail_job(event.job_id, self.worker_id, str(e), False)
        except Exception as e:
            log.exception("kafkarunner: job failed (retryable) job_id=%s: %s", event.job_id, e)
            self.client.fail_job(event.job_id, self.worker_id, str(e), True)
