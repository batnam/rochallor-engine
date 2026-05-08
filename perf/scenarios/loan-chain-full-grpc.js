// k6 stress scenario: full loan workflow chain end-to-end over gRPC.
//
// Mirrors loan-chain-full.js exactly; differences:
//   - Transport:  gRPC via perf/lib/grpc-client.js
//   - Env:        PERF_ENGINE_GRPC_URL instead of PERF_ENGINE_BASE_URL
//   - Metrics:    grpc_req_duration + grpc_error_rate instead of http_req_*
//   - Thresholds: GRPC_THRESHOLDS + chain-specific counters
//   - Status:     InstanceStatus string enum names ("INSTANCE_STATUS_*")
//   - Field shape: protobuf JSON mapping (camelCase) — currentStepIds, businessKey,
//                  res.message.instance, res.message.instances
//
// See loan-chain-full.js for the path semantics — they're identical.

import { sleep, check } from 'k6';
import { Counter } from 'k6/metrics';
import grpc from 'k6/net/grpc';
import exec from 'k6/execution';
import {
  startWorkflow,
  pollInstance,
  listInstances,
  completeUserTask,
  grpcErrorRate,
} from '../lib/grpc-client.js';
import { GRPC_THRESHOLDS } from '../lib/thresholds.js';

// --- Environment ---
const GRPC_URL          = __ENV.PERF_ENGINE_GRPC_URL || 'localhost:19090';
const APP_DEFINITION_ID = __ENV.PERF_LOAN_APP_DEFINITION_ID;
const DISB_DEFINITION_ID = __ENV.PERF_LOAN_DISB_DEFINITION_ID;
const RUN_ID            = __ENV.PERF_RUN_ID || 'local';
const RAMP_UP           = __ENV.PERF_RAMP_UP   || '2m';
const STEADY            = __ENV.PERF_STEADY    || '10m';
const RAMP_DOWN         = __ENV.PERF_RAMP_DOWN || '1m';
const TARGET_VUS        = parseInt(__ENV.PERF_TARGET_VUS || '1000', 10);
const SENIOR_COMPLETER_VUS = parseInt(__ENV.PERF_SENIOR_COMPLETER_VUS || '500', 10);

const POLL_INTERVAL_MS   = 250;
const ITERATION_DEADLINE = 120;
const APP_USER_TASK_ID   = 'manual-review-task';
const DISB_USER_TASK_ID  = 'senior-approval-task';
const TEARDOWN_DRAIN_PASSES = 120;          // up to ~120 × 1 s = 120 s drain budget
const TEARDOWN_SETTLE_SEC = 30;            // sleep after drain before final count

// protobuf JSON enum: full names
const TERMINAL_STATUSES = [
  'INSTANCE_STATUS_COMPLETED',
  'INSTANCE_STATUS_FAILED',
  'INSTANCE_STATUS_CANCELLED',
];
function isTerminal(status) { return TERMINAL_STATUSES.indexOf(status) !== -1; }

// --- Custom metrics ---
const appStarted    = new Counter('chain_app_started');
const appCompleted  = new Counter('chain_app_completed');
const userTasksDone = new Counter('chain_user_tasks_completed');
const appUnfinished  = new Counter('chain_app_unfinished_after_steady_state');
const disbUnfinished = new Counter('chain_disb_unfinished_after_steady_state');

const failKind = {
  engine_error:        new Counter('failure_kind_engine_error'),
  engine_timeout:      new Counter('failure_kind_engine_timeout'),
  engine_unresponsive: new Counter('failure_kind_engine_unresponsive'),
  generator_side:      new Counter('failure_kind_generator_side'),
};

// --- Scenario options ---

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
const TOTAL_MS   = RAMP_UP_MS + STEADY_MS + parseDurationMs(RAMP_DOWN);

function totalDurationStr() {
  return Math.ceil(TOTAL_MS / 1000) + 's';
}

export const options = {
  // k6 default is 60s, which force-kills the drain loop below before it can
  // finish (TEARDOWN_DRAIN_PASSES × 1s + TEARDOWN_SETTLE_SEC ≈ 150s budget).
  teardownTimeout: '5m',
  scenarios: {
    chain_load: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: RAMP_UP,   target: TARGET_VUS },
        { duration: STEADY,    target: TARGET_VUS },
        { duration: RAMP_DOWN, target: 0 },
      ],
      gracefulRampDown: '30s',
      exec: 'chainLoad',
    },
    senior_task_completer: {
      executor: 'constant-vus',
      vus: SENIOR_COMPLETER_VUS,
      duration: totalDurationStr(),
      exec: 'completeSeniorTasks',
    },
  },
  thresholds: (function () {
    // chain scenarios use chain_app/chain_disb_unfinished counters instead of
    // the global workflows_unfinished_after_steady_state — strip it so k6
    // doesn't error with "no metric name ... found".
    const base = Object.assign({}, GRPC_THRESHOLDS);
    delete base['workflows_unfinished_after_steady_state'];
    return Object.assign(base, {
      'chain_app_unfinished_after_steady_state':  ['count==0'],
      'chain_disb_unfinished_after_steady_state': ['count==0'],
      'chain_app_completed':                      ['count>0'],
    });
  })(),
};

// --- Lifecycle ---

export function setup() {
  if (!APP_DEFINITION_ID || !DISB_DEFINITION_ID) {
    throw new Error('PERF_LOAN_APP_DEFINITION_ID / PERF_LOAN_DISB_DEFINITION_ID not set — run via perf/run.sh --scenario=loan-chain-full --grpc');
  }
  if (!__ENV.PERF_ENGINE_GRPC_URL) {
    throw new Error('PERF_ENGINE_GRPC_URL is not set — run via perf/run.sh --grpc');
  }
  return {
    appDefinitionId:  APP_DEFINITION_ID,
    disbDefinitionId: DISB_DEFINITION_ID,
    grpcUrl:          GRPC_URL,
    testStartMs:      Date.now(),
  };
}

function currentPhase(testStartMs) {
  const elapsed = Date.now() - testStartMs;
  if (elapsed < RAMP_UP_MS) return 'ramp-up';
  if (elapsed < RAMP_UP_MS + STEADY_MS) return 'steady-state';
  return 'ramp-down';
}

function classifyGrpcFailure(res, deadlineExpired) {
  if (deadlineExpired) return 'engine_unresponsive';
  if (res.status === grpc.StatusDeadlineExceeded) return 'engine_timeout';
  if (res.status === grpc.StatusUnavailable) return 'engine_unresponsive';
  if (!res.ok) return 'engine_error';
  return null;
}

function recordFailure(res, deadlineExpired) {
  const k = classifyGrpcFailure(res, !!deadlineExpired);
  if (k) failKind[k].add(1);
}

// --- chain_load: per-VU iteration ---
export function chainLoad(data) {
  const { appDefinitionId, grpcUrl, testStartMs } = data;
  exec.vu.tags['phase'] = currentPhase(testStartMs);
  exec.vu.tags['scenario_role'] = 'chain_load';

  const businessKey       = `perf-loanchain-${RUN_ID}-vu${__VU}-iter${__ITER}`;
  const chainCorrelationId = businessKey;

  const startVars = {
    creditScore:         600,
    fraudScore:          0.0,
    loanAmount:          600000000,
    chainCorrelationId,
  };

  const startRes = startWorkflow(grpcUrl, appDefinitionId, businessKey, startVars);
  grpcErrorRate.add(startRes.ok ? 0 : 1, { operation: 'workflow.start' });

  if (!check(startRes, { 'workflow.start: gRPC OK': (r) => r.ok })) {
    recordFailure(startRes, false);
    return;
  }
  appStarted.add(1);
  const instanceId = startRes.message.instance.id;

  const deadline = Date.now() + ITERATION_DEADLINE * 1000;
  let userTaskCompleted = false;
  let terminal = false;

  while (Date.now() < deadline) {
    exec.vu.tags['phase'] = currentPhase(testStartMs);

    const pollRes = pollInstance(grpcUrl, instanceId);
    grpcErrorRate.add(pollRes.ok ? 0 : 1, { operation: 'workflow.poll' });

    if (!check(pollRes, { 'workflow.poll: gRPC OK': (r) => r.ok })) {
      recordFailure(pollRes, false);
      sleep(POLL_INTERVAL_MS / 1000);
      continue;
    }

    const inst = pollRes.message.instance;
    const status = inst.status;
    const currentSteps = inst.currentStepIds || [];

    if (isTerminal(status)) {
      terminal = true;
      if (status === 'INSTANCE_STATUS_COMPLETED') appCompleted.add(1);
      break;
    }

    if (!userTaskCompleted && currentSteps.indexOf(APP_USER_TASK_ID) !== -1) {
      const completeRes = completeUserTask(
        grpcUrl,
        instanceId,
        APP_USER_TASK_ID,
        { reviewDecision: 'APPROVED' },
      );
      grpcErrorRate.add(completeRes.ok ? 0 : 1, { operation: 'workflow.task_complete' });
      if (check(completeRes, { 'workflow.task_complete: gRPC OK': (r) => r.ok })) {
        userTaskCompleted = true;
        userTasksDone.add(1);
      } else {
        recordFailure(completeRes, false);
      }
    }

    sleep(POLL_INTERVAL_MS / 1000);
  }

  if (!terminal) {
    failKind.engine_unresponsive.add(1);
    console.warn(`[perf] loan-chain-grpc: app ${instanceId} did not reach terminal in ${ITERATION_DEADLINE}s`);
  }
}

// --- senior_task_completer: side scenario ---
export function completeSeniorTasks(data) {
  const { disbDefinitionId, grpcUrl, testStartMs } = data;
  exec.vu.tags['phase'] = currentPhase(testStartMs);
  exec.vu.tags['scenario_role'] = 'senior_completer';

  const found = findAndCompleteSeniorTasks(grpcUrl, disbDefinitionId, /*pageLimit*/ 10);
  if (found === 0) {
    sleep(0.5);
  }
}

// 2 = INSTANCE_STATUS_WAITING (protobuf enum integer)
const STATUS_WAITING_INT = 2;
const STATUS_ACTIVE_INT  = 1;

function findAndCompleteSeniorTasks(grpcUrl, disbDefinitionId, pageLimit) {
  let completed = 0;
  for (let page = 0; page < pageLimit; page++) {
    const listRes = listInstances(grpcUrl, {
      definitionId: disbDefinitionId,
      status: STATUS_WAITING_INT,
      page,
      pageSize: 100,
    });
    grpcErrorRate.add(listRes.ok ? 0 : 1, { operation: 'workflow.list' });
    if (!check(listRes, { 'workflow.list: gRPC OK': (r) => r.ok })) {
      recordFailure(listRes, false);
      return completed;
    }
    const items = (listRes.message && listRes.message.instances) || [];
    if (items.length === 0) return completed;

    for (let i = 0; i < items.length; i++) {
      const inst = items[i];
      const steps = inst.currentStepIds || [];
      if (steps.indexOf(DISB_USER_TASK_ID) === -1) continue;

      const completeRes = completeUserTask(
        grpcUrl,
        inst.id,
        DISB_USER_TASK_ID,
        { seniorDecision: 'APPROVED' },
      );
      grpcErrorRate.add(completeRes.ok ? 0 : 1, { operation: 'workflow.task_complete' });
      if (check(completeRes, { 'workflow.task_complete: gRPC OK': (r) => r.ok })) {
        completed++;
        userTasksDone.add(1);
      } else {
        recordFailure(completeRes, false);
      }
    }

    if (items.length < 100) return completed;
  }
  return completed;
}

// --- Teardown ---
export function teardown(data) {
  const { appDefinitionId, disbDefinitionId, grpcUrl } = data;

  console.log('[perf] loan-chain-grpc teardown: draining remaining senior-approval-tasks...');
  for (let i = 0; i < TEARDOWN_DRAIN_PASSES; i++) {
    const n = findAndCompleteSeniorTasks(grpcUrl, disbDefinitionId, /*pageLimit*/ 20);
    if (n === 0) break;
    sleep(1);
  }

  console.log(`[perf] loan-chain-grpc teardown: sleeping ${TEARDOWN_SETTLE_SEC}s before final non-terminal count...`);
  sleep(TEARDOWN_SETTLE_SEC);

  appUnfinished.add(countNonTerminal(grpcUrl, appDefinitionId, 'app'));
  disbUnfinished.add(countNonTerminal(grpcUrl, disbDefinitionId, 'disb'));
}

function countNonTerminal(grpcUrl, definitionId, label) {
  let nonTerminal = 0;
  const pageSize = 200;
  for (const statusInt of [STATUS_ACTIVE_INT, STATUS_WAITING_INT]) {
    let page = 0;
    while (true) {
      const res = listInstances(grpcUrl, { definitionId, status: statusInt, page, pageSize });
      if (!res.ok) break;
      const items = (res.message && res.message.instances) || [];
      nonTerminal += items.length;
      if (items.length < pageSize) break;
      page++;
    }
  }
  console.log(`[perf] loan-chain-grpc teardown: ${label} non-terminal count = ${nonTerminal}`);
  return nonTerminal;
}
