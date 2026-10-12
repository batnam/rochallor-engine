"""Prometheus metrics for the workflow Python SDK runner.

Metric names are identical to the Go, Java, and Node SDKs so that a single
Prometheus/Grafana configuration covers all language implementations.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import TYPE_CHECKING
import threading
from functools import partial
import time
from collections.abc import Callable

from prometheus_client import CollectorRegistry, Counter, Histogram, Gauge
from prometheus_client import REGISTRY as DEFAULT_REGISTRY

if TYPE_CHECKING:
    pass

_DEFAULT_BUCKETS = (
    0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0
)


@dataclass
class Metrics:
    """Container for all Prometheus metrics used by the SDK runner.

    Pass a custom ``registry`` to isolate metrics in tests — this prevents
    duplicate-registration errors across test cases.

    Example::

        from prometheus_client import CollectorRegistry
        from workflow_sdk.metrics import Metrics

        registry = CollectorRegistry()
        m = Metrics(registry=registry)
    """

    registry: CollectorRegistry = field(default_factory=lambda: DEFAULT_REGISTRY)

    # Declared after registry via __post_init__
    poll_latency: Histogram = field(init=False)
    lock_conflicts: Counter = field(init=False)
    handler_latency: Histogram = field(init=False)
    retries_total: Counter = field(init=False)
    jobs_completed: Counter = field(init=False)

    def __post_init__(self) -> None:
        reg = self.registry
        self.heartbeats = Counter("workflow_sdk_heartbeat_total", "Lease renewals", ["result"], registry=reg)
        self._pending_lock = threading.Lock()
        self._pending: dict[tuple[str, str], dict[object, float]] = {}
        self.callback_retries = Counter("workflow_sdk_callback_retries_total", "Callback transport retries", ["dispatch", "callback"], registry=reg)
        self.callback_rejected = Counter("workflow_sdk_callback_rejected_total", "Permanently rejected callbacks", ["dispatch", "callback"], registry=reg)
        self.callback_pending = Gauge("workflow_sdk_callback_pending", "Unacknowledged callbacks", ["dispatch", "callback"], registry=reg)
        self.callback_oldest = Gauge("workflow_sdk_callback_oldest_pending_seconds", "Age of oldest pending callback", ["dispatch", "callback"], registry=reg)
        self.kafka_record_errors = Counter("workflow_sdk_kafka_record_errors_total", "Invalid Kafka records blocking delivery", registry=reg)
        for mode in ("polling", "kafka"):
            for kind in ("complete", "fail"):
                self._pending[(mode, kind)] = {}
                self.callback_oldest.labels(dispatch=mode, callback=kind).set_function(
                    partial(self._oldest, (mode, kind)))

        self.poll_latency = Histogram(
            "workflow_sdk_poll_latency_seconds",
            "Latency of PollJobs calls in seconds.",
            buckets=_DEFAULT_BUCKETS,
            registry=reg,
        )
        self.lock_conflicts = Counter(
            "workflow_sdk_lock_conflicts_total",
            "Number of poll rounds that returned zero jobs.",
            registry=reg,
        )
        self.handler_latency = Histogram(
            "workflow_sdk_handler_latency_seconds",
            "Duration of handler executions in seconds.",
            labelnames=["job_type"],
            buckets=_DEFAULT_BUCKETS,
            registry=reg,
        )
        self.retries_total = Counter(
            "workflow_sdk_retries_total",
            "Number of jobs retried by the SDK runner.",
            labelnames=["job_type"],
            registry=reg,
        )
        self.jobs_completed = Counter(
            "workflow_sdk_jobs_completed_total",
            "Number of jobs completed, labelled by outcome: success or failure.",
            labelnames=["job_type", "outcome"],
            registry=reg,
        )

    def _oldest(self, key: tuple[str, str]) -> float:
        with self._pending_lock:
            starts = self._pending[key]
            return time.monotonic() - min(starts.values()) if starts else 0.0

    def track_callback(self, mode: str, kind: str) -> Callable[[], None]:
        key, token = (mode, kind), object()
        with self._pending_lock:
            self._pending[key][token] = time.monotonic()
        gauge = self.callback_pending.labels(dispatch=mode, callback=kind)
        gauge.inc()
        def finish() -> None:
            with self._pending_lock:
                del self._pending[key][token]
            gauge.dec()
        return finish
