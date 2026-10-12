"""Deliver an immutable handler outcome while the worker still owns the job."""
from __future__ import annotations

import json
import logging
import time
from dataclasses import dataclass
from collections.abc import Callable
from datetime import datetime, timezone
from threading import Event
from typing import Any

import grpc

from workflow_sdk.client.deadline import request_timeout
from workflow_sdk.client.interface import EngineClient
from workflow_sdk.errors import EngineClientError, NonRetryableError
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.metrics.metrics import Metrics
from workflow_sdk.retry.backoff import delay

log = logging.getLogger(__name__)


@dataclass(frozen=True)
class Outcome:
    kind: str
    payload: str
    retryable: bool = False


def lease_deadline(job: dict[str, Any]) -> float | None:
    value = job.get("lockExpiresAt")
    if value is None:
        return None
    try:
        expiry = datetime.fromisoformat(value.replace("Z", "+00:00"))
        remaining = (expiry - datetime.now(timezone.utc)).total_seconds()
        return time.monotonic() + remaining
    except (TypeError, ValueError, AttributeError):
        return 0.0


def execute(registry: HandlerRegistry, job: dict[str, Any], metrics: Metrics, cancel: Event | None = None) -> Outcome:
    job_type = job["jobType"]
    handler = registry.get(job_type)
    if handler is None:
        return Outcome("fail", f"no handler registered for {job_type!r}")
    ctx = {key: job.get(key, "") for key in ("id", "jobType", "instanceId", "stepId", "stepExecutionId")}
    ctx.update(retriesRemaining=job.get("retriesRemaining", 0), variables=job.get("variables") or {})
    if cancel is not None:
        ctx["cancel_event"] = cancel
    started = time.monotonic()
    try:
        result = handler(ctx)
        payload = json.dumps(result if isinstance(result, dict) else {}, allow_nan=False)
    except Exception as exc:
        if cancel is not None and cancel.is_set():
            raise RuntimeError("handler ownership ended") from exc
        retryable = not isinstance(exc, NonRetryableError)
        metrics.jobs_completed.labels(job_type=job_type, outcome="failure").inc()
        if retryable:
            metrics.retries_total.labels(job_type=job_type).inc()
        return Outcome("fail", str(exc), retryable)
    finally:
        metrics.handler_latency.labels(job_type=job_type).observe(time.monotonic() - started)
    if cancel is not None and cancel.is_set():
        raise RuntimeError("handler ownership ended")
    metrics.jobs_completed.labels(job_type=job_type, outcome="success").inc()
    return Outcome("complete", payload)


def permanent(exc: Exception) -> bool:
    if isinstance(exc, EngineClientError):
        return 400 <= exc.status_code < 500 and exc.status_code not in (408, 429)
    return isinstance(exc, grpc.RpcError) and exc.code() in (
        grpc.StatusCode.INVALID_ARGUMENT, grpc.StatusCode.NOT_FOUND,
        grpc.StatusCode.ALREADY_EXISTS, grpc.StatusCode.PERMISSION_DENIED,
        grpc.StatusCode.FAILED_PRECONDITION, grpc.StatusCode.OUT_OF_RANGE,
        grpc.StatusCode.UNIMPLEMENTED, grpc.StatusCode.UNAUTHENTICATED,
        grpc.StatusCode.DATA_LOSS,
    )


def deliver(client: EngineClient, worker: str, job_id: str, outcome: Outcome,
            stop: Event, metrics: Metrics, mode: str, deadline: float | None = None,
            retry: bool = True, renewed_deadline: Callable[[], float] | None = None) -> None:
    finish = metrics.track_callback(mode, outcome.kind)
    started = time.monotonic()
    attempt = 0
    try:
        while True:
            deadline = renewed_deadline() if renewed_deadline else deadline
            remaining = float("inf") if deadline is None else deadline - time.monotonic()
            if stop.is_set() or remaining <= 0:
                raise RuntimeError("callback ownership ended")
            attempt += 1
            try:
                with request_timeout(min(5.0, remaining)):
                    if outcome.kind == "complete":
                        client.complete_job(job_id, worker, json.loads(outcome.payload))
                    else:
                        client.fail_job(job_id, worker, outcome.payload, outcome.retryable)
                deadline = renewed_deadline() if renewed_deadline else deadline
                if stop.is_set() or deadline is not None and time.monotonic() >= deadline:
                    raise RuntimeError("callback ownership ended")
                return
            except Exception as exc:
                deadline = renewed_deadline() if renewed_deadline else deadline
                if stop.is_set() or deadline is not None and time.monotonic() >= deadline:
                    raise
                if permanent(exc):
                    metrics.callback_rejected.labels(dispatch=mode, callback=outcome.kind).inc()
                    raise
                if not retry:
                    raise
                metrics.callback_retries.labels(dispatch=mode, callback=outcome.kind).inc()
                log.warning("callback pending job_id=%s callback=%s attempt=%d age_seconds=%.3f: %s",
                            job_id, outcome.kind, attempt, time.monotonic() - started, exc)
                remaining = float("inf") if deadline is None else max(0, deadline - time.monotonic())
                stop.wait(min(delay(attempt), remaining))
    finally:
        finish()
