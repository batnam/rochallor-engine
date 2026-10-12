#!/usr/bin/env node
'use strict';
// Validate all evidence before producing a verdict. Exit 1 on invalid evidence
// or a failed gate, even when k6 itself returned success.
const fs = require('node:fs');
const path = require('node:path');
const readline = require('node:readline');

const PHASES = ['ramp-up', 'steady-state', 'ramp-down'];
const OPERATIONS = ['workflow.start', 'workflow.poll', 'workflow.list', 'workflow.history',
  'workflow.cancel', 'workflow.signal', 'workflow.task_complete'];
function requireThat(condition, message) { if (!condition) throw new Error(message); }
function number(value, name) {
  requireThat(typeof value === 'number' && Number.isFinite(value) && value >= 0, `invalid ${name}`);
  return value;
}
function seconds(value) {
  const match = /^(\d+(?:\.\d+)?)(ms|s|m|h)$/.exec(value);
  requireThat(match, `invalid phase duration: ${value}`);
  const result = Number(match[1]) * { ms: 0.001, s: 1, m: 60, h: 3600 }[match[2]];
  requireThat(result > 0, 'phase duration must be positive');
  return result;
}
function percentile(values, percent) {
  return values.length ? values[Math.ceil(values.length * percent / 100) - 1] : null;
}
function compatible(base, current) {
  requireThat(base.schemaVersion === '2.0' && base.result === 'pass', 'baseline must be a passing v2 report');
  for (const key of ['harnessSha', 'thresholds', 'scenario', 'host', 'generator']) {
    const comparable = key === 'scenario' ? { name: current.scenario.name, config: current.scenario.config } : current[key];
    const prior = key === 'scenario' ? { name: base.scenario?.name, config: base.scenario?.config } : base[key];
    requireThat(comparable !== undefined && JSON.stringify(prior) === JSON.stringify(comparable), `baseline differs: ${key}`);
  }
  for (const key of ['transport', 'dispatchMode', 'workerReplicas'])
    requireThat(base.engine?.[key] === current.engine[key], `baseline differs: engine.${key}`);
}

async function main() {
  const [directory, baselinePath] = process.argv.slice(2);
  requireThat(directory, 'usage: report.js <reports-dir> [baseline-summary.json]');
  const load = name => JSON.parse(fs.readFileSync(path.join(directory, name), 'utf8'));
  const current = load('summary.json');
  const summary = load('k6-summary-export.json');
  const host = load('host-stats.json');
  const outcomes = load('workflow-outcomes.json');
  requireThat(current.schemaVersion === '2.0' && current.harnessSha, 'missing current harness identity');
  requireThat(['rest', 'grpc'].includes(current.engine?.transport), 'invalid transport');
  requireThat(['polling', 'kafka_outbox'].includes(current.engine.dispatchMode), 'invalid dispatch mode');
  requireThat(summary.metrics && Object.keys(summary.metrics).length, 'missing k6 metrics');
  const durations = { 'ramp-up': seconds(current.scenario.config.rampUp),
    'steady-state': seconds(current.scenario.config.steady), 'ramp-down': seconds(current.scenario.config.rampDown) };
  const transport = current.engine.transport;
  const latencyMetric = transport === 'rest' ? 'http_req_duration' : 'grpc_req_duration';
  const errorMetric = transport === 'rest' ? 'http_req_failed' : 'grpc_error_rate';
  const buckets = new Map();
  for (const phase of PHASES) for (const operation of OPERATIONS)
    buckets.set(`${phase}/${operation}`, { phase, operation, values: [], errorSamples: 0, errors: 0 });
  let lines = 0;
  const input = readline.createInterface({ input: fs.createReadStream(path.join(directory, 'k6-output.json')), crlfDelay: Infinity });
  for await (const line of input) {
    if (!line.trim()) continue;
    lines++;
    const event = JSON.parse(line); // Malformed lines invalidate the report.
    if (event.type !== 'Point' || ![latencyMetric, errorMetric].includes(event.metric)) continue;
    const bucket = buckets.get(`${event.data?.tags?.phase}/${event.data?.tags?.operation}`);
    if (!bucket) continue; // Setup/teardown calls are outside the load phases.
    if (transport === 'rest') {
      const tags = event.data.tags;
      requireThat(tags.name === tags.operation && tags.url === tags.operation,
        `ungrouped REST request metrics: ${tags.operation}`);
    }
    const value = number(event.data.value, `${event.metric} sample`);
    if (event.metric === latencyMetric) bucket.values.push(value);
    else {
      requireThat(value === 0 || value === 1, 'error sample must be 0 or 1');
      bucket.errorSamples++; bucket.errors += value;
    }
  }
  requireThat(lines > 0, 'empty k6 output');
  const requiredOps = ['workflow.start', 'workflow.poll', 'workflow.list', 'workflow.history'];
  if (current.scenario.name.startsWith('loan-chain-full')) {
    requiredOps.splice(requiredOps.indexOf('workflow.history'), 1);
    requiredOps.push('workflow.task_complete');
  }
  for (const operation of requiredOps)
    requireThat(buckets.get(`steady-state/${operation}`).values.length > 0, `missing steady-state metric: ${operation}`);
  const phaseMetrics = Array.from(buckets.values()).map(b => {
    requireThat(b.errorSamples === b.values.length, `missing error/latency samples: ${b.phase}/${b.operation}`);
    b.values.sort((a, z) => a - z);
    return { phase: b.phase, operation: b.operation, count: b.values.length,
      rate: b.values.length / durations[b.phase], errorRate: b.values.length ? b.errors / b.values.length : null,
      p50Ms: percentile(b.values, 50), p95Ms: percentile(b.values, 95), p99Ms: percentile(b.values, 99) };
  }).filter(row => row.count > 0);

  const gateFailures = [];
  const breachedThresholds = [];
  for (const [name, metric] of Object.entries(summary.metrics)) {
    for (const [rule, result] of Object.entries(metric.thresholds || {})) {
      const failed = typeof result === 'boolean' ? result : result?.ok === false;
      requireThat(typeof result === 'boolean' || typeof result?.ok === 'boolean', `invalid threshold: ${name}`);
      if (failed) breachedThresholds.push({ threshold: name, ruleString: rule, observed: metric.values || metric });
    }
  }
  if (breachedThresholds.length) gateFailures.push('k6 thresholds breached');
  const checks = summary.metrics.checks?.values || summary.metrics.checks;
  requireThat(checks, 'missing k6 checks');
  if (number(checks.fails, 'failed checks') > 0) gateFailures.push('k6 checks failed');
  number(checks.passes, 'passed checks');
  const failures = {};
  for (const kind of ['engine_error', 'engine_timeout', 'engine_unresponsive', 'generator_side']) {
    const metric = summary.metrics[`failure_kind_${kind}`];
    requireThat(metric, `missing failure metric: ${kind}`);
    failures[kind] = number((metric.values || metric).count, kind);
    if (failures[kind] > 0) gateFailures.push(`${kind}: ${failures[kind]}`);
  }
  const unfinishedNames = current.scenario.name.startsWith('loan-chain-full')
    ? ['chain_app_unfinished_after_steady_state', 'chain_disb_unfinished_after_steady_state']
    : ['workflows_unfinished_after_steady_state'];
  let unfinished = 0;
  for (const name of unfinishedNames) {
    const metric = summary.metrics[name];
    requireThat(metric, `missing outcome metric: ${name}`);
    unfinished += number((metric.values || metric).count, name);
  }
  if (unfinished) gateFailures.push(`unfinished workflows: ${unfinished}`);

  requireThat(Array.isArray(outcomes.instances) && outcomes.instances.length > 0, 'missing workflow outcomes');
  const statusCounts = {};
  const ids = new Set();
  for (const item of outcomes.instances) {
    requireThat(typeof item.id === 'string' && item.id && !ids.has(item.id), 'invalid/duplicate outcome id');
    requireThat(['COMPLETED', 'FAILED', 'CANCELLED', 'ACTIVE', 'WAITING'].includes(item.status), 'invalid outcome status');
    ids.add(item.id); statusCounts[item.status] = (statusCounts[item.status] || 0) + 1;
  }
  if (statusCounts.COMPLETED !== outcomes.instances.length) gateFailures.push('unexpected workflow outcomes (all must be COMPLETED)');
  if (number(outcomes.pendingChains, 'pending chains') > 0) gateFailures.push('pending chained workflows');
  requireThat(host.summary && Array.isArray(host.samples) && host.samples.length > 0, 'missing host samples');
  requireThat(host.summary.sampleCount === host.samples.length, 'host sample count mismatch');
  number(host.summary.k6CpuPctPeak, 'generator CPU');
  requireThat(typeof host.summary.generatorLikelySaturated === 'boolean', 'missing generator saturation assessment');
  requireThat(!host.errors?.length, `host sampling failed: ${host.errors}`);
  if (host.summary.generatorLikelySaturated) gateFailures.push('load generator saturated');

  let comparison = null;
  if (baselinePath) {
    const baseline = JSON.parse(fs.readFileSync(baselinePath, 'utf8'));
    compatible(baseline, current);
    requireThat(Array.isArray(baseline.phaseMetrics), 'baseline missing phase metrics');
    const perOperation = [];
    let worse = false; let better = false;
    for (const row of phaseMetrics.filter(p => p.phase === 'steady-state')) {
      const base = baseline.phaseMetrics.find(p => p.phase === row.phase && p.operation === row.operation);
      requireThat(base && base.count > 0 && base.p95Ms > 0 && base.rate > 0, `baseline missing ${row.operation}`);
      for (const [metric, key, band, direction] of [['p95', 'p95Ms', 15, 1], ['throughput', 'rate', 10, -1], ['errorRate', 'errorRate', null, 1]]) {
        number(base[key], `baseline ${key}`);
        const delta = row[key] - base[key];
        const deltaPct = base[key] === 0 ? null : 100 * delta / base[key];
        const flagged = band === null ? Math.abs(delta) > 0.0005 : Math.abs(deltaPct) > band;
        if (flagged) { if (delta * direction > 0) worse = true; else better = true; }
        perOperation.push({ phase: row.phase, operation: row.operation, metric,
          baseline: base[key], current: row[key], deltaPct, flagged });
      }
    }
    comparison = { baselineRunId: baseline.runId, varianceBand: { p95Pct: 15, throughputPct: 10 },
      perOperation, flaggedCount: perOperation.filter(p => p.flagged).length,
      verdict: worse ? 'regressed' : better ? 'improved' : 'within-noise' };
    if (worse) gateFailures.push('regression exceeds variance band');
  }
  if (current.result !== 'pass') gateFailures.push('k6 execution failed');
  const patch = { result: gateFailures.length ? 'fail' : 'pass', phaseMetrics, failures,
    unfinishedWorkflows: { count: unfinished, sampleIds: [] }, hostStatsSummary: host.summary,
    workflowOutcomes: { total: ids.size, statusCounts, pendingChains: outcomes.pendingChains },
    comparison, breachedThresholds, gateFailures };
  process.stdout.write(JSON.stringify(patch, null, 2) + '\n');
  if (gateFailures.length) process.exitCode = 1;
}

main().catch(error => {
  process.stderr.write(`Report failed: ${error.message}\n`);
  process.exitCode = 1;
});
