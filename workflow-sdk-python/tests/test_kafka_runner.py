from threading import Event
from unittest.mock import MagicMock

from prometheus_client import CollectorRegistry
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.runner.kafka_runner import KafkaRunner
from workflow_sdk.internal.gen.workflow.v1 import engine_pb2


def event(job_id="j1", payload=b"{}"):
    return engine_pb2.JobDispatchEvent(job_id=job_id, job_type="test", instance_id="i1", dedup_id=job_id, job_payload=payload).SerializeToString()


def test_kafka_retries_completion_without_failure_or_handler_rerun():
    client = MagicMock()
    client.complete_job.side_effect = [RuntimeError("lost response"), None]
    registry = HandlerRegistry()
    handler = MagicMock(return_value={"result": 1})
    registry.register("test", handler)
    runner = KafkaRunner("w", "localhost:1", client, registry, dedup_window_seconds=.01)
    try:
        runner._dispatch(event())
        assert client.complete_job.call_count == 2
        assert handler.call_count == 1
        client.fail_job.assert_not_called()
    finally:
        runner.stop()
        runner.dedup_cache.close()


def test_kafka_immutable_failure_payload_and_invalid_record():
    import pytest
    from workflow_sdk.errors import NonRetryableError
    client = MagicMock()
    client.fail_job.side_effect = [RuntimeError("lost response"), None]
    registry = HandlerRegistry()
    handler = MagicMock(side_effect=NonRetryableError("business"))
    registry.register("test", handler)
    runner = KafkaRunner("w", "localhost:1", client, registry)
    try:
        runner._dispatch(event())
        assert handler.call_count == 1
        assert all(call.args == ("j1", "w", "business", False) for call in client.fail_job.call_args_list)
        with pytest.raises(Exception):
            runner._dispatch(b"\xff")
        with pytest.raises(ValueError, match="job ID"):
            runner._dispatch(b"")
        client.fail_job.reset_mock(side_effect=True)
        runner._dispatch(event("invalid-json", b"[]"))
        client.fail_job.assert_called_once_with("invalid-json", "w", "invalid job payload", False)
    finally:
        runner.dedup_cache.close()


def test_poll_continues_during_pending_and_commits_only_acknowledged_record(monkeypatch):
    _exercise_consumer(monkeypatch, "retry")


def test_revoked_owner_cannot_commit_and_new_owner_redelivers(monkeypatch):
    _exercise_consumer(monkeypatch, "revoke")


def test_shutdown_cancels_pending_without_commit(monkeypatch):
    _exercise_consumer(monkeypatch, "shutdown")


def test_poison_stops_without_commit(monkeypatch):
    _exercise_consumer(monkeypatch, "poison")


def _exercise_consumer(monkeypatch, mode):
    import time
    from types import SimpleNamespace
    import workflow_sdk.runner.kafka_runner as module
    from confluent_kafka import TopicPartition
    client = MagicMock()
    first = Event()
    registry = HandlerRegistry()
    handler = MagicMock(return_value={"v": 1})
    registry.register("test", handler)
    runner = KafkaRunner("w", "localhost:1", client, registry, extra_kafka_config={"enable.auto.commit": True, "enable.auto.offset.store": True})
    def completion(*args):
        if not first.is_set():
            first.set()
            raise RuntimeError("lost response")
    client.complete_job.side_effect = completion
    committed = []
    part = TopicPartition("workflow.jobs.test", 0)
    message = SimpleNamespace(topic=lambda: part.topic, partition=lambda: 0, offset=lambda: 10, error=lambda: None, value=lambda: b"\xff" if mode == "poison" else event())
    class Consumer:
        def __init__(self, config):
            assert config["enable.auto.commit"] is False
            assert config["enable.auto.offset.store"] is False
            self.polls = 0
            self.phase = 0
        def subscribe(self, topics, **callbacks):
            self.callbacks = callbacks
            callbacks["on_assign"](self, [part])
        def poll(self, timeout):
            time.sleep(.003)
            self.polls += 1
            assert self.polls < 400, "consumer stalled"
            if self.phase == 0:
                self.phase = 1
                return message
            if first.is_set() and self.phase == 1:
                if mode == "shutdown":
                    runner.stop()
                if mode == "revoke":
                    self.callbacks["on_revoke"](self, [part])
                    assert not committed
                    self.callbacks["on_assign"](self, [part])
                    self.phase = 2
                    return message
        def pause(self, partitions):
            pass
        def resume(self, partitions):
            pass
        def commit(self, *, offsets, asynchronous):
            assert self.polls >= 2
            assert asynchronous is False
            committed.extend((p.partition, p.offset) for p in offsets)
            runner.stop()
            return offsets
        def close(self):
            pass
    monkeypatch.setattr(module, "Consumer", Consumer)
    runner.running = True
    try:
        runner._consume_loop("test")
        if mode in ("shutdown", "poison"):
            assert committed == []
        else:
            assert committed == [(0, 11)]
            assert handler.call_count == (2 if mode == "revoke" else 1)
            assert runner._errors.empty()
        client.fail_job.assert_not_called()
    finally:
        runner.stop()
        runner.dedup_cache.close()
