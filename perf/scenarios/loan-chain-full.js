// k6 stress scenario: full loan workflow chain end-to-end.
//
// Definitions: perf/definitions/loan-chain/workflow-loan-application.json
//              perf/definitions/loan-chain/workflow-loan-disbursement.json
//
// Per VU iteration ("chain_load" scenario):
//   1. POST /v1/instances    — start loan-application with creditScore=600,
//      fraudScore=0.0, loanAmount=600_000_000 → forces the manual-review path
//      and the senior-approval branch in disbursement so every step type
//      (SERVICE_TASK, PARALLEL_GATEWAY, JOIN_GATEWAY, TRANSFORMATION,
//      DECISION, USER_TASK, END) is exercised.
//   2. Poll the application instance until "manual-review-task" is in
//      currentStepIds.
//   3. POST .../user-tasks/manual-review-task/complete with reviewDecision=APPROVED.
//   4. Poll until the application reaches a terminal status (must be COMPLETED).
//
// Side scenario ("senior_task_completer", constant-vus):
//   Continuously lists disbursement instances in WAITING status and completes
//   any senior-approval-task it finds. Decoupling the senior approval from
//   the per-VU iteration keeps chainLoad iterations short and lets the
//   chained workflow advance independently.
//
// Verification (teardown):
//   - Drain any remaining senior-approval-task user tasks until none remain
//     (or attempts exhausted).
//   - Sleep 30 s for chains to settle.
//   - Count loan-application and loan-disbursement instances by status; any
//     non-terminal instance fails the run via the unfinished counters.
//
// Reproducibility note: perf/run.sh runs `docker compose down -v` between
// runs, so the engine database is empty at run start. That means every
// loan-application or loan-disbursement instance the engine knows about
// belongs to THIS run — we don't need a per-run filter at teardown.

import { sleep, check } from 'k6';
import { Counter } from 'k6/metrics';
import exec from 'k6/execution';
import {
  startWorkflow,
  pollInstance,
  listInstances,
  completeUserTask,
  isTerminal,
} from '../lib/client.js';
import { THRESHOLDS } from '../lib/thresholds.js';

// --- Environment ---
const BASE_URL          = __ENV.PERF_ENGINE_BASE_URL || 'http://localhost:18080';
const APP_DEFINITION_ID = __ENV.PERF_LOAN_APP_DEFINITION_ID;
const DISB_DEFINITION_ID = __ENV.PERF_LOAN_DISB_DEFINITION_ID;
const RUN_ID            = __ENV.PERF_RUN_ID || 'local';
const RAMP_UP           = __ENV.PERF_RAMP_UP   || '2m';
const STEADY            = __ENV.PERF_STEADY    || '10m';
const RAMP_DOWN         = __ENV.PERF_RAMP_DOWN || '1m';
const TARGET_VUS        = parseInt(__ENV.PERF_TARGET_VUS || '1000', 10);
const SENIOR_COMPLETER_VUS = parseInt(__ENV.PERF_SENIOR_COMPLETER_VUS || '500', 10);

const POLL_INTERVAL_MS   = 250;
const ITERATION_DEADLINE = 120;            // seconds — chain is ~12 steps + 1 user task
const APP_USER_TASK_ID   = 'manual-review-task';
const DISB_USER_TASK_ID  = 'senior-approval-task';
const TEARDOWN_DRAIN_PASSES = 120;         // up to ~120 × 1 s = 120 s drain budget
const TEARDOWN_SETTLE_SEC = 30;            // sleep after drain before final count

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

// completer runs for the full chainLoad duration so user tasks created late
// in ramp-down still get completed before teardown drains the rest.
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
    const base = Object.assign({}, THRESHOLDS);
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
    throw new Error('PERF_LOAN_APP_DEFINITION_ID / PERF_LOAN_DISB_DEFINITION_ID not set — run via perf/run.sh --scenario=loan-chain-full');
  }
  return {
    appDefinitionId:  APP_DEFINITION_ID,
    disbDefinitionId: DISB_DEFINITION_ID,
    baseUrl:          BASE_URL,
    testStartMs:      Date.now(),
  };
}

function currentPhase(testStartMs) {
  const elapsed = Date.now() - testStartMs;
  if (elapsed < RAMP_UP_MS) return 'ramp-up';
  if (elapsed < RAMP_UP_MS + STEADY_MS) return 'steady-state';
  return 'ramp-down';
}

function classifyFailure(res, iterationDeadlineExpired) {
  if (iterationDeadlineExpired) return 'engine_unresponsive';
  const ec = res.error_code || 0;
  if (ec >= 1100 && ec < 1300) return 'generator_side';
  if (ec === 1050) return 'engine_timeout';
  if (res.status >= 500) return 'engine_error';
  if (res.status === 429 || res.status === 503) return 'engine_error';
  return null;
}

function recordFailure(res, deadlineExpired) {
  const k = classifyFailure(res, !!deadlineExpired);
  if (k) failKind[k].add(1);
}

// --- chain_load: per-VU iteration ---
// One iteration = one full app instance from start to COMPLETED, including
// completing the manual-review-task user task in the middle.
export function chainLoad(data) {
  const { appDefinitionId, baseUrl, testStartMs } = data;
  exec.vu.tags['phase'] = currentPhase(testStartMs);
  exec.vu.tags['scenario_role'] = 'chain_load';

  const businessKey       = `perf-loanchain-${RUN_ID}-vu${__VU}-iter${__ITER}`;
  const chainCorrelationId = businessKey;  // propagates to disbursement via variables

  const startVars = {
    creditScore:         600,            // 500 ≤ score < 650 → manual review
    fraudScore:          0.0,            // not high-risk
    loanAmount:          600000000,      // > 500M → senior approval branch
    chainCorrelationId,
  };

  const startRes = startWorkflow(baseUrl, appDefinitionId, businessKey, startVars);
  const startOk = check(startRes, {
    'workflow.start: 201': (r) => r.status === 201,
  });
  if (!startOk) {
    recordFailure(startRes, false);
    return;
  }
  appStarted.add(1);
  const instanceId = JSON.parse(startRes.body).id;

  const deadline = Date.now() + ITERATION_DEADLINE * 1000;
  let userTaskCompleted = false;
  let terminal = false;

  while (Date.now() < deadline) {
    exec.vu.tags['phase'] = currentPhase(testStartMs);

    const pollRes = pollInstance(baseUrl, instanceId);
    if (!check(pollRes, { 'workflow.poll: 200': (r) => r.status === 200 })) {
      recordFailure(pollRes, false);
      sleep(POLL_INTERVAL_MS / 1000);
      continue;
    }

    const body = JSON.parse(pollRes.body);
    const status = body.status;
    const currentSteps = body.currentStepIds || [];

    if (isTerminal(status)) {
      terminal = true;
      if (status === 'COMPLETED') appCompleted.add(1);
      break;
    }

    if (!userTaskCompleted && currentSteps.indexOf(APP_USER_TASK_ID) !== -1) {
      const completeRes = completeUserTask(
        baseUrl,
        instanceId,
        APP_USER_TASK_ID,
        { reviewDecision: 'APPROVED' },
      );
      if (check(completeRes, { 'workflow.task_complete: 200': (r) => r.status === 200 })) {
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
    console.warn(`[perf] loan-chain: app ${instanceId} did not reach terminal in ${ITERATION_DEADLINE}s`);
  }
}

// --- senior_task_completer: side scenario ---
// Continuously drains pending senior-approval-task user tasks during the
// load phase. Without this, disbursement instances would pile up in WAITING.
export function completeSeniorTasks(data) {
  const { disbDefinitionId, baseUrl, testStartMs } = data;
  exec.vu.tags['phase'] = currentPhase(testStartMs);
  exec.vu.tags['scenario_role'] = 'senior_completer';

  const found = findAndCompleteSeniorTasks(baseUrl, disbDefinitionId, /*pageLimit*/ 10);
  if (found === 0) {
    sleep(0.5);  // back off when there's nothing to do
  }
}

// Walks pages of WAITING disbursement instances, completes any whose current
// step is senior-approval-task. Returns the number of tasks completed.
function findAndCompleteSeniorTasks(baseUrl, disbDefinitionId, pageLimit) {
  let completed = 0;
  for (let page = 0; page < pageLimit; page++) {
    const listRes = listInstances(baseUrl, {
      definitionId: disbDefinitionId,
      status: 'WAITING',
      page,
      pageSize: 100,
    });
    if (!check(listRes, { 'workflow.list: 200': (r) => r.status === 200 })) {
      recordFailure(listRes, false);
      return completed;
    }
    const body = JSON.parse(listRes.body);
    const items = body.items || [];
    if (items.length === 0) return completed;

    for (let i = 0; i < items.length; i++) {
      const inst = items[i];
      const steps = inst.currentStepIds || [];
      if (steps.indexOf(DISB_USER_TASK_ID) === -1) continue;

      const completeRes = completeUserTask(
        baseUrl,
        inst.id,
        DISB_USER_TASK_ID,
        { seniorDecision: 'APPROVED' },
      );
      if (check(completeRes, { 'workflow.task_complete: 200': (r) => r.status === 200 })) {
        completed++;
        userTasksDone.add(1);
      } else {
        recordFailure(completeRes, false);
      }
    }

    if (items.length < 100) return completed;  // last page
  }
  return completed;
}

// --- Teardown: drain stragglers, settle, then count non-terminal ---
export function teardown(data) {
  const { appDefinitionId, disbDefinitionId, baseUrl } = data;

  console.log('[perf] loan-chain teardown: draining remaining senior-approval-tasks...');
  for (let i = 0; i < TEARDOWN_DRAIN_PASSES; i++) {
    const n = findAndCompleteSeniorTasks(baseUrl, disbDefinitionId, /*pageLimit*/ 20);
    if (n === 0) break;
    sleep(1);
  }

  console.log(`[perf] loan-chain teardown: sleeping ${TEARDOWN_SETTLE_SEC}s before final non-terminal count...`);
  sleep(TEARDOWN_SETTLE_SEC);

  appUnfinished.add(countNonTerminal(baseUrl, appDefinitionId, 'app'));
  disbUnfinished.add(countNonTerminal(baseUrl, disbDefinitionId, 'disb'));
}

function countNonTerminal(baseUrl, definitionId, label) {
  let nonTerminal = 0;
  const pageSize = 200;
  for (const status of ['ACTIVE', 'WAITING']) {
    let page = 0;
    while (true) {
      const res = listInstances(baseUrl, { definitionId, status, page, pageSize });
      if (res.status !== 200) break;
      const body = JSON.parse(res.body);
      const items = body.items || [];
      nonTerminal += items.length;
      if (items.length < pageSize) break;
      page++;
    }
  }
  console.log(`[perf] loan-chain teardown: ${label} non-terminal count = ${nonTerminal}`);
  return nonTerminal;
}
