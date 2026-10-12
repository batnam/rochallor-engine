"""Kafka delivery: poll membership while retaining one unacknowledged record per partition."""
from __future__ import annotations

import json
import logging
import signal
import threading
from concurrent.futures import Future, ThreadPoolExecutor
from queue import SimpleQueue
from typing import Any

from confluent_kafka import Consumer, KafkaError, KafkaException, TopicPartition
from prometheus_client import CollectorRegistry

from workflow_sdk.client.interface import EngineClient
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.internal.gen.workflow.v1 import engine_pb2
from workflow_sdk.metrics.metrics import Metrics
from workflow_sdk.runner.callback import Outcome, deliver, execute
from workflow_sdk.runner.deduplication_cache import DeduplicationCache

log = logging.getLogger(__name__)


class KafkaRunner:
    def __init__(self, worker_id: str, brokers: str, client: EngineClient,
                 registry: HandlerRegistry, dedup_window_seconds: float = 600.0,
                 extra_kafka_config: dict[str, Any] | None = None,
                 metrics: Metrics | None = None) -> None:
        self.worker_id, self.brokers = worker_id, brokers
        self.client, self.registry = client, registry
        self.dedup_cache = DeduplicationCache(dedup_window_seconds)
        self.metrics = metrics or Metrics(registry=CollectorRegistry())
        self.running = False
        self.consumers: list[Consumer] = []
        self._errors: SimpleQueue[Exception] = SimpleQueue()
        self.kafka_config = {
            'bootstrap.servers': brokers, 'auto.offset.reset': 'earliest',
            'partition.assignment.strategy': 'cooperative-sticky',
            'socket.timeout.ms': 5000,
            **(extra_kafka_config or {}),
            # Ownership and offset policy cannot be overridden by caller config.
            'enable.auto.commit': False, 'enable.auto.offset.store': False,
        }

    def run(self) -> None:
        self.running = True
        threads = [threading.Thread(target=self._consume_loop, args=(kind,),
                                    name=f"kafka-consumer-{kind}") for kind in self.registry.job_types()]
        if threading.current_thread() is threading.main_thread():
            signal.signal(signal.SIGINT, lambda *_: self.stop())
            signal.signal(signal.SIGTERM, lambda *_: self.stop())
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.dedup_cache.close()
        if not self._errors.empty():
            raise self._errors.get()

    def stop(self) -> None:
        self.running = False

    def _consume_loop(self, job_type: str) -> None:
        topic = f"workflow.jobs.{job_type}"
        owners: dict[tuple[str, int], threading.Event] = {}
        pending: dict[tuple[str, int], tuple[Any, Future[None], threading.Event]] = {}
        consumer = None
        executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix=f"handler-{job_type}")

        def assigned(_consumer: Any, partitions: list[TopicPartition]) -> None:
            for part in partitions:
                owners.setdefault((part.topic, part.partition), threading.Event())
            retained = [part for part in partitions if (part.topic, part.partition) in pending]
            if retained:
                _consumer.pause(retained)

        def revoked(_consumer: Any, partitions: list[TopicPartition]) -> None:
            for part in partitions:
                key = (part.topic, part.partition)
                owner = owners.pop(key, None)
                if owner is not None:
                    owner.set()
                old = pending.pop(key, None)
                if old is not None:
                    old[1].cancel()

        try:
            consumer = Consumer({**self.kafka_config, 'group.id': f"workflow.workers.{job_type}"})
            self.consumers.append(consumer)
            consumer.subscribe([topic], on_assign=assigned, on_revoke=revoked, on_lost=revoked)
            while self.running:
                for key, (message, future, owner) in list(pending.items()):
                    if not future.done():
                        continue
                    if owner.is_set() or owners.get(key) is not owner:
                        del pending[key]
                        continue
                    future.result()  # Failure stops the consumer with offset uncommitted.
                    offsets = consumer.commit(offsets=[TopicPartition(key[0], key[1], message.offset() + 1)], asynchronous=False)
                    for offset in offsets:
                        if offset.error is not None:
                            raise KafkaException(offset.error)
                    del pending[key]
                    consumer.resume([TopicPartition(*key)])
                message = consumer.poll(timeout=0.1)
                if message is None:
                    continue
                error = message.error()
                if error is not None:
                    if error.code() == KafkaError._PARTITION_EOF:
                        continue
                    raise KafkaException(error)
                record_topic, partition = message.topic(), message.partition()
                assert record_topic is not None and partition is not None
                key = (record_topic, partition)
                current_owner = owners.get(key)
                if current_owner is None or current_owner.is_set() or key in pending:
                    raise RuntimeError(f"Kafka record has no available owner: {key}")
                consumer.pause([TopicPartition(*key)])
                log.info("kafkarunner: topic=%s partition=%s offset=%s", *key, message.offset())
                pending[key] = (message, executor.submit(self._dispatch, message.value(), current_owner), current_owner)
        except Exception as exc:
            log.exception("Kafka consumer stopped with unacknowledged work topic=%s", topic)
            self._errors.put(exc)
            self.stop()
        finally:
            for owner in owners.values():
                owner.set()
            for _, future, _ in pending.values():
                future.cancel()
            if consumer is not None:
                consumer.close()
            # An arbitrary handler thread cannot be forcibly terminated.
            executor.shutdown(wait=True, cancel_futures=True)

    def _dispatch(self, data: bytes | None, owner: threading.Event | None = None) -> None:
        owner = owner if owner is not None else threading.Event()
        if owner.is_set():
            raise RuntimeError("partition ownership ended")
        try:
            if not data:
                raise ValueError("Kafka record has no job ID")
            event = engine_pb2.JobDispatchEvent()
            event.ParseFromString(data)
            if not event.job_id:
                raise ValueError("Kafka record has no job ID")
        except Exception:
            self.metrics.kafka_record_errors.inc()
            raise
        if event.dedup_id and self.dedup_cache.contains(event.dedup_id):
            return
        log.info("kafkarunner: job_id=%s dedup_id=%s", event.job_id, event.dedup_id)
        invalid = None
        variables = {}
        try:
            if event.job_payload:
                variables = json.loads(event.job_payload)
                if not isinstance(variables, dict):
                    raise ValueError("payload must be an object")
        except (ValueError, UnicodeError):
            invalid = Outcome("fail", "invalid job payload")
        job = {"id": event.job_id, "jobType": event.job_type, "instanceId": event.instance_id,
               "stepExecutionId": event.step_execution_id, "retriesRemaining": event.retries_remaining, "variables": variables}
        outcome = invalid or execute(self.registry, job, self.metrics)
        deliver(self.client, self.worker_id, event.job_id, outcome, owner, self.metrics, "kafka")
        self.dedup_cache.mark(event.dedup_id)
