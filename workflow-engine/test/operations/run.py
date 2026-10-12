#!/usr/bin/env python3
"""Readiness and migration CLI checks on disposable databases and processes."""
import argparse
import json
from pathlib import Path
import socket
import subprocess
import sys
import time
from urllib.error import HTTPError
from urllib.request import Request, urlopen

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from support import EngineStack, build_engine


def request(base, path, body=None, timeout=2):
    data = json.dumps(body).encode() if body is not None else None
    try:
        response = urlopen(Request(base + path, data=data, headers={"Content-Type": "application/json"}), timeout=timeout)
    except HTTPError as exc:
        response = exc
    with response:
        raw = response.read()
        return response.status, json.loads(raw) if raw and response.headers.get_content_type() == "application/json" else raw.decode()


def await_condition(check, seconds=20):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if check():
            return
        time.sleep(0.2)
    raise AssertionError("condition did not become true before deadline")


def readiness(binary, output, baseline=False):
    for mode in ["polling", "kafka_outbox"]:
        with EngineStack(binary, output / mode, mode) as stack:
            stack.start()
            assert request(stack.base, "/healthz")[0] == 200
            if not baseline:
                assert request(stack.base, "/readyz") == (200, {"status": "ready"})
            stack.docker("pause", stack.database)
            try:
                before = time.monotonic()
                ready = request(stack.base, "/readyz")
                elapsed = time.monotonic() - before
                health = request(stack.base, "/healthz")
                evidence = {"mode": mode, "readyDuringDBOutage": ready,
                            "readySeconds": elapsed, "livenessDuringDBOutage": health,
                            "processAlive": stack.process.poll() is None}
                (stack.output / "outage.json").write_text(json.dumps(evidence, indent=2))
                print(json.dumps(evidence), flush=True)
                if not baseline:
                    assert ready == (503, {"status": "not_ready"}) and elapsed < 1.5
                    assert health[0] == 200 and stack.process.poll() is None
            finally:
                stack.docker("unpause", stack.database)
            if baseline:
                continue
            await_condition(lambda: request(stack.base, "/readyz")[0] == 200)
            if mode == "kafka_outbox":
                stack.docker("exec", stack.broker, "kafka-topics", "--bootstrap-server", "localhost:9094",
                             "--create", "--topic", "workflow.jobs.ready-probe", "--partitions", "1",
                             "--replication-factor", "1")
                definition = {"id": "ready-outbox", "name": "Ready outbox", "steps": [
                    {"id": "work", "name": "Work", "type": "SERVICE_TASK", "jobType": "ready-probe", "nextStep": "end"},
                    {"id": "end", "name": "End", "type": "END"}]}
                assert request(stack.base, "/v1/definitions", definition)[0] == 201
                # Stop a real broker after runtime initialization. Durable writes
                # stay available; backlog must remain observable and drain later.
                stack.docker("pause", stack.broker)
                try:
                    assert request(stack.base, "/v1/instances", {"definitionId": definition["id"]})[0] == 201
                    assert request(stack.base, "/readyz")[0] == 200
                    assert stack.sql("SELECT count(*) FROM dispatch_outbox") == "1"
                    def backlog_visible():
                        _, metrics = request(f"http://127.0.0.1:{stack.metrics_port}", "/metrics")
                        return "workflow_engine_dispatch_outbox_backlog 1\n" in metrics
                    await_condition(backlog_visible, 10)
                finally:
                    stack.docker("unpause", stack.broker)
                await_condition(lambda: stack.sql("SELECT count(*) FROM dispatch_outbox") == "0", 30)
                assert stack.sql("SELECT count(*) FROM job") == "1"
                print("PASS broker outage: readiness 200, durable backlog 1 → 0", flush=True)
            print(f"PASS readiness DB outage/recovery ({mode})", flush=True)


def migrations(binary, output, baseline=False):
    # Kafka intentionally absent. CLI must ignore unrelated runtime settings.
    with EngineStack(binary, output, "polling") as stack:
        env = dict(stack.env, WE_DISPATCH_MODE="kafka_outbox", WE_REST_PORT="not-a-runtime-port",
                   WE_KAFKA_SEED_BROKERS="", WE_KAFKA_TRANSPORT="")
        def invoke(args, expected=0, environment=env):
            number = len(list(output.glob("command-*.log"))) + 1
            try:
                result = subprocess.run([str(binary), *args], env=environment, capture_output=True,
                                        text=True, timeout=20)
            except subprocess.TimeoutExpired as exc:
                stdout = exc.stdout.decode() if isinstance(exc.stdout, bytes) else (exc.stdout or "")
                stderr = exc.stderr.decode() if isinstance(exc.stderr, bytes) else (exc.stderr or "")
                (output / f"command-{number}.log").write_text(f"args={args}\nTIMEOUT after 20s\n" + stdout + stderr)
                raise AssertionError(f"migration process did not exit: {args}") from exc
            (output / f"command-{number}.log").write_text(f"args={args}\nexit={result.returncode}\n" + result.stdout + result.stderr)
            print(f"CLI {args}: exit {result.returncode}", flush=True)
            if not baseline:
                assert (result.returncode == 0) == (expected == 0), result.stdout + result.stderr
                assert "REST listening" not in result.stdout + result.stderr
                assert "dispatch runtime" not in result.stdout + result.stderr
                for port in [stack.rest_port, stack.grpc_port, stack.metrics_port]:
                    with socket.socket() as sock:
                        assert sock.connect_ex(("127.0.0.1", port)) != 0
            return result
        if baseline:
            # Ignored flag reaches runtime configuration, proving it was not
            # parsed before config. No destructive down command is executed.
            result = invoke(["-migrate-only"])
            assert "WE_REST_PORT" in result.stdout + result.stderr
            return
        invoke(["-migrate-only"])
        version = stack.sql("SELECT version || ':' || dirty FROM schema_migrations")
        assert version.endswith(":false"), version
        invoke(["-migrate-only"])
        assert stack.sql("SELECT version || ':' || dirty FROM schema_migrations") == version
        invoke(["-migrate-down"])
        assert stack.sql("SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='job'") == "0"
        invoke(["-migrate-only"])
        assert stack.sql("SELECT version || ':' || dirty FROM schema_migrations") == version
        for flags in [["-unknown"], ["-migrate-only", "-migrate-down"], ["unexpected-argument"]]:
            invoke(flags, 1)
        invoke(["-migrate-only"], 1, dict(env, WE_POSTGRES_DSN="postgres://workflow:workflow@127.0.0.1:1/workflow?sslmode=disable"))
        stack.sql("UPDATE schema_migrations SET dirty=true")
        invoke(["-migrate-only"], 1)
        stack.sql("UPDATE schema_migrations SET dirty=false")
        # Driver initialization errors must also release its owned SQL connection.
        stack.sql("DROP SCHEMA public CASCADE")
        invoke(["-migrate-only"], 1)
        print("PASS migration up/no-change/down-all/up and failure exits without runtime", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", choices=["all", "readiness", "migrations"], default="all")
    parser.add_argument("--engine")
    parser.add_argument("--output", default="testresults/engine-operations")
    parser.add_argument("--baseline", action="store_true", help="Capture known baseline failures; does not certify a candidate")
    args = parser.parse_args()
    output = Path(args.output).resolve()
    binary = Path(args.engine).resolve() if args.engine else build_engine(output / "engine")
    if args.case in ["all", "readiness"]:
        readiness(binary, output / "readiness", args.baseline)
    if args.case in ["all", "migrations"]:
        migrations(binary, output / "migrations", args.baseline)


if __name__ == "__main__":
    main()
