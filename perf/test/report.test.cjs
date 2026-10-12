'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const report = path.resolve(__dirname, '../lib/report.js');
const finish = path.resolve(__dirname, '../lib/finish-report.sh');
const operations = ['workflow.start', 'workflow.poll', 'workflow.history', 'workflow.list'];
const phases = ['ramp-up', 'steady-state', 'ramp-down'];
function write(dir, file, data) { fs.writeFileSync(path.join(dir, file), JSON.stringify(data)); }
function read(dir, file) { return JSON.parse(fs.readFileSync(path.join(dir, file), 'utf8')); }
function fixture(t, transport = 'rest', latency = 10) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'perf-report-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const metric = transport === 'rest' ? 'http_req_duration' : 'grpc_req_duration';
  const error = transport === 'rest' ? 'http_req_failed' : 'grpc_error_rate';
  const points = [];
  for (const phase of phases) for (const operation of operations) for (let n = 1; n <= 10; n++) {
    for (const [name, value] of [[metric, latency * n], [error, 0]])
      points.push({ type: 'Point', metric: name, data: { value, tags: { phase, operation, name: operation, url: operation } } });
  }
  fs.writeFileSync(path.join(dir, 'k6-output.json'), points.map(p => JSON.stringify(p)).join('\n') + '\n');
  const metrics = { checks: { passes: 100, fails: 0, thresholds: { 'rate==1': false } },
    workflows_unfinished_after_steady_state: { count: 0 } };
  for (const kind of ['engine_error', 'engine_timeout', 'engine_unresponsive', 'generator_side'])
    metrics[`failure_kind_${kind}`] = { count: 0 };
  write(dir, 'k6-summary-export.json', { metrics });
  write(dir, 'host-stats.json', { samples: [{ tSinceStartSec: 1 }], summary: {
    sampleCount: 1, k6CpuPctPeak: 20, generatorLikelySaturated: false } });
  write(dir, 'workflow-outcomes.json', { instances: [{ id: 'one', status: 'COMPLETED' }], pendingChains: 0 });
  write(dir, 'summary.json', { schemaVersion: '2.0', runId: 'fixture', result: 'pass',
    harnessSha: 'fixture-harness', thresholds: {}, scenario: { name: 'thousand-concurrent',
      config: { targetVUs: 10, rampUp: '5s', steady: '5s', rampDown: '5s', definitionRef: 'service-chain' } },
    engine: { transport, dispatchMode: 'polling', workerReplicas: 10 },
    host: { os: 'fixture', cpuCores: 8, memTotalMB: 16000, dockerVersion: 'fixture', k6Version: 'fixture' },
    generator: { containerized: false }, breachedThresholds: [] });
  return dir;
}
function run(dir, baseline) {
  const args = [report, dir];
  if (baseline) args.push(baseline);
  const p = spawnSync(process.execPath, args, { encoding: 'utf8' });
  return { ...p, patch: p.stdout.trim() ? JSON.parse(p.stdout) : null };
}

for (const transport of ['rest', 'grpc']) test(`${transport}: exact count, rate and percentiles`, t => {
  const r = run(fixture(t, transport));
  assert.equal(r.status, 0, r.stderr);
  const start = r.patch.phaseMetrics.find(p => p.phase === 'steady-state' && p.operation === 'workflow.start');
  assert.deepEqual([start.count, start.rate, start.errorRate, start.p50Ms, start.p95Ms, start.p99Ms],
    [10, 2, 0, 50, 100, 100]);
});

test('regression verdict exits non-zero, including an error rate rising from zero', t => {
  const baseDir = fixture(t);
  const base = read(baseDir, 'summary.json');
  Object.assign(base, run(baseDir).patch);
  const baseline = path.join(baseDir, 'baseline.json');
  fs.writeFileSync(baseline, JSON.stringify(base));
  const current = fixture(t, 'rest', 15);
  const r = run(current, baseline);
  assert.notEqual(r.status, 0);
  assert.equal(r.patch.comparison.verdict, 'regressed');
  const lines = fs.readFileSync(path.join(baseDir, 'k6-output.json'), 'utf8').trim().split('\n').map(JSON.parse);
  lines.find(p => p.metric === 'http_req_failed' && p.data.tags.phase === 'steady-state').data.value = 1;
  fs.writeFileSync(path.join(baseDir, 'k6-output.json'), lines.map(JSON.stringify).join('\n'));
  const errors = run(baseDir, baseline);
  assert.notEqual(errors.status, 0);
  assert.equal(errors.patch.comparison.verdict, 'regressed');
  assert.equal(errors.patch.phaseMetrics.find(p => p.phase === 'steady-state' && p.operation === 'workflow.start').errorRate, 0.1);
});

test('missing/malformed input and metric samples fail closed', t => {
  for (const file of ['k6-output.json', 'k6-summary-export.json', 'summary.json',
    'host-stats.json', 'workflow-outcomes.json']) {
    const dir = fixture(t);
    fs.unlinkSync(path.join(dir, file));
    assert.notEqual(run(dir).status, 0, `missing ${file}`);
    fs.writeFileSync(path.join(dir, file), '{invalid\n');
    assert.notEqual(run(dir).status, 0, `malformed ${file}`);
  }
  const dir = fixture(t);
  fs.writeFileSync(path.join(dir, 'k6-output.json'), '');
  assert.notEqual(run(dir).status, 0, 'no latency samples');
  for (const transport of ['rest', 'grpc']) {
    for (const phase of ['ramp-up', 'steady-state', 'ramp-down']) {
      const invalid = fixture(t, transport);
      const points = fs.readFileSync(path.join(invalid, 'k6-output.json'), 'utf8').trim().split('\n').map(JSON.parse);
      const metric = transport === 'rest' ? 'http_req_duration' : 'grpc_req_duration';
      points.find(point => point.metric === metric && point.data.tags.phase === phase).data.value = -8.297389;
      fs.writeFileSync(path.join(invalid, 'k6-output.json'), points.map(JSON.stringify).join('\n'));
      const result = run(invalid);
      assert.notEqual(result.status, 0, `negative ${transport} latency in ${phase}`);
      assert.match(result.stderr, new RegExp(`invalid ${metric} sample`));
    }
  }
});

test('REST workflow IDs cannot create unbounded metric groups', t => {
  for (const key of ['name', 'url']) {
    const dir = fixture(t);
    const points = fs.readFileSync(path.join(dir, 'k6-output.json'), 'utf8').trim().split('\n').map(JSON.parse);
    points.forEach((point, n) => point.data.tags[key] = `http://engine/v1/instances/unique-${n}`);
    fs.writeFileSync(path.join(dir, 'k6-output.json'), points.map(JSON.stringify).join('\n'));
    assert.notEqual(run(dir).status, 0, `ungrouped ${key} tags must fail the report gate`);
  }
});

test('FAILED, CANCELLED, pending chains, empty result and invalid outcome data fail', t => {
  for (const outcomes of [
    { instances: [{ id: 'one', status: 'FAILED' }], pendingChains: 0 },
    { instances: [{ id: 'one', status: 'CANCELLED' }], pendingChains: 0 },
    { instances: [{ id: 'one', status: 'COMPLETED' }], pendingChains: 1 },
    { instances: [], pendingChains: 0 }, { instances: null },
  ]) {
    const dir = fixture(t);
    write(dir, 'workflow-outcomes.json', outcomes);
    assert.notEqual(run(dir).status, 0, JSON.stringify(outcomes));
  }
});

test('comparison requires a compatible passing baseline', t => {
  const dir = fixture(t);
  assert.notEqual(run(dir, path.join(dir, 'missing.json')).status, 0);
  const base = { ...read(dir, 'summary.json'), ...run(dir).patch };
  for (const mutate of [b => b.result = 'fail', b => b.engine.transport = 'grpc',
    b => b.engine.dispatchMode = 'kafka_outbox', b => b.scenario.config.targetVUs = 20,
    b => b.scenario.config.steady = '6s', b => b.scenario.name = 'loan-chain-full',
    b => b.host.cpuCores = 4, b => b.harnessSha = 'old', b => b.phaseMetrics = []]) {
    const copy = structuredClone(base); mutate(copy);
    write(dir, 'baseline.json', copy);
    assert.notEqual(run(dir, path.join(dir, 'baseline.json')).status, 0, mutate.toString());
  }
});

test('legacy and handleSummary threshold failures and saturated generator fail', t => {
  for (const threshold of [true, { ok: false }]) {
    const dir = fixture(t);
    const summary = read(dir, 'k6-summary-export.json');
    summary.metrics.checks.thresholds['rate==1'] = threshold;
    write(dir, 'k6-summary-export.json', summary);
    assert.notEqual(run(dir).status, 0);
  }
  const dir = fixture(t);
  const host = read(dir, 'host-stats.json'); host.summary.generatorLikelySaturated = true;
  write(dir, 'host-stats.json', host);
  assert.notEqual(run(dir).status, 0);
});

test('shell report command propagates regression, report errors and k6 exit', t => {
  const dir = fixture(t);
  const base = { ...read(dir, 'summary.json'), ...run(dir).patch };
  for (const row of base.phaseMetrics) row.p95Ms /= 2;
  write(dir, 'baseline.json', base);
  for (const [baseline, k6Exit, expected] of [['', '0', 0], ['', '99', 99],
    [path.join(dir, 'baseline.json'), '0', 1], [path.join(dir, 'missing.json'), '0', 1]]) {
    // Restore the initial k6 result between invocations.
    write(dir, 'summary.json', { ...read(dir, 'summary.json'), result: k6Exit === '0' ? 'pass' : 'fail' });
    const r = spawnSync('sh', [finish, dir, baseline, k6Exit], { encoding: 'utf8' });
    assert.equal(r.status, expected, r.stdout + r.stderr);
  }
});

test('full run.sh propagates report, k6, outcome-query and cleanup failures', t => {
  const python = spawnSync('python3', ['-c', 'import sys; print(sys.executable)'], { encoding: 'utf8' }).stdout.trim();
  for (const fault of ['none', 'loan', 'metrics', 'outcome-query', 'outcome-failed', 'k6', 'cleanup', 'baseline']) {
    const input = fixture(t);
    if (fault === 'loan') {
      const points = fs.readFileSync(path.join(input, 'k6-output.json'), 'utf8');
      fs.writeFileSync(path.join(input, 'k6-output.json'), points.replaceAll('workflow.history', 'workflow.task_complete'));
      const summary = read(input, 'k6-summary-export.json');
      summary.metrics.chain_app_unfinished_after_steady_state = { count: 0 };
      summary.metrics.chain_disb_unfinished_after_steady_state = { count: 0 };
      write(input, 'k6-summary-export.json', summary);
    }
    const bin = path.join(input, 'bin'); fs.mkdirSync(bin);
    const stub = `#!${python}\n` + String.raw`
import json, os, pathlib, shutil, signal, sys, threading, time
args = sys.argv[1:]
tool = pathlib.Path(sys.argv[0]).name
source = pathlib.Path(os.environ['FIXTURE_INPUT'])
fault = os.environ['FIXTURE_FAULT']
if tool == 'python3':
    if args and args[0].endswith('host-stats.py'):
        shutil.copyfile(source/'host-stats.json', pathlib.Path(args[1])/'host-stats.json')
        done = threading.Event()
        signal.signal(signal.SIGTERM, lambda *_: done.set())
        done.wait(10)
        sys.exit(0)
    os.execv(os.environ['FIXTURE_PYTHON'], [os.environ['FIXTURE_PYTHON'], *args])
elif tool == 'k6':
    if args[0] == 'version':
        print('k6 v1.7.1 (fixture)')
    else:
        output = pathlib.Path(args[args.index('--summary-export')+1]).parent
        for name in ['k6-output.json','k6-summary-export.json']:
            shutil.copyfile(source/name, output/name)
        if fault == 'metrics': (output/'k6-output.json').write_text('{invalid\n')
        time.sleep(.4)
        sys.exit(99 if fault == 'k6' else 0)
elif tool == 'wget':
    pass
elif tool == 'curl':
    if '-o' in args:
        pathlib.Path(args[args.index('-o')+1]).write_text('engine_metric 1\n')
    elif args[-1].endswith('/v1/definitions'):
        print(json.dumps({'id':'fixture-definition'}))
    elif args[-1].endswith('/v1/instances'):
        print(json.dumps({'id':'fixture-instance'}))
    else:
        print(json.dumps({'status':'COMPLETED'}))
elif tool == 'docker':
    if 'down' in args:
        (source/'cleaned').write_text('yes')
        sys.exit(7 if fault == 'cleanup' else 0)
    if 'images' in args: print('[]')
    elif 'exec' in args:
        if fault == 'outcome-query': sys.exit(7)
        value = json.loads((source/'workflow-outcomes.json').read_text())
        if fault == 'outcome-failed': value['instances'][0]['status'] = 'FAILED'
        print(json.dumps(value))
    elif 'version' in args: print('fixture-docker')
else:
    raise SystemExit('unexpected stub: '+tool)
`;
    for (const tool of ['python3', 'k6', 'wget', 'curl', 'docker'])
      fs.writeFileSync(path.join(bin, tool), stub, { mode: 0o755 });
    const args = [path.resolve(__dirname, '../run.sh'), '--ramp-up=5s', '--steady=5s', '--ramp-down=5s', '--target-vus=10'];
    if (fault === 'baseline') args.push('--compare-to=missing-fixture-baseline');
    if (fault === 'loan') args.push('--scenario=loan-chain-full');
    const r = spawnSync('bash', args, { encoding: 'utf8', timeout: 15000, env: {
      ...process.env, PATH: bin + path.delimiter + process.env.PATH,
      FIXTURE_INPUT: input, FIXTURE_FAULT: fault, FIXTURE_PYTHON: python,
      PERF_SENIOR_COMPLETER_VUS: '1',
    } });
    const runId = /runId=([^\s]+)/.exec(r.stdout)?.[1];
    assert.ok(runId, r.stdout + r.stderr);
    const output = path.resolve(__dirname, '../reports', runId);
    t.after(() => fs.rmSync(output, { recursive: true, force: true }));
    assert.ok(fs.existsSync(path.join(input, 'cleaned')), `${fault}: cleanup did not run\n${r.stderr}`);
    if (['none', 'loan'].includes(fault)) assert.equal(r.status, 0, r.stdout + r.stderr);
    else assert.notEqual(r.status, 0, `${fault}: swallowed failure`);
    if (fault === 'k6') assert.equal(r.status, 99, r.stderr);
    if (['none','loan','cleanup','outcome-failed','metrics','baseline'].includes(fault))
      assert.equal(read(output, 'summary.json').result, ['none', 'loan'].includes(fault) ? 'pass' : 'fail', fault);
    if (fault === 'loan') {
      const config = read(output, 'summary.json').scenario.config;
      assert.equal(config.definitionRef, 'fixture-definition');
      assert.equal(config.seniorCompleterVUs, 1);
    }
  }
});

test('loan command rejects competing task completers before starting the stack', () => {
  const result = spawnSync('bash', [path.resolve(__dirname, '../run.sh'), '--scenario=loan-chain-full'], {
    encoding: 'utf8', env: { ...process.env, PERF_SENIOR_COMPLETER_VUS: '2' },
  });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /requires one senior-task completer/);
  assert.doesNotMatch(result.stdout, /booting compose/);
});
