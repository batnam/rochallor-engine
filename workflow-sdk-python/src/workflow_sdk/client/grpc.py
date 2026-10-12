"""gRPC transport implementation of EngineClient (worker-facing operations only)."""

from __future__ import annotations

from typing import Any

import grpc
from google.protobuf import json_format
from google.protobuf import struct_pb2

from workflow_sdk.client.deadline import current_lease_seconds, current_timeout
from workflow_sdk.errors import HeartbeatUnsupportedError, LeaseLostError, WorkflowSDKError
from workflow_sdk.internal.gen.workflow.v1 import engine_pb2
from workflow_sdk.internal.gen.workflow.v1 import engine_pb2_grpc


class GrpcEngineClient:
    """Communicates with the workflow engine over gRPC.

    Implements worker polling/callbacks and the optional extend_job_lease capability.

    Args:
        target: gRPC server address, e.g. ``"localhost:9090"``.
    """

    def __init__(self, target: str) -> None:
        self._channel = grpc.insecure_channel(target)
        self._stub = engine_pb2_grpc.WorkflowEngineStub(self._channel)

    def poll_jobs(
        self,
        worker_id: str,
        job_types: list[str],
        max_jobs: int = 1,
        lease_seconds: int | None = None,
    ) -> list[dict[str, Any]]:
        req = engine_pb2.PollJobsRequest(
            worker_id=worker_id,
            job_types=job_types,
            max_jobs=max_jobs,
            lease_seconds=current_lease_seconds() if lease_seconds is None else lease_seconds,
        )
        resp = self._stub.PollJobs(req, timeout=current_timeout())
        return [_proto_job_to_dict(j) for j in resp.jobs]

    def extend_job_lease(self, job_id: str, worker_id: str, lease_seconds: int = 0) -> str:
        try:
            response = self._stub.ExtendJobLease(engine_pb2.ExtendJobLeaseRequest(
                job_id=job_id, worker_id=worker_id, lease_seconds=lease_seconds,
            ), timeout=current_timeout())
        except grpc.RpcError as error:
            if error.code() == grpc.StatusCode.UNIMPLEMENTED:
                raise HeartbeatUnsupportedError("engine does not support heartbeat (extend_job_lease)") from error
            if error.code() == grpc.StatusCode.FAILED_PRECONDITION:
                raise LeaseLostError("job lease lost") from error
            raise
        if not response.accepted or not response.HasField("lock_expires_at"):
            raise WorkflowSDKError("invalid heartbeat response")
        return str(response.lock_expires_at.ToJsonString())

    def complete_job(
        self,
        job_id: str,
        worker_id: str,
        variables: dict[str, Any] | None = None,
    ) -> None:
        req = engine_pb2.CompleteJobRequest(job_id=job_id, worker_id=worker_id)
        if variables:
            s = struct_pb2.Struct()
            s.update(variables)
            req.variables_to_set.CopyFrom(s)
        self._stub.CompleteJob(req, timeout=current_timeout())

    def fail_job(
        self,
        job_id: str,
        worker_id: str,
        error_message: str,
        retryable: bool = True,
    ) -> None:
        self._stub.FailJob(engine_pb2.FailJobRequest(
            job_id=job_id,
            worker_id=worker_id,
            error_message=error_message,
            retryable=retryable,
        ), timeout=current_timeout())

    def close(self) -> None:
        self._channel.close()


def _proto_job_to_dict(job: Any) -> dict[str, Any]:
    variables: dict[str, Any] = {}
    if job.HasField("variables"):
        variables = json_format.MessageToDict(job.variables)
    return {
        "id": job.id,
        "jobType": job.job_type,
        "instanceId": job.instance_id,
        "stepExecutionId": job.step_execution_id,
        "retriesRemaining": job.retries_remaining,
        "variables": variables,
        **({"lockExpiresAt": job.lock_expires_at.ToJsonString()} if job.HasField("lock_expires_at") else {}),
    }
