"""gRPC transport implementation of EngineClient (worker-facing operations only)."""

from __future__ import annotations

from typing import Any

import grpc
from google.protobuf import json_format
from google.protobuf import struct_pb2

from workflow_sdk.internal.gen.workflow.v1 import engine_pb2
from workflow_sdk.internal.gen.workflow.v1 import engine_pb2_grpc


class GrpcEngineClient:
    """Communicates with the workflow engine over gRPC.

    Only implements the three operations used by the worker runner:
    poll_jobs, complete_job, and fail_job.

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
    ) -> list[dict[str, Any]]:
        req = engine_pb2.PollJobsRequest(
            worker_id=worker_id,
            job_types=job_types,
            max_jobs=max_jobs,
        )
        resp = self._stub.PollJobs(req)
        return [_proto_job_to_dict(j) for j in resp.jobs]

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
        self._stub.CompleteJob(req)

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
        ))

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
    }
