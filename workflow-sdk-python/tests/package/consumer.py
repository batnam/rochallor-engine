from importlib.metadata import version
from threading import Event

from workflow_sdk import __version__
from workflow_sdk.client.rest import RestEngineClient
from workflow_sdk.client.grpc import GrpcEngineClient
from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.runner.runner import Runner
from workflow_sdk.runner.kafka_runner import KafkaRunner
from workflow_sdk.internal.gen.workflow.v1 import engine_pb2

assert __version__ == version("rochallor-sdk")
message = engine_pb2.JobDispatchEvent(job_id="consumer", dedup_id="delivery")
assert engine_pb2.JobDispatchEvent.FromString(message.SerializeToString()).job_id == "consumer"
registry = HandlerRegistry()
registry.register("smoke", lambda ctx: {"ok": True})
stop = Event()
stop.set()

class LegacyClient:
    def poll_jobs(self, worker_id, job_types, max_jobs=1): return []
    def complete_job(self, job_id, worker_id, variables=None): pass
    def fail_job(self, job_id, worker_id, error_message, retryable=True): pass

Runner(LegacyClient(), registry, "legacy").run(stop)
for client in [RestEngineClient("http://localhost:1"), GrpcEngineClient("localhost:1")]:
    try:
        Runner(client, registry, "consumer").run(stop)
        kafka = KafkaRunner("consumer", "localhost:1", client, registry)
        kafka.stop()
        kafka.dedup_cache.close()
    finally:
        client.close()
print("External distribution consumer passed:", __version__)
