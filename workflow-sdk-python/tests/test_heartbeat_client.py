import json
from unittest.mock import MagicMock

import grpc
import pytest

from workflow_sdk.client.deadline import request_lease, request_timeout
from workflow_sdk.client.rest import RestEngineClient
from workflow_sdk.client.grpc import GrpcEngineClient
from workflow_sdk.errors import EngineClientError, HeartbeatUnsupportedError, LeaseLostError, WorkflowSDKError
from workflow_sdk.internal.gen.workflow.v1 import engine_pb2


def test_rest_poll_lease_and_heartbeat(httpx_mock):
    httpx_mock.add_response(json={"jobs": []})
    httpx_mock.add_response(json={"accepted": True, "lockExpiresAt": "2026-10-11T12:00:00Z"})
    with RestEngineClient("http://engine") as client:
        with request_lease(60):
            client.poll_jobs("w", ["job"])
        assert client.extend_job_lease("job", "w", 60) == "2026-10-11T12:00:00Z"
    poll, heartbeat = httpx_mock.get_requests()
    assert json.loads(poll.content)["leaseSeconds"] == 60
    assert heartbeat.url.path == "/v1/jobs/job/heartbeat"
    assert json.loads(heartbeat.content) == {"workerId": "w", "leaseSeconds": 60}


@pytest.mark.parametrize("status,error", [(404, HeartbeatUnsupportedError), (405, HeartbeatUnsupportedError),
    (501, HeartbeatUnsupportedError), (409, LeaseLostError), (503, EngineClientError)])
def test_rest_heartbeat_error_mapping(httpx_mock, status, error):
    httpx_mock.add_response(status_code=status)
    with RestEngineClient("http://engine") as client, pytest.raises(error):
        client.extend_job_lease("job", "w")


@pytest.mark.parametrize("body", [{"accepted": False}, {"accepted": True, "lockExpiresAt": "bad"},
    {"accepted": True, "lockExpiresAt": "2026-10-11T12:00:00"}])
def test_rest_requires_accepted_timestamp(httpx_mock, body):
    httpx_mock.add_response(json=body)
    with RestEngineClient("http://engine") as client, pytest.raises(WorkflowSDKError):
        client.extend_job_lease("job", "w")


def test_grpc_poll_lease_and_heartbeat():
    client = GrpcEngineClient("localhost:1")
    client._stub = MagicMock()
    response = engine_pb2.ExtendJobLeaseResponse(accepted=True)
    response.lock_expires_at.FromJsonString("2026-10-11T12:00:00Z")
    client._stub.ExtendJobLease.return_value = response
    try:
        with request_timeout(.25), request_lease(60):
            client.poll_jobs("w", ["job"])
            assert client.extend_job_lease("job", "w", 60) == "2026-10-11T12:00:00Z"
        assert client._stub.PollJobs.call_args.args[0].lease_seconds == 60
        call = client._stub.ExtendJobLease.call_args
        assert call.args[0].job_id == "job" and call.args[0].lease_seconds == 60
        assert call.kwargs["timeout"] == .25
    finally: client.close()


@pytest.mark.parametrize("code,error", [(grpc.StatusCode.UNIMPLEMENTED, HeartbeatUnsupportedError),
    (grpc.StatusCode.FAILED_PRECONDITION, LeaseLostError), (grpc.StatusCode.UNAVAILABLE, grpc.RpcError)])
def test_grpc_heartbeat_errors(code, error):
    class Failure(grpc.RpcError):
        def code(self): return code
    client = GrpcEngineClient("localhost:1")
    client._stub = MagicMock()
    client._stub.ExtendJobLease.side_effect = Failure()
    try:
        with pytest.raises(error): client.extend_job_lease("job", "w")
    finally: client.close()
