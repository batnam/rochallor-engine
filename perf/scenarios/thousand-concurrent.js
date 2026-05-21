// k6 load scenario: 1,000 concurrent active workflow instances.
//
// Executor:  ramping-vus (1 VU == 1 active workflow)
// Phases:    ramp-up → steady-state (1,000 VUs) → ramp-down (configurable via env)
// Operations: workflow.start → workflow.poll@200ms → workflow.history (+ workflow.list every 10)
// Teardown:  count non-terminal workflows 10 s after steady-state
//
// Additional features:
//   - failure_kind counters per category (engine_error, engine_timeout,
//     engine_unresponsive, generator_side)
//   - phase tag on every request (ramp-up / steady-state / ramp-down)
//
// Design decisions: specs/003-performance-load-testing/research.md
// Threshold contract: specs/003-performance-load-testing/data-model.md §ThresholdSet

import { sleep } from 'k6';
import { Counter } from 'k6/metrics';
import { check } from 'k6';
import exec from 'k6/execution';
import {
  startWorkflow,
  pollInstance,
  getHistory,
  listInstances,
  isTerminal,
  isKnownStatus,
} from '../lib/client.js';
import { THRESHOLDS } from '../lib/thresholds.js';

// Environment variables injected by run.sh
const BASE_URL      = __ENV.PERF_ENGINE_BASE_URL || 'http://localhost:18080';
const DEFINITION_ID = __ENV.PERF_DEFINITION_ID;
const RUN_ID        = __ENV.PERF_RUN_ID || 'local';
const RAMP_UP       = __ENV.PERF_RAMP_UP   || '2m';
const STEADY        = __ENV.PERF_STEADY     || '10m';
const RAMP_DOWN     = __ENV.PERF_RAMP_DOWN  || '1m';
const TARGET_VUS    = parseInt(__ENV.PERF_TARGET_VUS || '1000', 10);

// Poll cadence and per-iteration deadline
const POLL_INTERVAL_MS   = 200;
const ITERATION_DEADLINE = 60; // seconds

// How often (per VU, per iteration count) to exercise the list endpoint
const LIST_EVERY_N_ITERATIONS = 10;

// --- Custom metrics ---

// Counts workflows still non-terminal 10 s after steady-state
const unfinishedCounter = new Counter('workflows_unfinished_after_steady_state');

// Failure classification.
// One counter per category; each incremented on any failure event.
const failKind = {
  engine_error:        new Counter('failure_kind_engine_error'),
  engine_timeout:      new Counter('failure_kind_engine_timeout'),
  engine_unresponsive: new Counter('failure_kind_engine_unresponsive'),
  generator_side:      new Counter('failure_kind_generator_side'),
};

// --- k6 scenario options ---

export const options = {
  scenarios: {
    thousand_concurrent: {
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
  thresholds: THRESHOLDS,
};

// --- Helpers ---

// Parse k6 duration string ("2m", "30s", "500ms") → milliseconds.
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

// Returns "ramp-up" | "steady-state" | "ramp-down" based on elapsed time.
function currentPhase(testStartMs) {
  const elapsed = Date.now() - testStartMs;
  if (elapsed < RAMP_UP_MS) return 'ramp-up';
  if (elapsed < RAMP_UP_MS + STEADY_MS) return 'steady-state';
  return 'ramp-down';
}

// Classify a k6 response into one of the four failure categories.
// Returns null if the response is not a failure.
function classifyFailure(res, iterationDeadlineExpired) {
  if (iterationDeadlineExpired) return 'engine_unresponsive';

  // Network-level errors (generator-side): DNS (1100-1199), connection (1200-1299)
  const ec = res.error_code || 0;
  if (ec >= 1100 && ec < 1300) return 'generator_side';

  // Request timeout from k6 side (1050)
  if (ec === 1050) return 'engine_timeout';

  // Engine returned a server error (5xx) or service-limit 4xx
  if (res.status >= 500) return 'engine_error';
  if (res.status === 429 || res.status === 503) return 'engine_error';

  return null; // not a failure
}

// --- Lifecycle ---

// setup() runs once before VUs start. DEFINITION_ID is already uploaded by run.sh.
export function setup() {
  if (!DEFINITION_ID) {
    throw new Error('PERF_DEFINITION_ID is not set — run via perf/run.sh');
  }
  return {
    definitionId: DEFINITION_ID,
    baseUrl: BASE_URL,
    testStartMs: Date.now(),  // used for phase tagging
  };
}

// default() is the per-VU iteration body.
export default function (data) {
  const { definitionId, baseUrl, testStartMs } = data;

  // tag all requests in this iteration with the current phase.
  exec.vu.tags['phase'] = currentPhase(testStartMs);

  // --- workflow.start ---
  const businessKey = `perf-${RUN_ID}-vu${__VU}-iter${__ITER}`;
  const startRes = startWorkflow(baseUrl, definitionId, businessKey, {});

  const startOk = check(startRes, {
    'workflow.start: status 201': (r) => r.status === 201,
  });

  if (!startOk) {
    // classify and count the failure kind
    const kind = classifyFailure(startRes, false);
    if (kind) failKind[kind].add(1);
    return;
  }

  const instanceId = JSON.parse(startRes.body).id;

  // --- workflow.poll until terminal or per-iteration deadline ---
  const deadline = Date.now() + ITERATION_DEADLINE * 1000;
  let terminal = false;

  while (Date.now() < deadline) {
    // Re-tag in case the phase boundary crossed mid-poll-loop
    exec.vu.tags['phase'] = currentPhase(testStartMs);

    const pollRes = pollInstance(baseUrl, instanceId);

    const pollOk = check(pollRes, {
      'workflow.poll: status 200': (r) => r.status === 200,
    });

    if (!pollOk) {
      const kind = classifyFailure(pollRes, false);
      if (kind) failKind[kind].add(1);
      sleep(POLL_INTERVAL_MS / 1000);
      continue;
    }

    const body = JSON.parse(pollRes.body);
    const status = body.status;

    if (!isKnownStatus(status)) {
      console.warn(`[perf] unknown status '${status}' for instance ${instanceId}`);
    }

    if (isTerminal(status)) {
      terminal = true;
      break;
    }

    sleep(POLL_INTERVAL_MS / 1000);
  }

  if (!terminal) {
    // Per-iteration deadline expired — engine_unresponsive
    failKind.engine_unresponsive.add(1);
    console.warn(`[perf] workflow ${instanceId} did not complete within ${ITERATION_DEADLINE}s`);
  }

  // Re-tag for the remaining calls
  exec.vu.tags['phase'] = currentPhase(testStartMs);

  // --- workflow.history (once per iteration) ---
  const histRes = getHistory(baseUrl, instanceId);
  const histOk = check(histRes, {
    'workflow.history: status 200': (r) => r.status === 200,
  });
  if (!histOk) {
    const kind = classifyFailure(histRes, false);
    if (kind) failKind[kind].add(1);
  }

  // --- workflow.list (once every LIST_EVERY_N_ITERATIONS per VU) ---
  if (__ITER % LIST_EVERY_N_ITERATIONS === 0) {
    const listRes = listInstances(baseUrl, { page: 0, pageSize: 20 });
    const listOk = check(listRes, {
      'workflow.list: status 200': (r) => r.status === 200,
    });
    if (!listOk) {
      const kind = classifyFailure(listRes, false);
      if (kind) failKind[kind].add(1);
    }
  }
}

// teardown() runs once after all VUs finish.
// Waits 10 s, then counts non-terminal instances created by this run.
export function teardown(data) {
  const { baseUrl } = data;

  console.log('[perf] teardown: waiting 10 s before unfinished-workflow check...');
  sleep(10);

  let unfinished = 0;
  const pageSize = 200;
  const runPrefix = `perf-${RUN_ID}-`;

  for (const status of ['ACTIVE', 'WAITING']) {
    let page = 0;
    let more = true;
    while (more) {
      const res = listInstances(baseUrl, { page, pageSize, status });
      if (res.status !== 200) break;

      const body = JSON.parse(res.body);
      const items = body.items || [];

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
