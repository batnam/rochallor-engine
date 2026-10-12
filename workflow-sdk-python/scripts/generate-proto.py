"""Generate with Python 3.12, grpcio-tools==1.60.0 and setuptools==80.9.0.

This compiler preserves compatibility with the declared protobuf >=4.25 runtime.
"""
from pathlib import Path
import subprocess
import sys

sdk = Path(__file__).resolve().parents[1]
proto = sdk.parent / "proto"
output = sdk / "src/workflow_sdk/internal/gen"
subprocess.run([sys.executable, "-m", "grpc_tools.protoc", f"-I{proto}",
                f"--python_out={output}",
                str(proto / "workflow/v1/engine.proto")], check=True)
stub = output / "workflow/v1/engine_pb2_grpc.py"
# Keep the SDK's worker-only binding; no server implementation is shipped.
source = '''# Generated worker RPC bindings by scripts/generate-proto.py.
from workflow_sdk.internal.gen.workflow.v1 import engine_pb2


class WorkflowEngineStub:
    def __init__(self, channel):
'''
for method in ("PollJobs", "CompleteJob", "FailJob", "ExtendJobLease"):
    source += f'''        self.{method} = channel.unary_unary(
            '/workflow.v1.WorkflowEngine/{method}',
            request_serializer=engine_pb2.{method}Request.SerializeToString,
            response_deserializer=engine_pb2.{method}Response.FromString,
        )
'''
stub.write_text(source)
messages = output / "workflow/v1/engine_pb2.py"
messages.write_text(messages.read_text().replace("'workflow.v1.engine_pb2'",
                                               "'workflow_sdk.internal.gen.workflow.v1.engine_pb2'"))
