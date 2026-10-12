"""Worker runner — poll / dispatch / complete loop.

Mirrors the Go SDK's goroutine-based runner using Python's ThreadPoolExecutor
for bounded concurrency.
"""

from __future__ import annotations

import logging
import math
import signal
import threading
import time
from concurrent.futures import Future, ThreadPoolExecutor, wait
from typing import Any

from workflow_sdk.client.interface import EngineClient, LeaseExtender
from workflow_sdk.errors import HeartbeatUnsupportedError, WorkflowSDKError
from workflow_sdk.client.deadline import request_lease, request_timeout
from workflow_sdk.runner.heartbeat import Lease
from workflow_sdk.runner.callback import deliver, execute, lease_deadline
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
    are given a bounded drain period before the method returns. Uncooperative
    handler threads retain their slots and may keep the Python process alive.

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
        *,
        lease_seconds: int = 0,
        heartbeat: bool = False,
        heartbeat_interval: float | None = None,
        shutdown_timeout: float = 5.0,
    ) -> None:
        if not worker_id:
            raise ValueError("worker_id must not be empty")

        if not isinstance(lease_seconds, int) or lease_seconds != 0 and not 5 <= lease_seconds <= 3600:
            raise ValueError("lease_seconds must be 0 or 5–3600")
        interval = (lease_seconds or 30) / 3 if heartbeat_interval is None else heartbeat_interval
        if not 0 < interval <= (lease_seconds or 30) / 3:
            raise ValueError("heartbeat_interval must be positive and at most one third of the lease")
        if not math.isfinite(shutdown_timeout) or shutdown_timeout <= 0:
            raise ValueError("shutdown_timeout must be finite and positive")
        self._lease_seconds, self._heartbeat = lease_seconds, heartbeat
        self._heartbeat_interval, self._shutdown_timeout = interval, shutdown_timeout
        self._state_lock = threading.Lock()
        self._running = False
        self._pending: list[Future[None]] = []
        self._leases: set[Lease] = set()
        self._fatal_error: Exception | None = None
        self._stop_event = threading.Event()
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

        if self._heartbeat and not isinstance(self._client, LeaseExtender):
            raise HeartbeatUnsupportedError("heartbeat requires a client with extend_job_lease")
        with self._state_lock:
            if self._running or any(not future.done() for future in self._pending):
                raise RuntimeError("runner already running or draining handlers")
            self._running = True
            self._fatal_error = None
            self._pending = []
        self._stop_event = stop_event
        self._install_signal_handlers(stop_event)

        job_types = self._registry.job_types()
        logger.info(
            "runner: starting worker_id=%s job_types=%s parallelism=%d",
            self._worker_id,
            job_types,
            self._parallelism,
        )

        in_flight = self._pending
        executor = ThreadPoolExecutor(max_workers=self._parallelism)
        try:
            while not stop_event.is_set():
                self._poll_and_dispatch(executor, in_flight, job_types)
                in_flight[:] = [future for future in in_flight if not future.done()]
                stop_event.wait(timeout=self._poll_interval)
        finally:
            stop_event.set()
            with self._state_lock:
                leases = list(self._leases)
            for lease in leases:
                lease.stop()
            executor.shutdown(wait=False, cancel_futures=True)
            _, pending = wait(in_flight, timeout=self._shutdown_timeout)
            with self._state_lock:
                self._running = False
            if pending and self._fatal_error is None:
                self._fatal_error = RuntimeError("runner shutdown timed out; handlers still running")
        if self._fatal_error is not None:
            raise self._fatal_error
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
        in_flight[:] = [future for future in in_flight if not future.done()]
        available = self._parallelism - len(in_flight)
        if available <= 0 or self._stop_event.is_set():
            return
        start = time.monotonic()
        try:
            with request_timeout(5.0), request_lease(self._lease_seconds):
                jobs = self._client.poll_jobs(self._worker_id, job_types, max_jobs=available)
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
        deadline = lease_deadline(job)
        if self._stop_event.is_set() or deadline is not None and deadline <= time.monotonic():
            return
        lease: Lease | None = None
        try:
            if self._heartbeat:
                if not isinstance(self._client, LeaseExtender):
                    raise HeartbeatUnsupportedError("heartbeat requires extend_job_lease")
                lease = Lease(self._client, job, self._worker_id, self._lease_seconds,
                              self._heartbeat_interval, self._stop_event, self._metrics, self._fatal)
                with self._state_lock:
                    self._leases.add(lease)
                lease.start()
            cancel = lease.cancel_event if lease else self._stop_event
            if cancel.is_set():
                return
            outcome = execute(self._registry, job, self._metrics, cancel)
            if cancel.is_set() or lease is not None and not lease.valid():
                return
            deliver(self._client, self._worker_id, job["id"], outcome,
                    cancel, self._metrics, "polling", deadline,
                    retry=deadline is not None, renewed_deadline=lease.deadline if lease else None)
        except Exception as exc:
            if self._heartbeat and isinstance(exc, (HeartbeatUnsupportedError, ValueError)):
                self._fatal(exc)
            logger.warning("runner: unacknowledged job_id=%s: %s", job["id"], exc)
        finally:
            if lease:
                lease.close()
                with self._state_lock:
                    self._leases.discard(lease)

    def _fatal(self, error: Exception) -> None:
        with self._state_lock:
            if self._fatal_error is None:
                self._fatal_error = error
        self._stop_event.set()

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
