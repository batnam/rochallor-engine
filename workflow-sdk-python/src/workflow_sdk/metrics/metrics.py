"""Prometheus metrics for the workflow Python SDK runner.

Metric names are identical to the Go, Java, and Node SDKs so that a single
Prometheus/Grafana configuration covers all language implementations.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import TYPE_CHECKING

from prometheus_client import CollectorRegistry, Counter, Histogram
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
