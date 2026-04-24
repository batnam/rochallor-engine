"""Exception hierarchy for the workflow SDK."""

from __future__ import annotations


class WorkflowSDKError(Exception):
    """Base exception for all workflow SDK errors."""


class NonRetryableError(WorkflowSDKError):
    """Raised by a handler to signal the job must not be retried.

    The runner will call FailJob with ``retryable=False``, bypassing the
    engine's retry budget entirely.

    Example::

        from workflow_sdk.errors import NonRetryableError

        def my_handler(ctx):
            if ctx["variables"].get("invalid"):
                raise NonRetryableError("input data is invalid — will never succeed")
            return {"ok": True}
    """


class EngineClientError(WorkflowSDKError):
    """Raised when the workflow engine returns a non-2xx HTTP response.

    Attributes:
        status_code: The HTTP status code returned by the engine.
        message: The error description.
    """

    def __init__(self, status_code: int, message: str) -> None:
        super().__init__(f"engine error {status_code}: {message}")
        self.status_code = status_code
        self.message = message
