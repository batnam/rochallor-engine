"""Tests for the Metrics class — metric names, labels, and registry isolation."""

from __future__ import annotations

import pytest
from prometheus_client import CollectorRegistry

from workflow_sdk.metrics.metrics import Metrics


def _collect_names(registry: CollectorRegistry) -> set[str]:
    """Return all metric family names AND all sample names (covers _total suffix)."""
    names: set[str] = set()
    for family in registry.collect():
        names.add(family.name)
        for sample in family.samples:
            names.add(sample.name)
    return names


def _collect_family_names(registry: CollectorRegistry) -> set[str]:
    """Return only metric family base names (without _total suffix)."""
    return {metric.name for metric in registry.collect()}


class TestMetricsRegistration:
    def test_metric_names_match_convention(self) -> None:
        reg = CollectorRegistry()
        m = Metrics(registry=reg)
        # Trigger labeled counters so they appear in samples
        m.handler_latency.labels(job_type="t").observe(0.1)
        m.retries_total.labels(job_type="t").inc()
        m.jobs_completed.labels(job_type="t", outcome="success").inc()
        names = _collect_names(reg)
        assert "workflow_sdk_poll_latency_seconds" in names
        assert "workflow_sdk_lock_conflicts_total" in names
        assert "workflow_sdk_handler_latency_seconds" in names
        assert "workflow_sdk_retries_total" in names
        assert "workflow_sdk_jobs_completed_total" in names

    def test_lock_conflicts_is_counter(self) -> None:
        from prometheus_client import Counter

        reg = CollectorRegistry()
        m = Metrics(registry=reg)
        assert isinstance(m.lock_conflicts, Counter)

    def test_poll_latency_is_histogram(self) -> None:
        from prometheus_client import Histogram

        reg = CollectorRegistry()
        m = Metrics(registry=reg)
        assert isinstance(m.poll_latency, Histogram)

    def test_handler_latency_has_job_type_label(self) -> None:
        reg = CollectorRegistry()
        m = Metrics(registry=reg)
        # labelled histogram — observe with a label
        m.handler_latency.labels(job_type="my-type").observe(0.1)
        collected = list(reg.collect())
        handler_metric = next(
            (c for c in collected if c.name == "workflow_sdk_handler_latency_seconds"), None
        )
        assert handler_metric is not None
        label_names = {s.labels.get("job_type") for s in handler_metric.samples if "job_type" in s.labels}
        assert "my-type" in label_names

    def test_jobs_completed_has_job_type_and_outcome_labels(self) -> None:
        reg = CollectorRegistry()
        m = Metrics(registry=reg)
        m.jobs_completed.labels(job_type="process-order", outcome="success").inc()
        m.jobs_completed.labels(job_type="process-order", outcome="failure").inc()

        collected = list(reg.collect())
        jobs_metric = next(
            (c for c in collected if "jobs_completed" in c.name), None
        )
        assert jobs_metric is not None
        outcomes = {s.labels.get("outcome") for s in jobs_metric.samples}
        assert "success" in outcomes
        assert "failure" in outcomes

    def test_custom_registry_isolates_from_default(self) -> None:
        """Two Metrics instances with different registries must not conflict."""
        reg1 = CollectorRegistry()
        reg2 = CollectorRegistry()
        m1 = Metrics(registry=reg1)
        m2 = Metrics(registry=reg2)  # should not raise DuplicateRegistrationError

        m1.lock_conflicts.inc()
        # m2's counter is independent
        names2 = _collect_names(reg2)
        assert "workflow_sdk_lock_conflicts_total" in names2

    def test_two_independent_instances_do_not_share_state(self) -> None:
        reg1 = CollectorRegistry()
        reg2 = CollectorRegistry()
        m1 = Metrics(registry=reg1)
        m2 = Metrics(registry=reg2)

        m1.lock_conflicts.inc()
        m1.lock_conflicts.inc()

        # m2 should still be at 0
        for family in reg2.collect():
            if family.name == "workflow_sdk_lock_conflicts_total":
                for sample in family.samples:
                    if sample.name.endswith("_total"):
                        assert sample.value == 0
