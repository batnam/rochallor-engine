"""Optional lease renewal outside the handler executor. Cancellation is cooperative."""
from __future__ import annotations

import threading
import time
from collections.abc import Callable
from datetime import datetime
from typing import Any

from workflow_sdk.client.deadline import request_timeout
from workflow_sdk.client.interface import LeaseExtender
from workflow_sdk.errors import HeartbeatUnsupportedError, LeaseLostError
from workflow_sdk.metrics.metrics import Metrics
from workflow_sdk.retry.backoff import delay
from workflow_sdk.runner.callback import lease_deadline, permanent


class Lease:
    def __init__(self, client: LeaseExtender, job: dict[str, Any], worker: str, seconds: int,
                 interval: float, parent: threading.Event, metrics: Metrics,
                 fatal: Callable[[Exception], None]) -> None:
        self.cancel_event = threading.Event()
        self._ready = threading.Event()
        self._lock = threading.Lock()
        deadline = lease_deadline(job)
        if deadline is None:
            raise ValueError("heartbeat requires lockExpiresAt lease metadata")
        self._deadline = deadline
        self._expiry = datetime.fromisoformat(job["lockExpiresAt"].replace("Z", "+00:00")).timestamp()
        self._client, self._job, self._worker = client, job, worker
        self._seconds, self._interval, self._parent = seconds, interval, parent
        self._metrics, self._fatal = metrics, fatal
        self._error: Exception | None = None
        self._timer: threading.Timer | None = None
        self._thread = threading.Thread(target=self._run, name="workflow-heartbeat", daemon=True)

    def deadline(self) -> float:
        with self._lock:
            return self._deadline

    def valid(self) -> bool:
        return not self.cancel_event.is_set() and not self._parent.is_set() and time.monotonic() < self.deadline()

    def start(self) -> None:
        self._arm_expiry()
        self._thread.start()
        while not self._ready.wait(.02):
            if self._parent.is_set():
                self.stop()
                break
        if not self.valid():
            raise self._error or LeaseLostError("job lease lost before first heartbeat")

    def stop(self) -> None:
        self.cancel_event.set()
        self._ready.set()
        with self._lock:
            if self._timer:
                self._timer.cancel()

    def close(self) -> None:
        self.stop()
        if self._thread.ident is not None:
            self._thread.join(.1)

    def _arm_expiry(self) -> None:
        with self._lock:
            if self.cancel_event.is_set():
                return
            if self._timer:
                self._timer.cancel()
            self._timer = threading.Timer(max(0, self._deadline - time.monotonic()), self._expire)
            self._timer.daemon = True
            self._timer.start()

    def _expire(self) -> None:
        with self._lock:
            # A cancelled timer may already have started before renewal.
            if time.monotonic() < self._deadline or self.cancel_event.is_set():
                return
            self._error = LeaseLostError("job lease expired")
            self.cancel_event.set()
            self._ready.set()
        self._metrics.heartbeats.labels(result="lease_lost").inc()

    def _run(self) -> None:
        attempt = 0
        try:
            while self.valid():
                try:
                    with request_timeout(min(5.0, max(.001, self.deadline() - time.monotonic()))):
                        value = self._client.extend_job_lease(self._job["id"], self._worker, self._seconds)
                    if not self.valid():
                        break
                    expiry = datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()
                    deadline = lease_deadline({"lockExpiresAt": value})
                    if deadline is None or deadline <= time.monotonic() or expiry < self._expiry:
                        raise ValueError("invalid heartbeat expiry")
                    with self._lock:
                        if self.cancel_event.is_set():
                            break
                        self._deadline, self._expiry = deadline, expiry
                    self._arm_expiry()
                    self._metrics.heartbeats.labels(result="accepted").inc()
                    self._ready.set()
                    attempt = 0
                    self.cancel_event.wait(max(.001, min(self._interval, (deadline - time.monotonic()) / 3)))
                except Exception as error:
                    if not self.valid():
                        break
                    if isinstance(error, LeaseLostError):
                        self._metrics.heartbeats.labels(result="lease_lost").inc()
                        raise
                    if isinstance(error, (HeartbeatUnsupportedError, ValueError, TypeError)) or permanent(error):
                        self._metrics.heartbeats.labels(result="rejected").inc()
                        self._fatal(error)
                        raise
                    attempt += 1
                    self._metrics.heartbeats.labels(result="retry").inc()
                    self.cancel_event.wait(max(0, min(delay(attempt), self.deadline() - time.monotonic())))
        except Exception as error:
            self._error = error
        finally:
            self.stop()
