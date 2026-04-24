"""Job-type handler registry.

Handlers are keyed by jobType string — never by class path (R-010).
"""

from __future__ import annotations

import threading
from typing import Any, Callable


# Type alias for a handler function
JobHandler = Callable[[dict[str, Any]], dict[str, Any] | None]


class HandlerRegistry:
    """Thread-safe registry mapping job type strings to handler callables.

    Example::

        registry = HandlerRegistry()

        def process_order(ctx):
            order_id = ctx["variables"]["orderId"]
            # ... process order ...
            return {"processed": True, "orderId": order_id}

        registry.register("process-order", process_order)
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._handlers: dict[str, JobHandler] = {}

    def register(self, job_type: str, handler: JobHandler) -> None:
        """Register a handler for the given job type.

        Args:
            job_type: Non-empty string identifying the job type.
            handler: Callable that accepts a job context dict and returns
                     a dict of variables to set (or None).

        Raises:
            ValueError: If ``job_type`` is empty.
        """
        if not job_type:
            raise ValueError("job_type must not be empty")
        with self._lock:
            self._handlers[job_type] = handler

    def get(self, job_type: str) -> JobHandler | None:
        """Return the handler for ``job_type``, or ``None`` if not registered.

        Args:
            job_type: The job type to look up.

        Returns:
            The registered handler, or ``None``.
        """
        with self._lock:
            return self._handlers.get(job_type)

    def job_types(self) -> list[str]:
        """Return all registered job type strings.

        Returns:
            A snapshot list of registered job type strings.
        """
        with self._lock:
            return list(self._handlers.keys())
