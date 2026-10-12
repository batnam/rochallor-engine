#!/usr/bin/env python3
"""Render the validated report without turning absent evidence into zeroes."""
import json
from pathlib import Path
import sys

directory = Path(sys.argv[1])
data = json.loads((directory / "summary.json").read_text())
scenario, engine, host = data["scenario"], data["engine"], data["host"]
config = scenario["config"]
lines = [f"# Run {data['runId']} — {data['result']}", "",
         f"Scenario: {scenario['name']}; {engine['dispatchMode']} / {engine['transport']}.",
         f"VUs: {config['targetVUs']}; phases: {config['rampUp']} / {config['steady']} / {config['rampDown']}; workers: {engine['workerReplicas']}.",
         f"Harness SHA-256: `{data['harnessSha']}`.",
         f"Engine source SHA-256: `{engine.get('sourceHash', 'not recorded')}`; commit: `{engine.get('buildSha', 'not recorded')}`.",
         "The image IDs actually run are recorded in container-images.json and summary.json.", "",
         "## Gate verdict", ""]
if data.get("gateFailures"):
    lines += [f"- {reason}" for reason in data["gateFailures"]]
elif data["result"] == "pass":
    lines += ["Threshold, evidence, generator and business-outcome checks passed."]
else:
    lines += ["Run failed before all evidence could be verified; see the command log."]
for threshold in data.get("breachedThresholds", []):
    lines += [f"- Threshold: `{threshold['threshold']}` / `{threshold['ruleString']}`"]
lines += ["", "## Latency and throughput", "",
          "| Phase | Operation | Count | Requests/s | Error rate | p50 ms | p95 ms | p99 ms |",
          "|---|---|---:|---:|---:|---:|---:|---:|"]
for row in data.get("phaseMetrics", []):
    lines += [f"| {row['phase']} | {row['operation']} | {row['count']} | {row['rate']:.2f} | {row['errorRate']:.4f} | {row['p50Ms']:.2f} | {row['p95Ms']:.2f} | {row['p99Ms']:.2f} |"]
lines += ["", "## Workflow outcomes", ""]
outcomes = data.get("workflowOutcomes")
if outcomes:
    lines += [f"Verified {outcomes['total']} database rows: `{json.dumps(outcomes['statusCounts'], sort_keys=True)}`; pending chains: {outcomes['pendingChains']}.",
              "Both supported workloads require every workflow, including canary and chained children, to be COMPLETED."]
else:
    lines += ["Unavailable; this report cannot certify business outcomes."]
lines += ["", "## Generator and host", "",
          f"{host['os']}; {host['cpuCores']} CPU cores; {host['memTotalMB']} MiB RAM; Docker {host['dockerVersion']}; {host['k6Version']}."]
stats = data.get("hostStatsSummary")
if stats:
    lines += [f"Samples: {stats['sampleCount']}; k6 CPU peak: {stats['k6CpuPctPeak']}% (one core = 100%); CPU capacity peak: {stats.get('k6CapacityPctPeak', 'not recorded')}%.",
              f"Generator likely saturated: {stats['generatorLikelySaturated']}. At 90% of available CPU capacity the load gate fails."]
else:
    lines += ["Resource evidence unavailable."]
comparison = data.get("comparison")
if comparison:
    lines += ["", "## Baseline comparison", "", f"Baseline `{comparison['baselineRunId']}`: **{comparison['verdict']}**.",
              "Bands: p95 ±15%, throughput ±10%, error rate absolute ±0.0005; any worse metric beyond its band fails the gate."]
    for row in comparison["perOperation"]:
        if row["flagged"]:
            lines += [f"- {row['operation']} / {row['metric']}: {row['baseline']} → {row['current']}"]
if data.get("notes"):
    lines += ["", "## Notes", "", data["notes"]]
lines += ["", "Raw evidence: k6-output.json, k6-summary-export.json, workflow-outcomes.json, host-stats.json, engine-metrics-snapshot.txt and compose.log.", ""]
(directory / "summary.md").write_text("\n".join(lines))
