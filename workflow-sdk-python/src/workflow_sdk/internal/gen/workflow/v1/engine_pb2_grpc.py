# Generated worker RPC bindings by scripts/generate-proto.py.
from workflow_sdk.internal.gen.workflow.v1 import engine_pb2


class WorkflowEngineStub:
    def __init__(self, channel):
        self.PollJobs = channel.unary_unary(
            '/workflow.v1.WorkflowEngine/PollJobs',
            request_serializer=engine_pb2.PollJobsRequest.SerializeToString,
            response_deserializer=engine_pb2.PollJobsResponse.FromString,
        )
        self.CompleteJob = channel.unary_unary(
            '/workflow.v1.WorkflowEngine/CompleteJob',
            request_serializer=engine_pb2.CompleteJobRequest.SerializeToString,
            response_deserializer=engine_pb2.CompleteJobResponse.FromString,
        )
        self.FailJob = channel.unary_unary(
            '/workflow.v1.WorkflowEngine/FailJob',
            request_serializer=engine_pb2.FailJobRequest.SerializeToString,
            response_deserializer=engine_pb2.FailJobResponse.FromString,
        )
        self.ExtendJobLease = channel.unary_unary(
            '/workflow.v1.WorkflowEngine/ExtendJobLease',
            request_serializer=engine_pb2.ExtendJobLeaseRequest.SerializeToString,
            response_deserializer=engine_pb2.ExtendJobLeaseResponse.FromString,
        )
