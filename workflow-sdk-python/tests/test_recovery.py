from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from threading import Event
from unittest.mock import MagicMock

from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.runner.runner import Runner


def job():
    return {"id": "j1", "jobType": "test", "lockExpiresAt": (datetime.now(timezone.utc) + timedelta(seconds=2)).isoformat()}


def test_completion_retries_without_rerunning_handler():
    client = MagicMock()
    client.complete_job.side_effect = [RuntimeError("lost response"), None]
    registry = HandlerRegistry()
    handler = MagicMock(return_value={"value": 1})
    registry.register("test", handler)
    Runner(client, registry, "w")._dispatch(job())
    assert client.complete_job.call_count == 2
    assert handler.call_count == 1
    client.fail_job.assert_not_called()


def test_partial_capacity_and_pending_callbacks_bound_claims():
    client = MagicMock()
    client.poll_jobs.side_effect = lambda *args, **kwargs: [job()]
    registry = HandlerRegistry()
    entered, release = Event(), Event()
    def handler(ctx):
        entered.set()
        release.wait(2)
        return {}
    registry.register("test", handler)
    runner = Runner(client, registry, "w", parallelism=2)
    pending = []
    with ThreadPoolExecutor(max_workers=2) as executor:
        try:
            runner._poll_and_dispatch(executor, pending, ["test"])
            assert entered.wait(1)
            runner._poll_and_dispatch(executor, pending, ["test"])
            runner._poll_and_dispatch(executor, pending, ["test"])
            assert [c.kwargs["max_jobs"] for c in client.poll_jobs.call_args_list] == [2, 1]
        finally:
            release.set()


def test_failure_retry_keeps_reason_and_budget():
    from workflow_sdk.errors import NonRetryableError
    client = MagicMock()
    client.fail_job.side_effect = [RuntimeError("lost response"), None]
    registry = HandlerRegistry()
    handler = MagicMock(side_effect=NonRetryableError("business"))
    registry.register("test", handler)
    Runner(client, registry, "w")._dispatch(job())
    assert handler.call_count == 1
    assert client.fail_job.call_count == 2
    assert all(call.args == ("j1", "w", "business", False) for call in client.fail_job.call_args_list)
    client.complete_job.assert_not_called()


def test_callback_pending_holds_slot_until_expiry():
    import time
    client = MagicMock()
    lease = job()
    lease["lockExpiresAt"] = (datetime.now(timezone.utc) + timedelta(seconds=.15)).isoformat()
    client.poll_jobs.return_value = [lease]
    client.complete_job.side_effect = RuntimeError("unavailable")
    registry = HandlerRegistry()
    handler = MagicMock(return_value={"result": 1})
    registry.register("test", handler)
    runner = Runner(client, registry, "w", parallelism=1)
    pending = []
    with ThreadPoolExecutor(max_workers=1) as executor:
        runner._poll_and_dispatch(executor, pending, ["test"])
        time.sleep(.03)
        runner._poll_and_dispatch(executor, pending, ["test"])
        assert client.poll_jobs.call_count == 1
        pending[0].result(timeout=1)
        attempts = client.complete_job.call_count
        time.sleep(.15)
        assert client.complete_job.call_count == attempts >= 2
    assert handler.call_count == 1
    client.fail_job.assert_not_called()


def test_expired_running_handler_keeps_slot_and_shutdown_drains():
    import time
    from threading import Thread
    client = MagicMock()
    lease = job()
    lease["lockExpiresAt"] = (datetime.now(timezone.utc) + timedelta(seconds=.04)).isoformat()
    client.poll_jobs.return_value = [lease]
    entered, release, stop = Event(), Event(), Event()
    registry = HandlerRegistry()
    def handler(ctx):
        entered.set()
        release.wait(2)
        return {}
    registry.register("test", handler)
    runner = Runner(client, registry, "w", parallelism=1, poll_interval=.005)
    running = Thread(target=runner.run, args=(stop,))
    running.start()
    try:
        assert entered.wait(1)
        time.sleep(.08)
        assert client.poll_jobs.call_count == 1
        stop.set()
        running.join(.03)
        assert running.is_alive()
    finally:
        stop.set()
        release.set()
        running.join(1)
    assert not running.is_alive()
    client.complete_job.assert_not_called()
    client.fail_job.assert_not_called()


def test_legacy_client_without_lease_gets_one_attempt_and_immutable_payload():
    client = MagicMock()
    seen = []
    def completion(job_id, worker, variables):
        seen.append(dict(variables))
        variables["v"] = 9
        raise RuntimeError("unavailable")
    client.complete_job.side_effect = completion
    registry = HandlerRegistry()
    registry.register("test", lambda ctx: {"v": 1})
    legacy = job()
    del legacy["lockExpiresAt"]
    Runner(client, registry, "w")._dispatch(legacy)
    assert seen == [{"v": 1}]
    current = job()
    current["lockExpiresAt"] = (datetime.now(timezone.utc) + timedelta(seconds=.15)).isoformat()
    Runner(client, registry, "w")._dispatch(current)
    assert len(seen) >= 3 and all(value == {"v": 1} for value in seen)


def test_grpc_mapping_and_scoped_timeout():
    from workflow_sdk.client.grpc import GrpcEngineClient, _proto_job_to_dict
    from workflow_sdk.client.deadline import request_timeout
    from workflow_sdk.internal.gen.workflow.v1 import engine_pb2
    message = engine_pb2.Job(id="j1")
    message.lock_expires_at.FromJsonString("2026-10-11T00:00:30Z")
    assert _proto_job_to_dict(message)["lockExpiresAt"] == "2026-10-11T00:00:30Z"
    client = GrpcEngineClient("localhost:1")
    client._stub = MagicMock()
    try:
        with request_timeout(.25):
            client.complete_job("j1", "w", {})
        assert client._stub.CompleteJob.call_args.kwargs["timeout"] == .25
    finally:
        client.close()


def test_permanent_callback_rejection_is_not_retried():
    from workflow_sdk.errors import EngineClientError
    client = MagicMock()
    client.complete_job.side_effect = EngineClientError(403, "denied")
    registry = HandlerRegistry()
    registry.register("test", lambda ctx: {})
    Runner(client, registry, "w")._dispatch(job())
    assert client.complete_job.call_count == 1
    client.fail_job.assert_not_called()
