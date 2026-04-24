"""Worker runner — poll / dispatch / complete loop.

Mirrors the Go SDK's goroutine-based runner using Python's ThreadPoolExecutor
for bounded concurrency.
"""

from __future__ import annotations

import logging
import signal
import threading
import time
from concurrent.futures import Future, ThreadPoolExecutor
from typing import Any

from workflow_sdk.client.interface import EngineClient
from workflow_sdk.errors import NonRetryableError, WorkflowSDKError
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.metrics.metrics import Metrics
from prometheus_client import CollectorRegistry

logger = logging.getLogger(__name__)

_DEFAULT_PARALLELISM = 64
_DEFAULT_POLL_INTERVAL = 0.5  # seconds


class Runner:
    """Polls the workflow engine and dispatches jobs to registered handlers.

    The runner blocks inside :meth:`run` until ``stop_event`` is set or a
    ``SIGTERM`` / ``SIGINT`` signal is received.  All in-flight job executions
    are drained before the method returns.

    Example::

        from workflow_sdk.client import RestEngineClient
        from workflow_sdk.handler import HandlerRegistry
        from workflow_sdk.runner import Runner

        client = RestEngineClient("http://localhost:8080")
        registry = HandlerRegistry()
        registry.register("my-job", lambda ctx: {"done": True})

        runner = Runner(client, registry, worker_id="worker-1")
        runner.run()  # blocks until SIGTERM/SIGINT

    Args:
        client: Engine client (REST or future gRPC).
        registry: Handler registry mapping job types to callables.
        worker_id: Unique identifier for this worker process.
        parallelism: Maximum concurrent job executions (default 64).
        poll_interval: Seconds between poll rounds (default 0.5).
        metrics: Prometheus metrics instance. A fresh isolated registry is
                 created automatically if not provided.
    """

    def __init__(
        self,
        client: EngineClient,
        registry: HandlerRegistry,
        worker_id: str,
        parallelism: int = _DEFAULT_PARALLELISM,
        poll_interval: float = _DEFAULT_POLL_INTERVAL,
        metrics: Metrics | None = None,
    ) -> None:
        if not worker_id:
            raise ValueError("worker_id must not be empty")

        self._client = client
        self._registry = registry
        self._worker_id = worker_id
        self._parallelism = parallelism if parallelism > 0 else _DEFAULT_PARALLELISM
        self._poll_interval = poll_interval if poll_interval > 0 else _DEFAULT_POLL_INTERVAL
        self._metrics = metrics or Metrics(registry=CollectorRegistry())

    # ------------------------------------------------------------------ #
    # Public API                                                           #
    # ------------------------------------------------------------------ #

    def run(self, stop_event: threading.Event | None = None) -> None:
        """Start the polling loop and block until signalled to stop.

        Registers ``SIGTERM`` and ``SIGINT`` handlers that set the stop event.
        If ``stop_event`` is ``None``, an internal one is created.

        Args:
            stop_event: External event to signal shutdown.  Set it from
                another thread to trigger graceful shutdown.
        """
        if stop_event is None:
            stop_event = threading.Event()

        self._install_signal_handlers(stop_event)

        job_types = self._registry.job_types()
        logger.info(
            "runner: starting worker_id=%s job_types=%s parallelism=%d",
            self._worker_id,
            job_types,
            self._parallelism,
        )

        in_flight: list[Future[None]] = []

        with ThreadPoolExecutor(max_workers=self._parallelism) as executor:
            while not stop_event.is_set():
                self._poll_and_dispatch(executor, in_flight, job_types)
                # Prune completed futures to avoid memory growth
                in_flight[:] = [f for f in in_flight if not f.done()]
                stop_event.wait(timeout=self._poll_interval)

            # Drain remaining in-flight jobs
            logger.info("runner: draining %d in-flight jobs", len(in_flight))
            for future in in_flight:
                try:
                    future.result(timeout=300)
                except Exception:
                    pass  # errors were already handled inside _dispatch

        logger.info("runner: stopped")

    # ------------------------------------------------------------------ #
    # Internal methods                                                     #
    # ------------------------------------------------------------------ #

    def _poll_and_dispatch(
        self,
        executor: ThreadPoolExecutor,
        in_flight: list[Future[None]],
        job_types: list[str],
    ) -> None:
        start = time.monotonic()
        try:
            jobs = self._client.poll_jobs(
                self._worker_id, job_types, max_jobs=self._parallelism
            )
        except WorkflowSDKError as exc:
            logger.warning("runner: poll error — %s", exc)
            return
        except Exception as exc:
            logger.warning("runner: unexpected poll error — %s", exc)
            return
        finally:
            self._metrics.poll_latency.observe(time.monotonic() - start)

        if not jobs:
            self._metrics.lock_conflicts.inc()
            return

        for job in jobs:
            logger.info("runner: dispatching job_id=%s job_type=%s", job["id"], job["jobType"])
            future = executor.submit(self._dispatch, job)
            in_flight.append(future)

    def _dispatch(self, job: dict[str, Any]) -> None:
        job_id: str = job["id"]
        job_type: str = job["jobType"]

        handler = self._registry.get(job_type)
        if handler is None:
            logger.error("runner: no handler for job_type=%s job_id=%s", job_type, job_id)
            self._safe_fail(job_id, f"no handler registered for {job_type!r}", retryable=False)
            return

        ctx: dict[str, Any] = {
            "id": job_id,
            "jobType": job_type,
            "instanceId": job.get("instanceId", ""),
            "stepId": job.get("stepId", ""),
            "stepExecutionId": job.get("stepExecutionId", ""),
            "retriesRemaining": job.get("retriesRemaining", 0),
            "variables": job.get("variables") or {},
        }

        start = time.monotonic()
        try:
            result = self._safe_call(handler, ctx)
            logger.info("runner: job completed job_id=%s", job_id)
        except NonRetryableError as exc:
            self._metrics.handler_latency.labels(job_type=job_type).observe(
                time.monotonic() - start
            )
            self._metrics.jobs_completed.labels(job_type=job_type, outcome="failure").inc()
            logger.warning("runner: job failed (non-retryable) job_id=%s: %s", job_id, exc)
            self._safe_fail(job_id, str(exc), retryable=False)
            return
        except Exception as exc:
            self._metrics.handler_latency.labels(job_type=job_type).observe(
                time.monotonic() - start
            )
            self._metrics.jobs_completed.labels(job_type=job_type, outcome="failure").inc()
            self._metrics.retries_total.labels(job_type=job_type).inc()
            logger.exception("runner: job failed (retryable) job_id=%s: %s", job_id, exc)
            self._safe_fail(job_id, str(exc), retryable=True)
            return

        self._metrics.handler_latency.labels(job_type=job_type).observe(
            time.monotonic() - start
        )
        self._metrics.jobs_completed.labels(job_type=job_type, outcome="success").inc()

        variables = result if isinstance(result, dict) else {}
        try:
            self._client.complete_job(job_id, self._worker_id, variables)
        except Exception as exc:
            logger.error("runner: complete_job failed job_id=%s — %s", job_id, exc)

    @staticmethod
    def _safe_call(
        handler: Any,
        ctx: dict[str, Any],
    ) -> dict[str, Any] | None:
        """Execute handler, converting panics (unhandled exceptions) to NonRetryableError."""
        try:
            return handler(ctx)
        except NonRetryableError:
            raise
        except Exception:
            raise

    def _safe_fail(self, job_id: str, error_message: str, retryable: bool) -> None:
        try:
            self._client.fail_job(job_id, self._worker_id, error_message, retryable)
        except Exception as exc:
            logger.error("runner: fail_job error job_id=%s — %s", job_id, exc)

    @staticmethod
    def _install_signal_handlers(stop_event: threading.Event) -> None:
        def _handle(signum: int, frame: object) -> None:
            logger.info("runner: received signal %d, shutting down", signum)
            stop_event.set()

        try:
            signal.signal(signal.SIGTERM, _handle)
            signal.signal(signal.SIGINT, _handle)
        except (OSError, ValueError):
            # Can't install signal handlers in non-main threads — ignore
            pass
