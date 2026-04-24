"""Tests for the worker Runner.

Most tests drive _dispatch() directly (no timing dependency).
Loop-level tests use a very short poll_interval and explicit stop events.
"""

from __future__ import annotations

import threading
import time
from typing import Any
from unittest.mock import MagicMock, call

import pytest
from prometheus_client import CollectorRegistry

from workflow_sdk.errors import NonRetryableError
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.metrics.metrics import Metrics
from workflow_sdk.runner.runner import Runner


# ------------------------------------------------------------------ #
# Helpers                                                              #
# ------------------------------------------------------------------ #

def _metrics() -> Metrics:
    return Metrics(registry=CollectorRegistry())


def _make_runner(
    client: Any,
    registry: HandlerRegistry,
    worker_id: str = "test-worker",
    parallelism: int = 4,
    poll_interval: float = 0.02,
    m: Metrics | None = None,
) -> Runner:
    return Runner(
        client=client,
        registry=registry,
        worker_id=worker_id,
        parallelism=parallelism,
        poll_interval=poll_interval,
        metrics=m or _metrics(),
    )


def _make_job(
    job_id: str = "job-1",
    job_type: str = "test-type",
    variables: dict | None = None,
    retries_remaining: int = 3,
) -> dict:
    return {
        "id": job_id,
        "jobType": job_type,
        "instanceId": "inst-1",
        "stepId": "step-1",
        "stepExecutionId": "exec-1",
        "retriesRemaining": retries_remaining,
        "variables": variables or {},
        "lockExpiresAt": "2099-01-01T00:00:00Z",
    }


# ------------------------------------------------------------------ #
# Unit tests: _dispatch (no threading, fully synchronous)            #
# ------------------------------------------------------------------ #

class TestRunnerDispatch:
    """Test _dispatch() directly — fast, no timing dependencies."""

    def test_successful_handler_calls_complete_job(self) -> None:
        client = MagicMock()
        client.complete_job.return_value = None
        registry = HandlerRegistry()
        registry.register("test-type", lambda ctx: {"result": "ok"})

        runner = _make_runner(client, registry)
        runner._dispatch(_make_job())

        client.complete_job.assert_called_once_with("job-1", "test-worker", {"result": "ok"})
        client.fail_job.assert_not_called()

    def test_handler_returning_none_calls_complete_with_empty_dict(self) -> None:
        client = MagicMock()
        registry = HandlerRegistry()
        registry.register("test-type", lambda ctx: None)

        runner = _make_runner(client, registry)
        runner._dispatch(_make_job())

        client.complete_job.assert_called_once_with("job-1", "test-worker", {})

    def test_non_retryable_error_calls_fail_retryable_false(self) -> None:
        client = MagicMock()
        registry = HandlerRegistry()

        def bad_handler(ctx: dict) -> dict:
            raise NonRetryableError("permanently broken")

        registry.register("test-type", bad_handler)
        runner = _make_runner(client, registry)
        runner._dispatch(_make_job())

        client.complete_job.assert_not_called()
        client.fail_job.assert_called_once()
        _job_id, _worker_id, msg, retryable = client.fail_job.call_args[0]
        assert retryable is False
        assert "permanently broken" in msg

    def test_general_exception_calls_fail_retryable_true(self) -> None:
        client = MagicMock()
        registry = HandlerRegistry()

        def exploding(ctx: dict) -> dict:
            raise ValueError("unexpected")

        registry.register("test-type", exploding)
        runner = _make_runner(client, registry)
        runner._dispatch(_make_job())

        client.complete_job.assert_not_called()
        client.fail_job.assert_called_once()
        _job_id, _worker_id, msg, retryable = client.fail_job.call_args[0]
        assert retryable is True

    def test_no_handler_calls_fail_retryable_false(self) -> None:
        client = MagicMock()
        registry = HandlerRegistry()
        # no handler registered for "test-type"

        runner = _make_runner(client, registry)
        runner._dispatch(_make_job(job_type="test-type"))

        client.complete_job.assert_not_called()
        client.fail_job.assert_called_once()
        _job_id, _worker_id, msg, retryable = client.fail_job.call_args[0]
        assert retryable is False

    def test_job_context_contains_correct_fields(self) -> None:
        client = MagicMock()
        received_ctx: dict = {}
        registry = HandlerRegistry()

        def capture(ctx: dict) -> dict:
            received_ctx.update(ctx)
            return {}

        registry.register("test-type", capture)
        runner = _make_runner(client, registry)
        runner._dispatch(_make_job(variables={"orderId": "ORD-1"}))

        assert received_ctx["id"] == "job-1"
        assert received_ctx["jobType"] == "test-type"
        assert received_ctx["variables"] == {"orderId": "ORD-1"}
        assert received_ctx["retriesRemaining"] == 3


# ------------------------------------------------------------------ #
# Integration tests: full run() loop (short durations)               #
# ------------------------------------------------------------------ #

class TestRunnerLoop:
    def test_polls_with_registered_job_types(self) -> None:
        """Runner calls poll_jobs with the registered job type strings."""
        client = MagicMock()
        stop = threading.Event()
        polled_types: list[list[str]] = []

        def capture_and_stop(worker_id: str, job_types: list, max_jobs: int) -> list:
            polled_types.append(list(job_types))
            stop.set()
            return []

        client.poll_jobs.side_effect = capture_and_stop

        registry = HandlerRegistry()
        registry.register("type-a", lambda ctx: None)
        registry.register("type-b", lambda ctx: None)

        runner = _make_runner(client, registry, poll_interval=0.01)
        runner.run(stop_event=stop)

        assert len(polled_types) >= 1
        assert set(polled_types[0]) == {"type-a", "type-b"}

    def test_stop_event_terminates_loop(self) -> None:
        """Setting stop_event causes run() to exit within reasonable time."""
        client = MagicMock()
        client.poll_jobs.return_value = []
        stop = threading.Event()

        registry = HandlerRegistry()
        registry.register("t", lambda ctx: None)

        runner = _make_runner(client, registry, poll_interval=0.01)

        t = threading.Thread(target=runner.run, kwargs={"stop_event": stop})
        t.start()

        time.sleep(0.05)
        stop.set()
        t.join(timeout=3.0)
        assert not t.is_alive(), "Runner did not stop within 3 seconds after stop event"

    def test_lock_conflicts_incremented_for_empty_polls(self) -> None:
        """Each empty poll round increments lock_conflicts counter."""
        client = MagicMock()
        stop = threading.Event()
        m = _metrics()

        poll_count = [0]

        def count_and_stop(worker_id: str, job_types: list, max_jobs: int) -> list:
            poll_count[0] += 1
            if poll_count[0] >= 2:
                stop.set()
            return []

        client.poll_jobs.side_effect = count_and_stop

        registry = HandlerRegistry()
        registry.register("t", lambda ctx: None)

        runner = Runner(
            client=client,
            registry=registry,
            worker_id="w",
            poll_interval=0.01,
            metrics=m,
        )
        runner.run(stop_event=stop)

        # Get lock_conflicts counter value
        total = _counter_value(m.lock_conflicts)
        assert total >= 1, f"Expected lock_conflicts >= 1, got {total}"


# ------------------------------------------------------------------ #
# Helper                                                               #
# ------------------------------------------------------------------ #

def _counter_value(counter: Any) -> float:
    """Extract current value from a prometheus_client Counter."""
    for family in counter.collect():
        for sample in family.samples:
            if sample.name.endswith("_total"):
                return sample.value
    return 0.0
