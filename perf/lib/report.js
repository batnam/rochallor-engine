#!/usr/bin/env node
// Post-run report builder for the perf load test (Phase 4, T021).
// Reads k6-output.json + host-stats.json, produces the phaseMetrics array
// and failures object that are merged into summary.json.
//
// Usage: node perf/lib/report.js <reports-dir> [baseline-summary.json]
// Outputs two JSON objects to stdout separated by a NUL byte:
//   1. Partial summary.json patch (phaseMetrics, failures, hostStatsSummary,
//      unfinishedWorkflows, comparison if baseline provided)
//   2. (currently omitted — future: full summary rewrite)
//
// Contract: specs/003-performance-load-testing/contracts/run-report.md §1
// Data model: specs/003-performance-load-testing/data-model.md §PhaseMetric

'use strict';

const fs = require('fs');
const path = require('path');

const reportsDir = process.argv[2];
const baselinePath = process.argv[3] || null;

if (!reportsDir) {
  process.stderr.write('Usage: report.js <reports-dir> [baseline-summary.json]\n');
  process.exit(1);
}

// --- Load k6 output ---
const k6OutputPath = path.join(reportsDir, 'k6-output.json');
const summaryExportPath = path.join(reportsDir, 'k6-summary-export.json');
const hostStatsPath = path.join(reportsDir, 'host-stats.json');
const currentSummaryPath = path.join(reportsDir, 'summary.json');

// k6 --out json produces one JSON object per line (streaming JSON, not an array)
function loadK6Output(filePath) {
  if (!fs.existsSync(filePath)) return [];
  const lines = fs.readFileSync(filePath, 'utf8').trim().split('\n');
  const events = [];
  for (const line of lines) {
    try { events.push(JSON.parse(line)); } catch (_) { /* skip malformed */ }
  }
  return events;
}

function loadJson(filePath) {
  if (!fs.existsSync(filePath)) return null;
  try { return JSON.parse(fs.readFileSync(filePath, 'utf8')); } catch (_) { return null; }
}

const k6Events   = loadK6Output(k6OutputPath);
const summaryExp = loadJson(summaryExportPath);
const hostStats  = loadJson(hostStatsPath);
const currentSum = loadJson(currentSummaryPath);
const baseline   = baselinePath ? loadJson(baselinePath) : null;

// --- Phase × operation metric aggregation from k6 streaming output ---
// Each k6 Point event has: { metric, type, data: { tags, value, time } }
// We filter on type==="Point" and aggregate by (phase tag, operation tag).

const PHASES      = ['ramp-up', 'steady-state', 'ramp-down'];
const OPERATIONS  = [
  'workflow.start', 'workflow.poll', 'workflow.list', 'workflow.history',
  'workflow.cancel', 'workflow.signal', 'workflow.task_complete',
];

// Bucket structure: { phase: { operation: { count, sum, errors, values[] } } }
const buckets = {};
for (const ph of PHASES) {
  buckets[ph] = {};
  for (const op of OPERATIONS) {
    buckets[ph][op] = { count: 0, sumMs: 0, errorCount: 0, values: [] };
  }
}

for (const ev of k6Events) {
  if (ev.type !== 'Point') continue;
  const tags = (ev.data && ev.data.tags) || {};
  const phase = tags.phase;
  const operation = tags.operation;
  const metric = ev.metric;
  const value = ev.data && ev.data.value;

  if (!phase || !operation) continue;
  if (!PHASES.includes(phase) || !OPERATIONS.includes(operation)) continue;

  const b = buckets[phase][operation];

  if (metric === 'http_req_duration') {
    b.count++;
    b.sumMs += value;
    b.values.push(value);
  } else if (metric === 'http_req_failed') {
    if (value > 0) b.errorCount++;
  }
}

function percentile(sortedArr, p) {
  if (!sortedArr.length) return 0;
  const idx = Math.ceil((p / 100) * sortedArr.length) - 1;
  return sortedArr[Math.max(0, idx)];
}

const phaseMetrics = [];
for (const ph of PHASES) {
  for (const op of OPERATIONS) {
    const b = buckets[ph][op];
    b.values.sort((a, z) => a - z);
    const rate = b.count;  // raw count; run.sh knows the duration to compute req/s if needed
    const errorRate = b.count > 0 ? b.errorCount / b.count : 0;
    phaseMetrics.push({
      phase: ph,
      operation: op,
      count: b.count,
      rate,
      errorRate,
      p50Ms: percentile(b.values, 50),
      p95Ms: percentile(b.values, 95),
      p99Ms: percentile(b.values, 99),
    });
  }
}

// --- Failure kind counts from summary export ---
const failures = {
  engine_error: 0,
  engine_timeout: 0,
  engine_unresponsive: 0,
  generator_side: 0,
  surface_drift: 0,
  setup_failed: 0,
};

if (summaryExp && summaryExp.metrics) {
  const m = summaryExp.metrics;
  for (const kind of Object.keys(failures)) {
    const key = `failure_kind_${kind}`;
    if (m[key] && m[key].values) {
      failures[kind] = m[key].values.count || 0;
    }
  }
}

// --- Unfinished workflow count (from the custom metric in summary export) ---
const unfinishedWorkflows = { count: 0, sampleIds: [] };
if (summaryExp && summaryExp.metrics) {
  const uf = summaryExp.metrics['workflows_unfinished_after_steady_state'];
  if (uf && uf.values) {
    unfinishedWorkflows.count = uf.values.count || 0;
  }
}

// --- Host stats summary ---
const hostStatsSummary = hostStats ? hostStats.summary : null;

// --- Regression comparison (Phase 5 / T029-T031, placeholder here) ---
let comparison = null;
if (baseline && baseline.phaseMetrics) {
  const BAND = { p95Pct: 15, throughputPct: 10 };
  const perOperation = [];
  let flaggedCount = 0;
  let hasWorse = false;
  let hasBetter = false;

  for (const cur of phaseMetrics) {
    if (cur.phase !== 'steady-state') continue;  // compare steady-state only
    const base = baseline.phaseMetrics
      ? baseline.phaseMetrics.find(
          (b) => b.phase === cur.phase && b.operation === cur.operation,
        )
      : null;
    if (!base || base.p95Ms === 0) continue;

    for (const { metric, baseVal, curVal, band, direction } of [
      { metric: 'p95',       baseVal: base.p95Ms,     curVal: cur.p95Ms,     band: BAND.p95Pct,       direction: 'up-is-worse'   },
      { metric: 'errorRate', baseVal: base.errorRate,  curVal: cur.errorRate,  band: null,              direction: 'up-is-worse'   },
      { metric: 'throughput',baseVal: base.count,      curVal: cur.count,      band: BAND.throughputPct,direction: 'down-is-worse' },
    ]) {
      if (baseVal === 0) continue;
      const deltaPct = baseVal !== 0 ? ((curVal - baseVal) / baseVal) * 100 : 0;
      let flagged = false;
      if (band !== null) {
        flagged = Math.abs(deltaPct) > band;
      } else {
        // errorRate: flag if absolute increase > 0.05%
        flagged = (curVal - baseVal) > 0.0005;
      }
      if (flagged) {
        flaggedCount++;
        const worse = (direction === 'up-is-worse' && deltaPct > 0) ||
                      (direction === 'down-is-worse' && deltaPct < 0);
        if (worse) hasWorse = true; else hasBetter = true;
      }
      perOperation.push({
        phase: cur.phase,
        operation: cur.operation,
        metric,
        baseline: baseVal,
        current: curVal,
        deltaPct: Math.round(deltaPct * 10) / 10,
        flagged,
      });
    }
  }

  comparison = {
    baselineRunId: baseline.runId,
    varianceBand: BAND,
    perOperation,
    flaggedCount,
    verdict: hasWorse ? 'regressed' : hasBetter ? 'improved' : 'within-noise',
  };
}

// --- Output the patch object ---
const patch = {
  phaseMetrics,
  failures,
  unfinishedWorkflows,
  hostStatsSummary,
  comparison,
};

process.stdout.write(JSON.stringify(patch, null, 2));
process.stdout.write('\n');
