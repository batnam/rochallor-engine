"""EngineClient protocol — transport-agnostic interface for the workflow engine."""

from __future__ import annotations

from typing import Any, Protocol, runtime_checkable


@runtime_checkable
class EngineClient(Protocol):
    """Transport-agnostic interface for communicating with the workflow engine.

    Both :class:`~workflow_sdk.client.rest.RestEngineClient` and any future
    gRPC client implement this protocol.
    """

    # ------------------------------------------------------------------ #
    # Definition operations                                                #
    # ------------------------------------------------------------------ #

    def upload_definition(self, definition: dict[str, Any]) -> dict[str, Any]:
        """Upload a workflow definition.

        Args:
            definition: The workflow definition as a JSON-serialisable dict.

        Returns:
            The created definition summary returned by the engine.

        Raises:
            EngineClientError: On non-2xx response.
            WorkflowSDKError: On connection / timeout errors.
        """
        ...

    def get_definition(self, definition_id: str) -> dict[str, Any]:
        """Retrieve the latest version of a definition by its ID."""
        ...

    def list_definitions(
        self,
        keyword: str = "",
        page: int = 0,
        page_size: int = 20,
    ) -> dict[str, Any]:
        """List workflow definitions with optional keyword search.

        Returns:
            A page dict containing ``items``, ``page``, ``pageSize``, ``total``.
        """
        ...

    # ------------------------------------------------------------------ #
    # Instance operations                                                  #
    # ------------------------------------------------------------------ #

    def start_instance(
        self,
        definition_id: str,
        variables: dict[str, Any] | None = None,
        definition_version: int | None = None,
        business_key: str | None = None,
    ) -> dict[str, Any]:
        """Start a new workflow instance.

        Args:
            definition_id: ID of the workflow definition to run.
            variables: Optional input variables.
            definition_version: Pin to a specific version (latest if omitted).
            business_key: Optional business correlation key.

        Returns:
            The created workflow instance summary.
        """
        ...

    def get_instance(self, instance_id: str) -> dict[str, Any]:
        """Get the current state of a workflow instance."""
        ...

    def get_instance_history(self, instance_id: str) -> list[dict[str, Any]]:
        """Get the step execution history for a workflow instance."""
        ...

    def cancel_instance(self, instance_id: str, reason: str = "") -> dict[str, Any]:
        """Cancel an in-flight workflow instance."""
        ...

    # ------------------------------------------------------------------ #
    # Job operations (used by the Runner / worker processes)              #
    # ------------------------------------------------------------------ #

    def poll_jobs(
        self,
        worker_id: str,
        job_types: list[str],
        max_jobs: int = 1,
    ) -> list[dict[str, Any]]:
        """Poll for available jobs matching the given job types.

        Uses ``SELECT ... FOR UPDATE SKIP LOCKED`` on the engine side.

        Args:
            worker_id: Unique identifier for this worker process.
            job_types: List of job type strings this worker handles.
            max_jobs: Maximum jobs to return in one call (1–100).

        Returns:
            A list of locked job dicts (may be empty if nothing available).
        """
        ...

    def complete_job(
        self,
        job_id: str,
        worker_id: str,
        variables: dict[str, Any] | None = None,
    ) -> None:
        """Mark a job as successfully completed.

        Args:
            job_id: ID of the job to complete.
            worker_id: ID of the worker that processed the job.
            variables: Output variables to merge into the workflow instance.
        """
        ...

    def fail_job(
        self,
        job_id: str,
        worker_id: str,
        error_message: str,
        retryable: bool = True,
    ) -> None:
        """Mark a job as failed.

        Args:
            job_id: ID of the job to fail.
            worker_id: ID of the worker that processed the job.
            error_message: Human-readable error description.
            retryable: If ``False``, the engine will not retry regardless of
                retries_remaining. Use for permanent failures.
        """
        ...

    # ------------------------------------------------------------------ #
    # User task operations                                                 #
    # ------------------------------------------------------------------ #

    def complete_user_task(
        self,
        task_id: str,
        completed_by: str = "",
        result: dict[str, Any] | None = None,
    ) -> None:
        """Complete a pending USER_TASK step.

        Args:
            task_id: ID of the user task to complete.
            completed_by: Optional identifier of the person completing the task.
            result: Optional output variables from the human task.
        """
        ...
