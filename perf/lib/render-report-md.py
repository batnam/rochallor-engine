#!/usr/bin/env python3
import json, os, sys

reports_dir = sys.argv[1]
e = os.environ

with open(f"{reports_dir}/summary.json") as f:
    d = json.load(f)

hs = d.get("hostStatsSummary") or {}
sat_generator = hs.get("generatorLikelySaturated", False)
sat_engine = hs.get("engineLikelySaturated", False)

run_id        = e.get("PERF_RUN_ID", "")
result        = e.get("PERF_RESULT", "")
scenario_label = e.get("PERF_SCENARIO_LABEL", "")
scenario_sha  = e.get("PERF_SCENARIO_SHA", "unknown")
engine_sha    = e.get("PERF_ENGINE_SHA", "")
target_vus    = e.get("PERF_TARGET_VUS", "")
ramp_up       = e.get("PERF_RAMP_UP", "")
steady        = e.get("PERF_STEADY", "")
ramp_down     = e.get("PERF_RAMP_DOWN", "")
started_at    = e.get("PERF_STARTED_AT", "")
ended_at      = e.get("PERF_ENDED_AT", "")
dispatch_mode = e.get("PERF_DISPATCH_MODE", "")
threshold_overrides = e.get("PERF_THRESHOLD_OVERRIDES", "")
note          = e.get("PERF_NOTE", "")

result_icon = "✅" if result == "pass" else "❌"
lines = []

lines.append(f"# Run {run_id} — {result} {result_icon}\n")

lines.append("## TL;DR\n")
if sat_generator:
    lines.append("> ⚠️  **Generator likely saturated** (k6 process CPU peak > 90%).")
    lines.append("> This run may not be authoritative — the laptop, not the engine, was the bottleneck.")
    lines.append("> Re-run on a larger machine or reduce --target-vus.\n")
lines.append(f"- **Scenario**: {scenario_label} | **Engine build**: `{engine_sha}`")
lines.append(f"- **Target VUs**: {target_vus} | ramp-up {ramp_up} / steady {steady} / ramp-down {ramp_down}")
lines.append("- **Result**: PASS — all thresholds within bounds" if result == "pass"
             else "- **Result**: FAIL — see Breached Thresholds below")
lines.append(f"- **Started**: {started_at} | **Ended**: {ended_at}\n")

lines.append("## Scenario & engine\n")
lines.append("| key | value |")
lines.append("|---|---|")
lines.append(f"| Scenario | {scenario_label} (`{scenario_sha[:7]}`) |")
lines.append(f"| Engine build | `{engine_sha}` |")
lines.append(f"| Dispatch mode | {dispatch_mode} |")
lines.append("| Worker replicas | 8 |")
lines.append(f"| Phases | ramp-up {ramp_up} / steady {steady} / ramp-down {ramp_down} |\n")

lineage = "overridden" if threshold_overrides else "default"
lines.append(f"## Thresholds (lineage: {lineage})\n")
try:
    bt = d.get("breachedThresholds") or []
    breached_names = {b["threshold"] for b in bt}
    try:
        with open(f"{reports_dir}/k6-summary-export.json") as f:
            se = json.load(f)
        metrics = se.get("metrics", {})
    except Exception:
        metrics = {}
    all_thresholds = {
        "workflow.start error rate < 0.1%":         "http_req_failed{operation:workflow.start}",
        "workflow.poll error rate < 0.1%":          "http_req_failed{operation:workflow.poll}",
        "workflow.list error rate < 0.1%":          "http_req_failed{operation:workflow.list}",
        "workflow.history error rate < 0.1%":       "http_req_failed{operation:workflow.history}",
        "workflow.cancel error rate < 0.1%":        "http_req_failed{operation:workflow.cancel}",
        "workflow.signal error rate < 0.1%":        "http_req_failed{operation:workflow.signal}",
        "workflow.task_complete error rate < 0.1%": "http_req_failed{operation:workflow.task_complete}",
        "workflow.start p95 < 1000 ms":             "http_req_duration{operation:workflow.start}",
        "workflow.poll p95 < 500 ms":               "http_req_duration{operation:workflow.poll}",
        "workflow.list p95 < 500 ms":               "http_req_duration{operation:workflow.list}",
        "workflow.history p95 < 500 ms":            "http_req_duration{operation:workflow.history}",
        "workflow.cancel p95 < 500 ms":             "http_req_duration{operation:workflow.cancel}",
        "workflow.signal p95 < 500 ms":             "http_req_duration{operation:workflow.signal}",
        "workflow.task_complete p95 < 500 ms":      "http_req_duration{operation:workflow.task_complete}",
        "zero unfinished workflows (10s)":           "workflows_unfinished_after_steady_state",
        "at least one boundary event fired":         "boundary_events_fired",
    }
    thresholds = {desc: m for desc, m in all_thresholds.items() if not metrics or m in metrics}
    lines.append("| Threshold | Result |")
    lines.append("|---|---|")
    for desc, metric in thresholds.items():
        icon = "❌ FAIL" if metric in breached_names else "✅ pass"
        lines.append(f"| {desc} | {icon} |")
except Exception as ex:
    lines.append(f"_(threshold table unavailable: {ex})_")
lines.append("")

lines.append("## Per-phase / per-operation metrics\n")
try:
    pm = d.get("phaseMetrics") or []
    ops = ["workflow.start", "workflow.poll", "workflow.list", "workflow.history",
           "workflow.cancel", "workflow.signal", "workflow.task_complete"]
    phases = ["ramp-up", "steady-state", "ramp-down"]
    lines.append("| Phase | Operation | Count | ErrorRate | p50ms | p95ms | p99ms |")
    lines.append("|---|---|---|---|---|---|---|")
    for ph in phases:
        for op in ops:
            row = next((r for r in pm if r["phase"] == ph and r["operation"] == op), None)
            if row:
                lines.append(f"| {ph} | {op} | {row['count']} | {row['errorRate']:.4f}"
                             f" | {row['p50Ms']:.0f} | {row['p95Ms']:.0f} | {row['p99Ms']:.0f} |")
            else:
                lines.append(f"| {ph} | {op} | — | — | — | — | — |")
except Exception as ex:
    lines.append(f"_(metrics table unavailable: {ex})_")
lines.append("")

lines.append("## Failures by category\n")
try:
    f_data = d.get("failures") or {}
    lines.append("| Category | Count |")
    lines.append("|---|---|")
    for k in ("engine_error", "engine_timeout", "engine_unresponsive",
              "generator_side", "surface_drift", "setup_failed"):
        lines.append(f"| {k} | {f_data.get(k, 0)} |")
except Exception as ex:
    lines.append(f"_(failures table unavailable: {ex})_")
lines.append("")

lines.append("## Unfinished workflows\n")
try:
    uf = d.get("unfinishedWorkflows") or {}
    cnt = uf.get("count", 0)
    ids = uf.get("sampleIds") or []
    if cnt == 0:
        lines.append("**0** — all workflows reached a terminal state within 10 s of steady-state end.")
    else:
        lines.append(f"**{cnt}** workflow(s) still non-terminal 10 s after steady-state ended.")
        if ids:
            lines.append("\nSample instance IDs:")
            for iid in ids[:5]:
                lines.append(f"- `{iid}`")
except Exception as ex:
    lines.append(f"_(unfinished count unavailable: {ex})_")
lines.append("")

lines.append("## Host & generator\n")
try:
    h = d.get("host") or {}
    lines.append(f"- OS: {h.get('os','n/a')} | CPU cores: {h.get('cpuCores','n/a')} | RAM: {h.get('memTotalMB','n/a')} MB")
    lines.append(f"- k6 peak CPU: {hs.get('k6CpuPctPeak','n/a')}% | engine peak CPU: {hs.get('engineCpuPctPeak','n/a')}%")
    lines.append(f"- postgres peak CPU: {hs.get('postgresCpuPctPeak','n/a')}% | workers peak CPU: {hs.get('workersCpuPctPeak','n/a')}%")
    if sat_generator:
        lines.append("\n> ⚠️ **Generator likely saturated** — k6 process CPU peaked above 90%. Treat this run with caution.")
    if sat_engine:
        lines.append("\n> ⚠️ **Engine likely saturated** — engine container CPU peaked above 90%.")
except Exception as ex:
    lines.append(f"_(host stats unavailable: {ex})_")
lines.append("")

try:
    cmp = d.get("comparison")
    if cmp:
        verd = cmp.get("verdict", "?")
        icon = {"regressed": "⚠️ REGRESSED", "improved": "✅ improved",
                "within-noise": "✅ within-noise"}.get(verd, verd)
        lines.append("## Comparison\n")
        lines.append(f"**Baseline**: `{cmp.get('baselineRunId','?')}` | **Verdict**: {icon}\n")
        band = cmp.get("varianceBand", {})
        lines.append(f"Variance band: p95 ±{band.get('p95Pct',15)}% / throughput ±{band.get('throughputPct',10)}%\n")
        rows = cmp.get("perOperation") or []
        if rows:
            lines.append("| Operation | Metric | Baseline | Current | Delta% | Flagged |")
            lines.append("|---|---|---|---|---|---|")
            for r in rows:
                flag = "⚠️" if r.get("flagged") else ""
                lines.append(f"| {r.get('operation')} | {r.get('metric')} | {r.get('baseline')}"
                             f" | {r.get('current')} | {r.get('deltaPct')}% | {flag} |")
        lines.append("")
except Exception:
    pass

if note:
    lines.append(f"## Engineer notes\n\n{note}\n")

lines.append("## Files in this run dir\n")
lines.append("- summary.md (this file)")
lines.append("- summary.json")
for fname in ("k6-output.json", "k6-summary-export.json", "host-stats.json", "engine-metrics-snapshot.txt"):
    if os.path.exists(f"{reports_dir}/{fname}"):
        lines.append(f"- {fname}")
lines.append("")

with open(f"{reports_dir}/summary.md", "w") as f:
    f.write("\n".join(lines))

if sat_generator:
    print("[perf] WARNING: generator likely saturated — this result may not be authoritative", file=sys.stderr)
