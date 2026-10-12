#!/usr/bin/env python3
"""Two real engine versions share a disposable DB during a rolling upgrade."""
import argparse
from contextlib import ExitStack
import hashlib
import json
from pathlib import Path
import re
import sys
from urllib.error import HTTPError
from urllib.request import Request, urlopen
import uuid

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "workflow-engine/test"))
from support import EngineStack, build_engine


def call(stack, method, path, expected, body=None):
    request = Request(stack.base + path, method=method,
                      data=json.dumps(body).encode() if body is not None else None,
                      headers={"Content-Type": "application/json"})
    try:
        response = urlopen(request, timeout=5)
    except HTTPError as error:
        response = error
    with response:
        raw = response.read()
        if response.status != expected:
            raise AssertionError(f"{method} {path}: {response.status}, expected {expected}: {raw!r}")
        return json.loads(raw) if raw and expected != 404 else None


def verify(old, new):
    results = []
    for name, claimant, callback, renew in [
        ("old-claim-new-callback", old, new, False),
        ("new-claim-old-callback", new, old, False),
        ("mixed-heartbeat-capability", old, old, True),
    ]:
        identifier = "mixed-" + uuid.uuid4().hex
        call(old, "POST", "/v1/definitions", 201, {"id": identifier, "name": name, "steps": [
            {"id": "work", "name": "Work", "type": "SERVICE_TASK", "jobType": identifier,
             "retryCount": 2, "nextStep": "end"},
            {"id": "end", "name": "End", "type": "END"}]})
        instance = call(new, "POST", "/v1/instances", 201, {"definitionId": identifier})
        jobs = call(claimant, "POST", "/v1/jobs/poll", 200,
                    {"workerId": "mixed-worker", "jobTypes": [identifier], "maxJobs": 1})["jobs"]
        assert len(jobs) == 1
        job = jobs[0]
        assert re.fullmatch(r"[0-9A-Z]{26}", job["id"])
        path = "/v1/jobs/" + job["id"]
        if renew:
            body = {"workerId": "mixed-worker", "leaseSeconds": 60}
            accepted = call(new, "POST", path + "/heartbeat", 200, body)
            assert accepted["accepted"] and accepted["lockExpiresAt"] >= job["lockExpiresAt"]
            snapshot = old.sql(f"SELECT row_to_json(j)::text FROM job j WHERE id='{job['id']}'")
            call(old, "POST", path + "/heartbeat", 404, body)
            assert snapshot == old.sql(f"SELECT row_to_json(j)::text FROM job j WHERE id='{job['id']}'")
            assert call(new, "POST", path + "/heartbeat", 200, body)["accepted"]
        call(callback, "POST", path + "/complete", 204, {"workerId": "mixed-worker", "variables": {"mixed": True}})
        for replica in [old, new]:
            actual = call(replica, "GET", "/v1/instances/" + instance["id"], 200)
            assert actual["status"] == "COMPLETED" and actual["variables"]["mixed"] is True
        assert old.sql(f"SELECT status || ':' || retries_remaining FROM job WHERE id='{job['id']}'") == "COMPLETED:2"
        results.append({"case": name, "result": "pass", "instanceId": instance["id"], "jobId": job["id"]})
        print("PASS", name, flush=True)
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", required=True, type=Path)
    parser.add_argument("--engine", type=Path)
    parser.add_argument("--output", type=Path, default=ROOT / "testresults/mixed-engine")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    (args.output / "result.json").unlink(missing_ok=True)
    engine = args.engine or build_engine(args.output / "engine")
    with ExitStack() as cleanup:
        old = cleanup.enter_context(EngineStack(args.baseline, args.output / "old"))
        old.start()
        new = EngineStack(engine, args.output / "new")
        new.env["WE_POSTGRES_DSN"] = old.dsn
        cleanup.callback(new.__exit__, None, None, None)
        new.start()
        cases = verify(old, new)
    result = {"result": "pass", "transport": "rest", "cases": cases,
              "binaries": {"baseline": hashlib.sha256(args.baseline.read_bytes()).hexdigest(),
                           "candidate": hashlib.sha256(engine.read_bytes()).hexdigest()}}
    (args.output / "result.json").write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    main()
