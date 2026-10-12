"""REST transport implementation of EngineClient.

All 11 operations are implemented against the Rochallor engine's JSON REST API.
"""

from __future__ import annotations

from typing import Any
from datetime import datetime

import httpx

from workflow_sdk.client.deadline import current_lease_seconds, current_timeout
from workflow_sdk.errors import EngineClientError, HeartbeatUnsupportedError, LeaseLostError, WorkflowSDKError


class RestEngineClient:
    """Communicates with the workflow engine over HTTP/JSON (REST).

    Example::

        client = RestEngineClient("http://localhost:8080")
        jobs = client.poll_jobs("worker-1", ["process-order"])

    Args:
        base_url: Base URL of the workflow engine (no trailing slash).
        timeout: HTTP request timeout in seconds (default 30).
    """

    def __init__(self, base_url: str, timeout: float = 30.0) -> None:
        self._base = base_url.rstrip("/")
        self._client = httpx.Client(
            timeout=timeout,
            headers={"Content-Type": "application/json", "Accept": "application/json"},
        )

    # ------------------------------------------------------------------ #
    # Internal helpers                                                     #
    # ------------------------------------------------------------------ #

    def _get(self, path: str, params: dict[str, Any] | None = None) -> Any:
        try:
            resp = self._client.get(self._base + path, params=params)
        except (httpx.ConnectError, httpx.TimeoutException) as exc:
            raise WorkflowSDKError(f"connection error: {exc}") from exc
        self._raise_for_status(resp)
        return resp.json()

    def _post(self, path: str, body: dict[str, Any]) -> httpx.Response:
        try:
            timeout = current_timeout()
            if timeout is None:
                resp = self._client.post(self._base + path, json=body)
            else:
                resp = self._client.post(self._base + path, json=body, timeout=timeout)
        except (httpx.ConnectError, httpx.TimeoutException) as exc:
            raise WorkflowSDKError(f"connection error: {exc}") from exc
        return resp

    def _post_json(self, path: str, body: dict[str, Any]) -> Any:
        resp = self._post(path, body)
        self._raise_for_status(resp)
        if resp.status_code == 204 or not resp.content:
            return None
        return resp.json()

    def _post_no_content(self, path: str, body: dict[str, Any]) -> None:
        """POST and accept 200 or 204 as success."""
        resp = self._post(path, body)
        if resp.status_code not in (200, 204):
            self._raise_for_status(resp)

    @staticmethod
    def _raise_for_status(resp: httpx.Response) -> None:
        if resp.status_code >= 400:
            try:
                detail = resp.json().get("message", resp.text)
            except Exception:
                detail = resp.text
            raise EngineClientError(resp.status_code, detail)

    # ------------------------------------------------------------------ #
    # Definition operations                                                #
    # ------------------------------------------------------------------ #

    def upload_definition(self, definition: dict[str, Any]) -> dict[str, Any]:
        """POST /v1/definitions — returns 201 with the definition summary."""
        resp = self._post("/v1/definitions", definition)
        if resp.status_code not in (200, 201):
            self._raise_for_status(resp)
        return resp.json()  # type: ignore[no-any-return]

    def get_definition(self, definition_id: str) -> dict[str, Any]:
        """GET /v1/definitions/{id}"""
        return self._get(f"/v1/definitions/{definition_id}")  # type: ignore[return-value]

    def list_definitions(
        self,
        keyword: str = "",
        page: int = 0,
        page_size: int = 20,
    ) -> dict[str, Any]:
        """GET /v1/definitions — returns page dict with items/page/pageSize/total."""
        params: dict[str, Any] = {"page": page, "pageSize": page_size}
        if keyword:
            params["keyword"] = keyword
        return self._get("/v1/definitions", params=params)  # type: ignore[return-value]

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
        """POST /v1/instances — returns 201 with the instance summary."""
        body: dict[str, Any] = {"definitionId": definition_id}
        if variables is not None:
            body["variables"] = variables
        if definition_version is not None:
            body["definitionVersion"] = definition_version
        if business_key is not None:
            body["businessKey"] = business_key

        resp = self._post("/v1/instances", body)
        if resp.status_code not in (200, 201):
            self._raise_for_status(resp)
        return resp.json()  # type: ignore[no-any-return]

    def get_instance(self, instance_id: str) -> dict[str, Any]:
        """GET /v1/instances/{id}"""
        return self._get(f"/v1/instances/{instance_id}")  # type: ignore[return-value]

    def get_instance_history(self, instance_id: str) -> list[dict[str, Any]]:
        """GET /v1/instances/{id}/history — returns list of step execution summaries."""
        result = self._get(f"/v1/instances/{instance_id}/history")
        # Engine wraps in {"items": [...]}
        if isinstance(result, dict) and "items" in result:
            return result["items"]  # type: ignore[no-any-return]
        return result  # type: ignore[return-value]  # fallback for direct array

    def cancel_instance(self, instance_id: str, reason: str = "") -> dict[str, Any]:
        """POST /v1/instances/{id}/cancel"""
        body: dict[str, Any] = {}
        if reason:
            body["reason"] = reason
        resp = self._post(f"/v1/instances/{instance_id}/cancel", body)
        self._raise_for_status(resp)
        return resp.json()  # type: ignore[no-any-return]

    # ------------------------------------------------------------------ #
    # Job operations                                                       #
    # ------------------------------------------------------------------ #

    def poll_jobs(
        self,
        worker_id: str,
        job_types: list[str],
        max_jobs: int = 1,
        lease_seconds: int | None = None,
    ) -> list[dict[str, Any]]:
        """POST /v1/jobs/poll — returns list of locked jobs (may be empty)."""
        body = {
            "workerId": worker_id,
            "jobTypes": job_types,
            "maxJobs": max_jobs,
            "leaseSeconds": current_lease_seconds() if lease_seconds is None else lease_seconds,
        }
        resp = self._post("/v1/jobs/poll", body)
        self._raise_for_status(resp)
        data = resp.json()
        # Engine wraps response in {"jobs": [...]}
        if isinstance(data, dict) and "jobs" in data:
            return data["jobs"]  # type: ignore[no-any-return]
        return data  # type: ignore[return-value]  # fallback for direct array

    def extend_job_lease(self, job_id: str, worker_id: str, lease_seconds: int = 0) -> str:
        response = self._post(f"/v1/jobs/{job_id}/heartbeat", {"workerId": worker_id, "leaseSeconds": lease_seconds})
        if response.status_code in (404, 405, 501):
            raise HeartbeatUnsupportedError("engine does not support heartbeat (extend_job_lease)")
        if response.status_code == 409:
            raise LeaseLostError("job lease lost")
        self._raise_for_status(response)
        body = response.json()
        value = body.get("lockExpiresAt")
        if body.get("accepted") is not True or not isinstance(value, str):
            raise WorkflowSDKError("invalid heartbeat response")
        try:
            if datetime.fromisoformat(value.replace("Z", "+00:00")).tzinfo is None:
                raise ValueError("timezone missing")
        except ValueError as error:
            raise WorkflowSDKError("invalid heartbeat deadline") from error
        return value

    def complete_job(
        self,
        job_id: str,
        worker_id: str,
        variables: dict[str, Any] | None = None,
    ) -> None:
        """POST /v1/jobs/{id}/complete — accepts 200 or 204."""
        body = {
            "workerId": worker_id,
            "variables": variables or {},
        }
        self._post_no_content(f"/v1/jobs/{job_id}/complete", body)

    def fail_job(
        self,
        job_id: str,
        worker_id: str,
        error_message: str,
        retryable: bool = True,
    ) -> None:
        """POST /v1/jobs/{id}/fail — accepts 200 or 204."""
        body = {
            "workerId": worker_id,
            "errorMessage": error_message,
            "retryable": retryable,
        }
        self._post_no_content(f"/v1/jobs/{job_id}/fail", body)

    # ------------------------------------------------------------------ #
    # User task operations                                                 #
    # ------------------------------------------------------------------ #

    def complete_user_task(
        self,
        task_id: str,
        completed_by: str = "",
        result: dict[str, Any] | None = None,
    ) -> None:
        """POST /v1/user-tasks/{id}/complete"""
        body: dict[str, Any] = {}
        if completed_by:
            body["completedBy"] = completed_by
        if result is not None:
            body["result"] = result
        resp = self._post(f"/v1/user-tasks/{task_id}/complete", body)
        self._raise_for_status(resp)

    def close(self) -> None:
        """Close the underlying HTTP client and release connections."""
        self._client.close()

    def __enter__(self) -> RestEngineClient:
        return self

    def __exit__(self, *args: object) -> None:
        self.close()
