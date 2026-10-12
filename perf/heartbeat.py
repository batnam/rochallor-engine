#!/usr/bin/env python3
"""Paired SDK heartbeat cost gate: identical images, fresh DBs, renewal off/on.

Uses the verified E2E worker's opt-in heartbeat registry and T02 resource sampler.
This small controlled workload measures renewal cost, not maximum capacity.
"""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import math
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def percentile(values, percent):
    return sorted(values)[math.ceil(len(values) * percent / 100) - 1]


def validate_pair(off, on):
    for key in ("harnessSha", "config", "images", "host"):
        if off[key] != on[key]:
            raise ValueError(f"incompatible pair: {key}")
    if off["heartbeat"] is not False or on["heartbeat"] is not True:
        raise ValueError("pair must compare heartbeat off then on")
    for run in (off, on):
        count = run["config"]["cases"]
        if len(run["workflows"]) != count or len({row["id"] for row in run["workflows"]}) != count:
            raise ValueError("missing/duplicate workflows")
        if any(row["status"] != "COMPLETED" or row["handlerCalls"] != 1 or row["remaining"] != 2 for row in run["workflows"]):
            raise ValueError("unexpected workflow outcome or duplicate handler")
        if run["database"]["jobs"] != count or run["database"]["completedJobs"] != count:
            raise ValueError("unexpected replacement/incomplete job")
        if run["database"]["writerConnections"] != 0:
            raise ValueError("database writers were not drained before reading statistics")
        if not isinstance(run["database"]["jobUpdates"], int) or run["database"]["jobUpdates"] < count * 2:
            raise ValueError("missing DB update evidence")
        if run["timeouts"] != 0:
            raise ValueError("lease expired during cost measurement")
        for field in ("workflowP95Ms", "startP95Ms", "workflowsPerSecond"):
            value = run[field]
            if not isinstance(value, (float, int)) or not math.isfinite(value) or value <= 0:
                raise ValueError(f"invalid {field}")
        if run["startP95Ms"] >= 1000 or run["workflowP95Ms"] >= 30000:
            raise ValueError("latency threshold breached")
        host = run["resources"]
        if host.get("errors") or not host["samples"] or host["summary"]["sampleCount"] != len(host["samples"]):
            raise ValueError("missing resource evidence")
        peak = host["summary"]["k6CapacityPctPeak"]
        if not isinstance(peak, (float, int)) or not math.isfinite(peak) or peak < 0 or peak >= 90:
            raise ValueError("generator saturated or CPU evidence invalid")
    if off["acceptedHeartbeats"] != 0 or on["acceptedHeartbeats"] < on["config"]["cases"] * 2:
        raise ValueError("heartbeat was not actually toggled/renewed")
    if on["heartbeatTxMeanMs"] is None or not 0 <= on["heartbeatTxMeanMs"] < 1000:
        raise ValueError("missing/invalid heartbeat transaction latency")
    writes = on["database"]["jobUpdates"] - off["database"]["jobUpdates"]
    if writes < on["acceptedHeartbeats"]:
        raise ValueError("DB writes do not account for accepted renewals")
    if on["workflowP95Ms"] > off["workflowP95Ms"] * 1.15:
        raise ValueError("workflow p95 regressed by more than 15%")
    if on["workflowsPerSecond"] < off["workflowsPerSecond"] * .9:
        raise ValueError("workflow throughput regressed by more than 10%")
    return {"result": "pass", "additionalJobUpdates": writes,
            "workflowP95ChangePct": (on["workflowP95Ms"] / off["workflowP95Ms"] - 1) * 100,
            "throughputChangePct": (on["workflowsPerSecond"] / off["workflowsPerSecond"] - 1) * 100}


def request(base, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as response:
        return json.load(response)


def metric(snapshot, name, labels="", default=None):
    match = re.search(r"^" + re.escape(name + labels) + r" ([0-9.eE+\-]+)$", snapshot, re.M)
    if match is None:
        if default is not None:
            return default
        raise ValueError(f"missing engine metric: {name}{labels}")
    return float(match[1])


def database_snapshot(compose, env, log):
    # Cumulative statistics can remain local to an idle backend for longer than
    # a fixed sleep. Close engine clients, then read from a fresh transaction.
    subprocess.run([*compose, "stop", "-t", "15", "engine"], env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
    psql = [*compose, "exec", "-T", "postgres", "psql", "-U", "workflow", "-d", "workflow", "-At", "-v", "ON_ERROR_STOP=1", "-c"]
    clients = "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND backend_type='client backend' AND pid<>pg_backend_pid()"
    deadline = time.monotonic() + 5
    while True:
        remaining = int(subprocess.check_output([*psql, clients], env=env, text=True))
        if remaining == 0:
            break
        if time.monotonic() >= deadline:
            raise RuntimeError("database clients did not exit before statistics collection")
        time.sleep(.1)
    sql = "SELECT json_build_object('jobs',(SELECT count(*) FROM job),'completedJobs',(SELECT count(*) FROM job WHERE status='COMPLETED'),'jobUpdates',(SELECT n_tup_upd FROM pg_stat_user_tables WHERE relname='job'),'tables',(SELECT json_agg(row_to_json(s)) FROM (SELECT relname,n_tup_ins,n_tup_upd,n_tup_del FROM pg_stat_user_tables ORDER BY relname) s));"
    result = json.loads(subprocess.check_output([*psql, sql], env=env, text=True))
    result["writerConnections"] = remaining
    return result


def measure(args, heartbeat, identity):
    directory = args.output / ("on" if heartbeat else "off")
    directory.mkdir(parents=True, exist_ok=True)
    sockets = [socket.socket() for _ in range(4)]
    for sock in sockets:
        sock.bind(("127.0.0.1", 0))
    db_port, rest_port, grpc_port, metrics_port = [sock.getsockname()[1] for sock in sockets]
    for sock in sockets:
        sock.close()
    project = "heartbeat-" + uuid.uuid4().hex[:12]
    env = {**os.environ, "COMPOSE_PROJECT_NAME": project, "PERF_POSTGRES_PORT": str(db_port),
           "PERF_ENGINE_REST_PORT": str(rest_port), "PERF_ENGINE_GRPC_PORT": str(grpc_port),
           "PERF_ENGINE_METRICS_PORT": str(metrics_port), "PERF_WORKER_REPLICAS": "1",
           "PERF_WORKER_TRANSPORT": args.transport, "PERF_K6_CONTAINER": "", "ENGINE_HEALTH_PATH": "readyz"}
    overlay = directory / "compose.yaml"
    overlay.write_text(f'''services:
  engine:
    build: !reset null
    image: {json.dumps(args.engine_image)}
  worker-go:
    build: !reset null
    image: {json.dumps(args.worker_image)}
    environment:
      E2E_HEARTBEAT: "1"
      E2E_HEARTBEAT_ENABLED: "{int(heartbeat)}"
''')
    compose = ["docker", "compose", "-f", str(ROOT / "perf/docker-compose.yml"), "-f", str(overlay)]
    log = (directory / "commands.log").open("w")
    sampler = None
    try:
        subprocess.run([*compose, "up", "-d", "--wait", "--wait-timeout", "90"], env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
        for service, key in (("engine", "engine"), ("worker-go", "worker")):
            container = subprocess.check_output([*compose, "ps", "-q", service], env=env, text=True).strip()
            actual = subprocess.check_output(["docker", "inspect", "--format", "{{.Image}}", container], text=True).strip()
            if actual != identity["images"][key]:
                raise ValueError(f"running {service} image differs from the pinned image")
        base = f"http://localhost:{rest_port}"
        definition = "heartbeat-cost"
        request(base, "/v1/definitions", {"id": definition, "name": "Heartbeat cost", "steps": [
            {"id": "work", "name": "Work", "type": "SERVICE_TASK", "jobType": args.sdk + "-sdk-heartbeat", "retryCount": 2, "nextStep": "end"},
            {"id": "end", "name": "End", "type": "END"}]})
        sampler = subprocess.Popen([sys.executable, str(ROOT / "perf/lib/host-stats.py"), str(directory), str(os.getpid()), "2"],
                                   env=env, stdout=log, stderr=subprocess.STDOUT)
        def execute(case):
            start = time.monotonic()
            instance = request(base, "/v1/instances", {"definitionId": definition, "businessKey": f"cost-{case}",
                               "variables": {"durationMs": args.handler_ms}})
            start_ms = (time.monotonic() - start) * 1000
            deadline = time.monotonic() + 45
            while time.monotonic() < deadline:
                instance = request(base, "/v1/instances/" + instance["id"])
                if instance["status"] in ("COMPLETED", "FAILED", "CANCELLED"):
                    variables = instance.get("variables") or {}
                    return {"id": instance["id"], "status": instance["status"], "handlerCalls": variables.get("handlerCalls"),
                            "remaining": variables.get("remaining"), "startMs": start_ms, "elapsedMs": (time.monotonic() - start) * 1000}
                time.sleep(.05)
            raise TimeoutError(f"workflow {instance['id']} did not finish")
        started = time.monotonic()
        with ThreadPoolExecutor(max_workers=2) as executor:
            workflows = list(executor.map(execute, range(args.cases)))
        elapsed = time.monotonic() - started
        subprocess.run([*compose, "stop", "-t", "5", "worker-go"], env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
        with urllib.request.urlopen(f"http://localhost:{metrics_port}/metrics", timeout=5) as response:
            snapshot = response.read().decode()
        (directory / "engine-metrics.txt").write_text(snapshot)
        sampler.terminate()
        if sampler.wait(timeout=20):
            raise RuntimeError("resource sampler failed")
        sampler = None
        resources = json.loads((directory / "host-stats.json").read_text())
        database = database_snapshot(compose, env, log)
        accepted = metric(snapshot, "engine_job_heartbeat_total", '{result="accepted"}', default=0)
        count = metric(snapshot, "engine_db_transaction_duration_seconds_count", '{tx_type="job.heartbeat"}', default=0)
        total = metric(snapshot, "engine_db_transaction_duration_seconds_sum", '{tx_type="job.heartbeat"}', default=0)
        result = {**identity, "heartbeat": heartbeat, "workflows": workflows, "database": database, "resources": resources,
                  "elapsedSeconds": elapsed, "workflowP95Ms": percentile([row["elapsedMs"] for row in workflows], 95),
                  "startP95Ms": percentile([row["startMs"] for row in workflows], 95), "workflowsPerSecond": args.cases / elapsed,
                  "acceptedHeartbeats": accepted, "heartbeatTxMeanMs": total * 1000 / count if count else None,
                  "timeouts": metric(snapshot, "engine_job_timeout_total")}
        (directory / "summary.json").write_text(json.dumps(result, indent=2) + "\n")
        return result
    finally:
        if sampler:
            sampler.terminate(); sampler.wait(timeout=20)
        with (directory / "compose.log").open("w") as output:
            logs = subprocess.run([*compose, "logs", "--no-color"], env=env, stdout=output, stderr=subprocess.STDOUT)
        cleanup = subprocess.run([*compose, "down", "-v", "--remove-orphans"], env=env, stdout=log, stderr=subprocess.STDOUT)
        log.close()
        if logs.returncode or cleanup.returncode:
            raise RuntimeError("compose log collection/cleanup failed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sdk", choices=["go", "node", "python", "java"], required=True)
    parser.add_argument("--transport", choices=["rest", "grpc"], required=True)
    parser.add_argument("--engine-image", required=True)
    parser.add_argument("--worker-image", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--cases", type=int, default=8)
    parser.add_argument("--handler-ms", type=int, default=12000)
    args = parser.parse_args()
    if args.cases < 4 or not 11000 <= args.handler_ms <= 20000:
        parser.error("need >=4 cases and 11000–20000ms handlers to observe renewal within a 30s lease")
    args.output = args.output.resolve()
    args.output.mkdir(parents=True, exist_ok=True)
    identity = {"schemaVersion": "heartbeat-cost-1", "config": {"sdk": args.sdk, "transport": args.transport, "dispatch": "polling",
        "cases": args.cases, "concurrency": 2, "handlerMs": args.handler_ms, "leaseSeconds": 30, "heartbeatIntervalMs": 10000},
        "harnessSha": subprocess.check_output([sys.executable, str(ROOT / "perf/lib/source-hash.py"), str(ROOT / "perf")], text=True).strip(),
        "images": {name: subprocess.check_output(["docker", "image", "inspect", "--format", "{{.Id}}", ref], text=True).strip()
                   for name, ref in (("engine", args.engine_image), ("worker", args.worker_image))},
        "host": {"os": os.uname().sysname + " " + os.uname().release, "cores": os.cpu_count(),
                 "docker": subprocess.check_output(["docker", "info", "--format", "{{.ServerVersion}} CPUs={{.NCPU}} memory={{.MemTotal}}"], text=True).strip()}}
    result = {"result": "fail"}
    try:
        off = measure(args, False, identity)
        on = measure(args, True, identity)
        result = {**identity, **validate_pair(off, on), "off": "off/summary.json", "on": "on/summary.json"}
    except Exception as error:
        result["error"] = str(error)
        raise
    finally:
        (args.output / "summary.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
