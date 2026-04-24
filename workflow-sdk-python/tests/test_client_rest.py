"""Tests for RestEngineClient — all HTTP interactions mocked via pytest-httpx."""

from __future__ import annotations

import json
from typing import Any

import pytest
from pytest_httpx import HTTPXMock

from workflow_sdk.client.rest import RestEngineClient
from workflow_sdk.errors import EngineClientError


BASE_URL = "http://engine.test"


@pytest.fixture
def client() -> RestEngineClient:
    return RestEngineClient(BASE_URL)


# ------------------------------------------------------------------ #
# poll_jobs                                                            #
# ------------------------------------------------------------------ #


class TestPollJobs:
    def test_sends_correct_request(
        self, client: RestEngineClient, httpx_mock: HTTPXMock, sample_job: dict
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/poll",
            json={"jobs": [sample_job]},
        )
        jobs = client.poll_jobs("worker-1", ["process-order"], max_jobs=5)
        assert len(jobs) == 1
        assert jobs[0]["id"] == "job-001"

        request = httpx_mock.get_request()
        assert request is not None
        body = json.loads(request.content)
        assert body["workerId"] == "worker-1"
        assert body["jobTypes"] == ["process-order"]
        assert body["maxJobs"] == 5

    def test_returns_empty_list_when_no_jobs(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/poll",
            json={"jobs": []},
        )
        jobs = client.poll_jobs("worker-1", ["type-a"])
        assert jobs == []

    def test_raises_engine_client_error_on_4xx(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/poll",
            status_code=400,
            json={"error": "bad_request", "message": "workerId is required"},
        )
        with pytest.raises(EngineClientError) as exc_info:
            client.poll_jobs("", ["type-a"])
        assert exc_info.value.status_code == 400

    def test_raises_engine_client_error_on_5xx(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/poll",
            status_code=500,
        )
        with pytest.raises(EngineClientError) as exc_info:
            client.poll_jobs("worker-1", ["type-a"])
        assert exc_info.value.status_code == 500


# ------------------------------------------------------------------ #
# complete_job                                                          #
# ------------------------------------------------------------------ #


class TestCompleteJob:
    def test_sends_correct_request_with_200(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/job-001/complete",
            status_code=200,
        )
        client.complete_job("job-001", "worker-1", {"result": "done"})

        request = httpx_mock.get_request()
        assert request is not None
        body = json.loads(request.content)
        assert body["workerId"] == "worker-1"
        assert body["variables"] == {"result": "done"}

    def test_accepts_204_no_content(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/job-001/complete",
            status_code=204,
        )
        # Should not raise
        client.complete_job("job-001", "worker-1")

    def test_sends_empty_variables_when_none(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/job-001/complete",
            status_code=204,
        )
        client.complete_job("job-001", "worker-1", None)

        request = httpx_mock.get_request()
        assert request is not None
        body = json.loads(request.content)
        assert body["variables"] == {}

    def test_raises_on_error_status(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/job-001/complete",
            status_code=404,
        )
        with pytest.raises(EngineClientError) as exc_info:
            client.complete_job("job-001", "worker-1")
        assert exc_info.value.status_code == 404


# ------------------------------------------------------------------ #
# fail_job                                                             #
# ------------------------------------------------------------------ #


class TestFailJob:
    def test_sends_retryable_true(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/job-001/fail",
            status_code=204,
        )
        client.fail_job("job-001", "worker-1", "something went wrong", retryable=True)

        request = httpx_mock.get_request()
        assert request is not None
        body = json.loads(request.content)
        assert body["retryable"] is True
        assert body["errorMessage"] == "something went wrong"
        assert body["workerId"] == "worker-1"

    def test_sends_retryable_false(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/job-001/fail",
            status_code=204,
        )
        client.fail_job("job-001", "worker-1", "fatal error", retryable=False)

        request = httpx_mock.get_request()
        assert request is not None
        body = json.loads(request.content)
        assert body["retryable"] is False

    def test_accepts_200(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/jobs/job-001/fail",
            status_code=200,
        )
        client.fail_job("job-001", "worker-1", "error", retryable=True)


# ------------------------------------------------------------------ #
# upload_definition                                                    #
# ------------------------------------------------------------------ #


class TestUploadDefinition:
    def test_sends_post_and_parses_response(
        self,
        client: RestEngineClient,
        httpx_mock: HTTPXMock,
        sample_definition: dict,
    ) -> None:
        response_body = {"id": "order-workflow", "version": 1, "name": "Order Workflow"}
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/definitions",
            status_code=201,
            json=response_body,
        )
        result = client.upload_definition(sample_definition)
        assert result["id"] == "order-workflow"
        assert result["version"] == 1

        request = httpx_mock.get_request()
        assert request is not None
        assert request.headers["content-type"] == "application/json"


# ------------------------------------------------------------------ #
# get_definition                                                        #
# ------------------------------------------------------------------ #


class TestGetDefinition:
    def test_sends_get_to_correct_url(
        self, client: RestEngineClient, httpx_mock: HTTPXMock, sample_definition: dict
    ) -> None:
        httpx_mock.add_response(
            method="GET",
            url=f"{BASE_URL}/v1/definitions/order-workflow",
            json=sample_definition,
        )
        result = client.get_definition("order-workflow")
        assert result["id"] == "order-workflow"

    def test_raises_on_404(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="GET",
            url=f"{BASE_URL}/v1/definitions/missing",
            status_code=404,
        )
        with pytest.raises(EngineClientError) as exc_info:
            client.get_definition("missing")
        assert exc_info.value.status_code == 404


# ------------------------------------------------------------------ #
# list_definitions                                                      #
# ------------------------------------------------------------------ #


class TestListDefinitions:
    def test_returns_page_dict(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        page = {"items": [], "page": 0, "pageSize": 20, "total": 0}
        # Match URL with default query params page=0&pageSize=20
        httpx_mock.add_response(
            method="GET",
            url=f"{BASE_URL}/v1/definitions?page=0&pageSize=20",
            json=page,
        )
        result = client.list_definitions()
        assert result["total"] == 0


# ------------------------------------------------------------------ #
# start_instance                                                        #
# ------------------------------------------------------------------ #


class TestStartInstance:
    def test_sends_definition_id_and_variables(
        self, client: RestEngineClient, httpx_mock: HTTPXMock, sample_instance: dict
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/instances",
            status_code=201,
            json=sample_instance,
        )
        result = client.start_instance("order-workflow", variables={"orderId": "ORD-1"})
        assert result["id"] == "inst-001"

        request = httpx_mock.get_request()
        assert request is not None
        body = json.loads(request.content)
        assert body["definitionId"] == "order-workflow"
        assert body["variables"] == {"orderId": "ORD-1"}


# ------------------------------------------------------------------ #
# get_instance / get_instance_history / cancel_instance               #
# ------------------------------------------------------------------ #


class TestInstanceOperations:
    def test_get_instance(
        self, client: RestEngineClient, httpx_mock: HTTPXMock, sample_instance: dict
    ) -> None:
        httpx_mock.add_response(
            method="GET",
            url=f"{BASE_URL}/v1/instances/inst-001",
            json=sample_instance,
        )
        result = client.get_instance("inst-001")
        assert result["status"] == "ACTIVE"

    def test_get_instance_history(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        history_items = [{"id": "exec-1", "stepId": "step-1", "status": "COMPLETED"}]
        httpx_mock.add_response(
            method="GET",
            url=f"{BASE_URL}/v1/instances/inst-001/history",
            json={"items": history_items},
        )
        result = client.get_instance_history("inst-001")
        assert len(result) == 1
        assert result[0]["stepId"] == "step-1"

    def test_cancel_instance(
        self, client: RestEngineClient, httpx_mock: HTTPXMock, sample_instance: dict
    ) -> None:
        cancelled = {**sample_instance, "status": "CANCELLED"}
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/instances/inst-001/cancel",
            json=cancelled,
        )
        result = client.cancel_instance("inst-001")
        assert result["status"] == "CANCELLED"


# ------------------------------------------------------------------ #
# complete_user_task                                                   #
# ------------------------------------------------------------------ #


class TestCompleteUserTask:
    def test_sends_variables_to_correct_url(
        self, client: RestEngineClient, httpx_mock: HTTPXMock
    ) -> None:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE_URL}/v1/user-tasks/task-001/complete",
            status_code=200,
        )
        client.complete_user_task("task-001", completed_by="alice", result={"approved": True})

        request = httpx_mock.get_request()
        assert request is not None
        body = json.loads(request.content)
        assert body["completedBy"] == "alice"
        assert body["result"] == {"approved": True}
