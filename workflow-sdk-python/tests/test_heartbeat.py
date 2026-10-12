import time
from datetime import datetime, timedelta, timezone
from threading import Event, Thread
from unittest.mock import MagicMock

import pytest

from workflow_sdk.handler.registry import HandlerRegistry
from workflow_sdk.runner.runner import Runner


def expiry():
    return (datetime.now(timezone.utc) + timedelta(seconds=.2)).isoformat()


def setup(**options):
    job = {"id": "job", "jobType": "work", "lockExpiresAt": expiry(), "retriesRemaining": 2}
    client = MagicMock()
    client.poll_jobs.side_effect = lambda *args, **kwargs: [job] if client.poll_jobs.call_count == 1 else []
    client.extend_job_lease = MagicMock(side_effect=lambda *args: expiry())
    registry = HandlerRegistry()
    runner = Runner(client, registry, "worker", parallelism=1, poll_interval=.005,
                    heartbeat=True, heartbeat_interval=.025, shutdown_timeout=.06, **options)
    return client, registry, runner, job


def test_heartbeat_renews_through_handler_and_immutable_callback_retry():
    client, registry, runner, job = setup()
    handler = MagicMock(side_effect=lambda ctx: (time.sleep(.3), {"frozen": 1})[1])
    registry.register("work", handler)
    seen = []
    release_at = time.monotonic() + .5
    def callback(job_id, worker, variables):
        seen.append(dict(variables))
        variables["frozen"] = 9
        if time.monotonic() < release_at:
            raise RuntimeError("unavailable")
    client.complete_job.side_effect = callback
    runner._dispatch(job)
    assert handler.call_count == 1
    assert client.extend_job_lease.call_count >= 10
    assert len(seen) > 1 and all(value == {"frozen": 1} for value in seen)
    client.fail_job.assert_not_called()


def test_lost_lease_retains_uncooperative_handler_and_shutdown_is_bounded():
    from workflow_sdk.errors import LeaseLostError
    client, registry, runner, job = setup()
    client.extend_job_lease.side_effect = [expiry(), LeaseLostError("lost")]
    entered, release, stop = Event(), Event(), Event()
    context = {}
    def handler(ctx):
        context.update(ctx)
        entered.set()
        release.wait(2)
        return {}
    registry.register("work", handler)
    errors = []
    def run():
        try: runner.run(stop)
        except Exception as error: errors.append(error)
    running = Thread(target=run)
    running.start()
    try:
        assert entered.wait(1)
        assert context["cancel_event"].wait(1)
        assert client.poll_jobs.call_count == 1
        stop.set()
        running.join(.5)
        assert not running.is_alive()
        assert len(errors) == 1 and "shutdown timed out" in str(errors[0])
        with pytest.raises(RuntimeError, match="already running"):
            runner.run(Event())
    finally:
        stop.set(); release.set(); running.join(1)
    client.complete_job.assert_not_called()
    client.fail_job.assert_not_called()


def test_unsupported_engine_fails_before_handler():
    from workflow_sdk.errors import HeartbeatUnsupportedError
    client, registry, runner, _ = setup()
    client.extend_job_lease.side_effect = HeartbeatUnsupportedError("heartbeat unsupported")
    handler = MagicMock(return_value={})
    registry.register("work", handler)
    with pytest.raises(HeartbeatUnsupportedError):
        runner.run(Event())
    handler.assert_not_called()
    client.fail_job.assert_not_called()


def test_hung_custom_heartbeat_cannot_keep_attempt_alive():
    client, registry, runner, job = setup()
    release = Event()
    client.extend_job_lease.side_effect = lambda *args: release.wait(2)
    handler = MagicMock(return_value={})
    registry.register("work", handler)
    started = time.monotonic()
    try:
        runner._dispatch(job)
        assert time.monotonic() - started < .6
    finally:
        release.set()
    assert client.extend_job_lease.call_count == 1
    handler.assert_not_called()


def test_pure_python_cpu_handler_does_not_starve_heartbeat():
    client, registry, runner, job = setup()
    def handler(ctx):
        until = time.monotonic() + .4
        while time.monotonic() < until:
            sum(range(1000))
        return {"ok": True}
    registry.register("work", handler)
    runner._dispatch(job)
    assert client.extend_job_lease.call_count >= 5
    client.complete_job.assert_called_once()


def test_legacy_custom_client_still_works_without_heartbeat():
    class Legacy:
        def poll_jobs(self, worker_id, job_types, max_jobs=1): return []
        def complete_job(self, job_id, worker_id, variables=None): pass
        def fail_job(self, job_id, worker_id, error_message, retryable=True): pass
    stop = Event(); stop.set()
    Runner(Legacy(), HandlerRegistry(), "w").run(stop)
    with pytest.raises(Exception, match="extend_job_lease"):
        Runner(Legacy(), HandlerRegistry(), "w", heartbeat=True).run(stop)


@pytest.mark.parametrize("options", [{"lease_seconds": 4}, {"lease_seconds": 3601}, {"heartbeat_interval": 11},
    {"shutdown_timeout": 0}, {"shutdown_timeout": float("inf")}, {"shutdown_timeout": float("nan")}])
def test_invalid_heartbeat_options(options):
    with pytest.raises(ValueError):
        Runner(MagicMock(), HandlerRegistry(), "worker", heartbeat=True, **options)
