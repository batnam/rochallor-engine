"""Per-call timeout scope used by runners without changing custom client methods."""
from contextlib import contextmanager
from contextvars import ContextVar
from collections.abc import Iterator

_timeout: ContextVar[float | None] = ContextVar("workflow_sdk_timeout", default=None)
_lease: ContextVar[int] = ContextVar("workflow_sdk_lease_seconds", default=0)


def current_lease_seconds() -> int:
    return _lease.get()


@contextmanager
def request_lease(seconds: int) -> Iterator[None]:
    """Optional poll lease scope; leaves legacy custom client signatures unchanged."""
    token = _lease.set(seconds)
    try:
        yield
    finally:
        _lease.reset(token)


def current_timeout() -> float | None:
    return _timeout.get()


@contextmanager
def request_timeout(seconds: float) -> Iterator[None]:
    token = _timeout.set(seconds)
    try:
        yield
    finally:
        _timeout.reset(token)
