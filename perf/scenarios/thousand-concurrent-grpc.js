// k6 load scenario: 1,000 concurrent active workflow instances over gRPC.
//
// Mirrors thousand-concurrent.js exactly; differences:
//   - Transport: gRPC via perf/lib/grpc-client.js
//   - Env:       PERF_ENGINE_GRPC_URL instead of PERF_ENGINE_BASE_URL
//   - Metrics:   grpc_req_duration + grpc_error_rate instead of http_req_*
//   - Thresholds: GRPC_THRESHOLDS
//   - Status:    InstanceStatus string enum names (protobuf JSON mapping)
//   - Teardown:  res.message.instances (not .items as in the REST response)
//
// Design decisions: specs/003-performance-load-testing/research.md

import { sleep } from 'k6';
import { Counter } from 'k6/metrics';
import { check } from 'k6';
import grpc from 'k6/net/grpc';
import exec from 'k6/execution';
import {
  startWorkflow,
  pollInstance,
  getHistory,
  listInstances,
  grpcErrorRate,
} from '../lib/grpc-client.js';
import { GRPC_THRESHOLDS } from '../lib/thresholds.js';

const GRPC_URL     = __ENV.PERF_ENGINE_GRPC_URL || 'localhost:19090';
const DEFINITION_ID = __ENV.PERF_DEFINITION_ID;
const RUN_ID       = __ENV.PERF_RUN_ID || 'local';
const RAMP_UP      = __ENV.PERF_RAMP_UP   || '2m';
const STEADY       = __ENV.PERF_STEADY    || '10m';
const RAMP_DOWN    = __ENV.PERF_RAMP_DOWN || '1m';
const TARGET_VUS   = parseInt(__ENV.PERF_TARGET_VUS || '1000', 10);

const POLL_INTERVAL_MS   = 200;
const ITERATION_DEADLINE = 60;
const LIST_EVERY_N_ITERATIONS = 10;

// InstanceStatus string enum values (protobuf JSON mapping)
const TERMINAL_STATUSES = ['INSTANCE_STATUS_COMPLETED', 'INSTANCE_STATUS_FAILED', 'INSTANCE_STATUS_CANCELLED'];
function isTerminal(status) { return TERMINAL_STATUSES.indexOf(status) !== -1; }

// --- Custom metrics ---

const unfinishedCounter = new Counter('workflows_unfinished_after_steady_state');

const failKind = {
  engine_error:        new Counter('failure_kind_engine_error'),
  engine_timeout:      new Counter('failure_kind_engine_timeout'),
  engine_unresponsive: new Counter('failure_kind_engine_unresponsive'),
  generator_side:      new Counter('failure_kind_generator_side'),
};

// --- k6 scenario options ---

export const options = {
  scenarios: {
    thousand_concurrent_grpc: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: RAMP_UP,   target: TARGET_VUS },
        { duration: STEADY,    target: TARGET_VUS },
        { duration: RAMP_DOWN, target: 0 },
      ],
      gracefulRampDown: '30s',
    },
  },
  thresholds: GRPC_THRESHOLDS,
};

// --- Helpers ---

function parseDurationMs(s) {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m|h)?$/.exec(String(s).trim());
  if (!m) return 0;
  const n = parseFloat(m[1]);
  switch (m[2] || 's') {
    case 'ms': return n;
    case 's':  return n * 1000;
    case 'm':  return n * 60000;
    case 'h':  return n * 3600000;
    default:   return n * 1000;
  }
}

const RAMP_UP_MS = parseDurationMs(RAMP_UP);
const STEADY_MS  = parseDurationMs(STEADY);

function currentPhase(testStartMs) {
  const elapsed = Date.now() - testStartMs;
  if (elapsed < RAMP_UP_MS) return 'ramp-up';
  if (elapsed < RAMP_UP_MS + STEADY_MS) return 'steady-state';
  return 'ramp-down';
}

// gRPC failure classification (data-model §FailureClassification).
// No generator_side category — network failures surface as StatusUnavailable.
function classifyGrpcFailure(res, deadlineExpired) {
  if (deadlineExpired) return 'engine_unresponsive';
  if (res.status === grpc.StatusDeadlineExceeded) return 'engine_timeout';
  if (res.status === grpc.StatusUnavailable) return 'engine_unresponsive';
  if (!res.ok) return 'engine_error';
  return null;
}

// --- Lifecycle ---

export function setup() {
  if (!DEFINITION_ID) {
    throw new Error('PERF_DEFINITION_ID is not set — run via perf/run.sh --grpc');
  }
  if (!__ENV.PERF_ENGINE_GRPC_URL) {
    throw new Error('PERF_ENGINE_GRPC_URL is not set — run via perf/run.sh --grpc');
  }
  return {
    definitionId: DEFINITION_ID,
    grpcUrl: GRPC_URL,
    testStartMs: Date.now(),
  };
}

export default function (data) {
  const { definitionId, grpcUrl, testStartMs } = data;

  exec.vu.tags['phase'] = currentPhase(testStartMs);

  // --- workflow.start ---
  const businessKey = `perf-${RUN_ID}-vu${__VU}-iter${__ITER}`;
  const startRes = startWorkflow(grpcUrl, definitionId, businessKey, {});

  grpcErrorRate.add(startRes.ok ? 0 : 1, { operation: 'workflow.start' });

  const startOk = check(startRes, {
    'workflow.start: gRPC OK': (r) => r.ok,
  });

  if (!startOk) {
    const kind = classifyGrpcFailure(startRes, false);
    if (kind) failKind[kind].add(1);
    return;
  }

  const instanceId = startRes.message.instance.id;

  // --- workflow.poll until terminal or per-iteration deadline ---
  const deadline = Date.now() + ITERATION_DEADLINE * 1000;
  let terminal = false;

  while (Date.now() < deadline) {
    exec.vu.tags['phase'] = currentPhase(testStartMs);

    const pollRes = pollInstance(grpcUrl, instanceId);
    grpcErrorRate.add(pollRes.ok ? 0 : 1, { operation: 'workflow.poll' });

    const pollOk = check(pollRes, {
      'workflow.poll: gRPC OK': (r) => r.ok,
    });

    if (!pollOk) {
      const kind = classifyGrpcFailure(pollRes, false);
      if (kind) failKind[kind].add(1);
      sleep(POLL_INTERVAL_MS / 1000);
      continue;
    }

    const status = pollRes.message.instance.status;

    if (isTerminal(status)) {
      terminal = true;
      break;
    }

    sleep(POLL_INTERVAL_MS / 1000);
  }

  if (!terminal) {
    failKind.engine_unresponsive.add(1);
    console.warn(`[perf] workflow ${instanceId} did not complete within ${ITERATION_DEADLINE}s`);
  }

  exec.vu.tags['phase'] = currentPhase(testStartMs);

  // --- workflow.history (once per iteration) ---
  const histRes = getHistory(grpcUrl, instanceId);
  grpcErrorRate.add(histRes.ok ? 0 : 1, { operation: 'workflow.history' });
  const histOk = check(histRes, {
    'workflow.history: gRPC OK': (r) => r.ok,
  });
  if (!histOk) {
    const kind = classifyGrpcFailure(histRes, false);
    if (kind) failKind[kind].add(1);
  }

  // --- workflow.list (once every LIST_EVERY_N_ITERATIONS per VU) ---
  if (__ITER % LIST_EVERY_N_ITERATIONS === 0) {
    const listRes = listInstances(grpcUrl, { page: 0, pageSize: 20 });
    grpcErrorRate.add(listRes.ok ? 0 : 1, { operation: 'workflow.list' });
    const listOk = check(listRes, {
      'workflow.list: gRPC OK': (r) => r.ok,
    });
    if (!listOk) {
      const kind = classifyGrpcFailure(listRes, false);
      if (kind) failKind[kind].add(1);
    }
  }
}

// teardown() runs once after all VUs finish.
// Waits 10 s, then counts non-terminal instances created by this run.
// Uses res.message.instances (not .items — gRPC ListInstancesResponse field name).
export function teardown(data) {
  const { grpcUrl } = data;

  console.log('[perf] teardown: waiting 10 s before unfinished-workflow check...');
  sleep(10);

  let unfinished = 0;
  const pageSize = 200;
  const runPrefix = `perf-${RUN_ID}-`;

  // 1 = INSTANCE_STATUS_ACTIVE, 2 = INSTANCE_STATUS_WAITING
  for (const statusInt of [1, 2]) {
    let page = 0;
    let more = true;
    while (more) {
      const res = listInstances(grpcUrl, { page, pageSize, status: statusInt });
      if (!res.ok) break;

      const items = (res.message && res.message.instances) || [];

      for (const item of items) {
        const bk = item.businessKey || '';
        if (bk.startsWith(runPrefix)) {
          unfinished++;
        }
      }

      more = items.length === pageSize;
      page++;
    }
  }

  console.log(`[perf] teardown: ${unfinished} unfinished workflow(s) from this run`);
  if (unfinished > 0) {
    unfinishedCounter.add(unfinished);
  }
}
