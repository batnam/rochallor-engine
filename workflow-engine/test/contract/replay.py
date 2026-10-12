#!/usr/bin/env python3
"""Replay real REST requests and validate their responses against OpenAPI 3.1."""
import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import sys
import uuid
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from jsonschema import Draft202012Validator, FormatChecker
import yaml


class Replay:
    def __init__(self, spec, base):
        self.spec = yaml.safe_load(Path(spec).read_text())
        if self.spec.get("openapi") != "3.1.0":
            raise ValueError("replay requires OpenAPI 3.1.0")
        if "date-time" not in FormatChecker.checkers:
            raise RuntimeError("install test/contract/requirements.txt; date-time validation is required")
        self.base = base.rstrip("/")
        self.count = 0

    def resolve(self, node):
        while "$ref" in node:
            ref = node["$ref"]
            if not ref.startswith("#/"):
                raise ValueError(f"external reference is not supported: {ref}")
            node = self.spec
            for key in ref[2:].split("/"):
                node = node[key.replace("~1", "/").replace("~0", "~")]
        return node

    def validate(self, schema, value):
        # Retain the document root so local #/components/... references resolve.
        document = {"$schema": "https://json-schema.org/draft/2020-12/schema",
                    "components": self.spec["components"], **schema}
        Draft202012Validator.check_schema(document)
        Draft202012Validator(document, format_checker=FormatChecker()).validate(value)

    def call(self, method, template, expected, body=None, *, path=None,
             invalid=False, content_type="application/json"):
        operation = self.spec["paths"][template][method.lower()]
        if body is not None and not invalid:
            request_body = self.resolve(operation["requestBody"])
            self.validate(request_body["content"]["application/json"]["schema"], body)
        data = body.encode() if isinstance(body, str) else (
            json.dumps(body).encode() if body is not None else None)
        request = Request(self.base + (path or template), data=data, method=method,
                          headers={"Content-Type": content_type, "Accept": "application/json"})
        try:
            response = urlopen(request, timeout=5)
        except HTTPError as exc:
            response = exc
        with response:
            raw = response.read()
            if response.status != expected:
                raise AssertionError(f"{method} {template}: expected {expected}, "
                                     f"received {response.status}: {raw!r}")
            definition = self.resolve(operation["responses"][str(expected)])
            if "content" in definition:
                if response.headers.get_content_type() != "application/json":
                    raise AssertionError(f"{method} {template}: expected application/json")
                value = json.loads(raw)
                self.validate(definition["content"]["application/json"]["schema"], value)
            else:
                if raw:
                    raise AssertionError(f"{method} {template}: expected an empty body")
                value = None
        self.count += 1
        print(f"PASS {method} {template} → {expected}", flush=True)
        return value

    def run(self, mode):
        self.call("GET", "/healthz", 200)
        assert self.call("GET", "/readyz", 200) == {"status": "ready"}
        identifier = "contract-" + uuid.uuid4().hex
        worker = identifier + "-worker"
        job_type = identifier + "-job"
        steps = [{"id": "end", "name": "End", "type": "END"}]
        if mode == "polling":
            steps.insert(0, {"id": "work", "name": "Work", "type": "SERVICE_TASK",
                             "jobType": job_type, "retryCount": 2, "nextStep": "end"})
        definition = {"id": identifier, "name": "Contract replay", "steps": steps}
        uploaded = self.call("POST", "/v1/definitions", 201, definition)
        assert uploaded["id"] == identifier and uploaded["version"] == 1
        fetched = self.call("GET", "/v1/definitions/{id}", 200,
                            path=f"/v1/definitions/{identifier}")
        assert fetched["steps"] == steps
        self.call("GET", "/v1/definitions/{id}/versions/{version}", 200,
                  path=f"/v1/definitions/{identifier}/versions/1")
        self.call("GET", "/v1/definitions/{id}", 404,
                  path=f"/v1/definitions/missing-{identifier}")
        self.call("POST", "/v1/definitions", 400, {}, invalid=True)
        self.call("POST", "/v1/definitions", 415, "<xml/>", invalid=True,
                  content_type="application/xml")
        self.call("POST", "/v1/instances", 400, {}, invalid=True)
        self.call("GET", "/v1/instances/{id}", 404,
                  path=f"/v1/instances/missing-{identifier}")
        poll = {"workerId": worker, "jobTypes": [job_type], "maxJobs": 1}
        if mode == "kafka_outbox":
            self.call("POST", "/v1/jobs/poll", 410, poll)
            instance = self.call("POST", "/v1/instances", 201,
                                 {"definitionId": identifier, "variables": {"probe": True}})
            result = self.call("GET", "/v1/instances/{id}", 200,
                               path=f"/v1/instances/{instance['id']}")
            assert result["status"] == "COMPLETED"
        else:
            self.call("POST", "/v1/jobs/poll", 400, {"workerId": worker}, invalid=True)
            assert self.call("POST", "/v1/jobs/poll", 200, poll)["jobs"] in (None, [])
            for seconds in [-1, 4, 3601]:
                self.call("POST", "/v1/jobs/poll", 400, {**poll, "leaseSeconds": seconds}, invalid=True)
            for outcome, status in [("complete", "COMPLETED"), ("fail", "FAILED")]:
                instance = self.call("POST", "/v1/instances", 201,
                                     {"definitionId": identifier, "variables": {"probe": True}})
                assert instance["status"] == "ACTIVE"
                seconds = 60 if outcome == "complete" else 0
                before = datetime.now(timezone.utc)
                jobs = self.call("POST", "/v1/jobs/poll", 200, {**poll, "leaseSeconds": seconds})["jobs"]
                assert len(jobs) == 1 and jobs[0]["instanceId"] == instance["id"]
                job = jobs[0]
                expiry = datetime.fromisoformat(job["lockExpiresAt"].replace("Z", "+00:00"))
                assert abs((expiry - before).total_seconds() - (seconds or 30)) < 2, job
                assert job["variables"] == {"probe": True} and job["retriesRemaining"] == 2
                endpoint = "/v1/jobs/{id}/heartbeat"
                heartbeat_path = f"/v1/jobs/{job['id']}/heartbeat"
                accepted = self.call("POST", endpoint, 200, {"workerId": worker}, path=heartbeat_path)
                assert accepted["accepted"] is True
                assert datetime.fromisoformat(accepted["lockExpiresAt"].replace("Z", "+00:00")) >= expiry
                self.call("POST", endpoint, 409, {"workerId": "wrong-worker"}, path=heartbeat_path)
                self.call("POST", endpoint, 400, {"workerId": worker, "leaseSeconds": 4}, path=heartbeat_path, invalid=True)
                callback = {"workerId": worker}
                if outcome == "complete":
                    callback["variables"] = {"done": True}
                else:
                    callback.update(retryable=False, errorMessage="contract failure")
                self.call("POST", f"/v1/jobs/{{id}}/{outcome}", 204, callback,
                          path=f"/v1/jobs/{job['id']}/{outcome}")
                self.call("POST", endpoint, 409, {"workerId": worker}, path=heartbeat_path)
                result = self.call("GET", "/v1/instances/{id}", 200,
                                   path=f"/v1/instances/{instance['id']}")
                assert result["status"] == status, result
                if outcome == "complete":
                    assert result["variables"]["done"] is True
                else:
                    assert result["failureReason"]
        print(f"Contract replay: {self.count} requests passed ({mode})", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--openapi", default="api/openapi/rest-openapi.yaml")
    parser.add_argument("--base", default="http://localhost:8080")
    parser.add_argument("--mode", choices=["polling", "kafka_outbox"], default="polling")
    args = parser.parse_args()
    Replay(args.openapi, args.base).run(args.mode)


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"Contract replay FAILED: {type(exc).__name__}: {exc}", file=sys.stderr)
        sys.exit(1)
