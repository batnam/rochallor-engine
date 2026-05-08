#!/usr/bin/env python3
import json, os, sys

reports_dir = sys.argv[1]
e = os.environ

base = {
    "schemaVersion": "1.0",
    "runId": e.get("PERF_RUN_ID", ""),
    "result": e.get("PERF_RESULT", ""),
    "startedAt": e.get("PERF_STARTED_AT", ""),
    "endedAt": e.get("PERF_ENDED_AT", ""),
    "scenario": {
        "name": e.get("PERF_SCENARIO_LABEL", ""),
        "gitSha": e.get("PERF_SCENARIO_SHA", ""),
        "config": {
            "targetVUs": int(e.get("PERF_TARGET_VUS", "0")),
            "rampUp": e.get("PERF_RAMP_UP", ""),
            "steady": e.get("PERF_STEADY", ""),
            "rampDown": e.get("PERF_RAMP_DOWN", ""),
            "definitionRef": e.get("PERF_DEFINITION_ID", ""),
        },
    },
    "engine": {
        "buildSha": e.get("PERF_ENGINE_SHA", ""),
        "transport": e.get("PERF_ENGINE_TRANSPORT", "rest"),
        "dispatchMode": e.get("PERF_DISPATCH_MODE", ""),
        "workerReplicas": 8,
    },
    "host": {
        "os": e.get("PERF_HOST_OS", "unknown"),
        "cpuCores": int(e.get("PERF_HOST_CPU_CORES", "0")),
        "memTotalMB": int(e.get("PERF_HOST_MEM_MB", "0")),
        "dockerVersion": e.get("PERF_HOST_DOCKER_VER", "unknown"),
        "k6Version": e.get("PERF_HOST_K6_VER", "unknown"),
    },
    "thresholdsLineage": "overridden" if e.get("PERF_THRESHOLD_OVERRIDES") else "default",
    "engineMetricsSnapshot": "engine-metrics-snapshot.txt"
        if os.path.exists(f"{reports_dir}/engine-metrics-snapshot.txt") else None,
    "notes": e.get("PERF_NOTE") or None,
}

try:
    with open(f"{reports_dir}/k6-summary-export.json") as f:
        se = json.load(f)
    metrics = se.get("metrics", {})
    breached = []
    for m_name, m_data in metrics.items():
        for rule, t_data in (m_data.get("thresholds") or {}).items():
            if not t_data.get("ok", True):
                vals = m_data.get("values", {})
                observed = (
                    vals.get("p(95)", vals.get("p95")) if "p(95)" in rule
                    else vals.get("rate") if "rate" in rule
                    else vals.get("count") if "count" in rule
                    else None
                )
                cat = (
                    "errorRate" if "rate" in rule
                    else "unfinished" if "unfinished" in m_name
                    else "latency"
                )
                breached.append({"threshold": m_name, "ruleString": rule,
                                  "observed": observed, "category": cat})
    base["breachedThresholds"] = breached
except Exception:
    base["breachedThresholds"] = []

try:
    patch = json.loads(e.get("PERF_REPORT_PATCH", "{}"))
    for key in ("phaseMetrics", "failures", "hostStatsSummary", "unfinishedWorkflows", "comparison"):
        if key in patch:
            base[key] = patch[key]
except Exception:
    pass

for key in ("phaseMetrics", "failures", "hostStatsSummary", "unfinishedWorkflows", "comparison"):
    base.setdefault(key, None)

with open(f"{reports_dir}/summary.json", "w") as f:
    json.dump(base, f, indent=2)
