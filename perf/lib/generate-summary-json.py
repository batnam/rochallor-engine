#!/usr/bin/env python3
"""Write run identity before validation; report.js owns the final verdict."""
import json
import os
from pathlib import Path
import sys

directory = Path(sys.argv[1])
env = os.environ
metadata = {
    "schemaVersion": "2.0", "runId": env["PERF_RUN_ID"], "result": env["PERF_RESULT"],
    "harnessSha": env["PERF_HARNESS_SHA"],
    "startedAt": env["PERF_STARTED_AT"], "endedAt": env["PERF_ENDED_AT"],
    "scenario": {"name": env["PERF_SCENARIO_LABEL"], "gitSha": env["PERF_SCENARIO_SHA"], "config": {
        "targetVUs": int(env["PERF_TARGET_VUS"]), "rampUp": env["PERF_RAMP_UP"],
        "steady": env["PERF_STEADY"], "rampDown": env["PERF_RAMP_DOWN"],
        "definitionRef": env["PERF_DEFINITION_ID"],
        "seniorCompleterVUs": int(env.get("PERF_SENIOR_COMPLETER_VUS", "1")) if env["PERF_SCENARIO_LABEL"].startswith("loan-chain-full") else 0,
    }},
    "engine": {"buildSha": None if env.get("PERF_ENGINE_IMAGE") else env["PERF_ENGINE_SHA"],
               "sourceHash": env["PERF_ENGINE_SOURCE_SHA"] or None, "imageRef": env.get("PERF_ENGINE_IMAGE") or None,
               "transport": env["PERF_ENGINE_TRANSPORT"], "dispatchMode": env["PERF_DISPATCH_MODE"],
               "workerReplicas": int(env["PERF_WORKER_REPLICAS"])},
    "host": {"os": env["PERF_HOST_OS"], "cpuCores": int(env["PERF_HOST_CPU_CORES"]),
             "memTotalMB": int(env["PERF_HOST_MEM_MB"]), "dockerVersion": env["PERF_HOST_DOCKER_VER"],
             "k6Version": env["PERF_HOST_K6_VER"]},
    "generator": {"containerized": env["PERF_CONTAINERIZED_K6"] == "true"},
    "thresholds": json.loads(env.get("PERF_THRESHOLD_OVERRIDES_JSON") or "{}"),
    "thresholdsLineage": "overridden" if env.get("PERF_THRESHOLD_OVERRIDES") else "default",
    "engineMetricsSnapshot": "engine-metrics-snapshot.txt",
    "containerImages": json.loads((directory / "container-images.json").read_text()),
    "notes": env.get("PERF_NOTE") or None,
}
(directory / "summary.json").write_text(json.dumps(metadata, indent=2) + "\n")
