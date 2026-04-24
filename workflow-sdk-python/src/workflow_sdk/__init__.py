"""Rochallor Workflow Engine — Python SDK.

Quickstart::

    from workflow_sdk.client import RestEngineClient
    from workflow_sdk.handler import HandlerRegistry
    from workflow_sdk.runner import Runner

    client = RestEngineClient("http://localhost:8080")
    registry = HandlerRegistry()

    def my_handler(ctx):
        return {"result": "ok"}

    registry.register("my-job-type", my_handler)
    runner = Runner(client, registry, worker_id="worker-1")
    runner.run()  # blocks until SIGTERM/SIGINT
"""

__version__ = "1.0.0"

__all__ = ["__version__"]
