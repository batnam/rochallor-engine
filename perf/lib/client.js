// k6 REST client for the rochallor-engine perf load test.
//
// Each helper sets the `operation` tag for thresholds and reports, and a stable
// `name` so workflow IDs and query strings cannot create unbounded time series.
//
// Engine surface contracted in:
//   specs/003-performance-load-testing/contracts/engine-endpoints.md
//
// Status enum (per engine OpenAPI):
//   ACTIVE | WAITING | COMPLETED | FAILED | CANCELLED
// Terminal:    COMPLETED, FAILED, CANCELLED
// Non-terminal: ACTIVE, WAITING

import http from 'k6/http';

const TERMINAL_STATUSES = ['COMPLETED', 'FAILED', 'CANCELLED'];
const NON_TERMINAL_STATUSES = ['ACTIVE', 'WAITING'];
const KNOWN_STATUSES = TERMINAL_STATUSES.concat(NON_TERMINAL_STATUSES);

export function isTerminal(status) {
  return TERMINAL_STATUSES.indexOf(status) !== -1;
}

export function isKnownStatus(status) {
  return KNOWN_STATUSES.indexOf(status) !== -1;
}

function jsonHeaders() {
  return { 'Content-Type': 'application/json', 'Accept': 'application/json' };
}

// POST /v1/definitions — application/json. Returns WorkflowDefinitionSummary.
// Used in setup only; not measured.
export function uploadDefinition(baseUrl, definition) {
  return http.post(
    `${baseUrl}/v1/definitions`,
    JSON.stringify(definition),
    {
      headers: jsonHeaders(),
      tags: { operation: 'definition.upload', name: 'definition.upload' },
    },
  );
}

// POST /v1/instances — measured as `workflow.start`.
export function startWorkflow(baseUrl, definitionId, businessKey, variables) {
  const body = {
    definitionId: definitionId,
    businessKey: businessKey || undefined,
    variables: variables || {},
  };
  return http.post(
    `${baseUrl}/v1/instances`,
    JSON.stringify(body),
    {
      headers: jsonHeaders(),
      tags: { operation: 'workflow.start', name: 'workflow.start' },
    },
  );
}

// GET /v1/instances/{id} — measured as `workflow.poll`.
export function pollInstance(baseUrl, instanceId) {
  return http.get(
    `${baseUrl}/v1/instances/${instanceId}`,
    {
      headers: { 'Accept': 'application/json' },
      tags: { operation: 'workflow.poll', name: 'workflow.poll' },
    },
  );
}

// GET /v1/instances/{id}/history — measured as `workflow.history`.
export function getHistory(baseUrl, instanceId) {
  return http.get(
    `${baseUrl}/v1/instances/${instanceId}/history`,
    {
      headers: { 'Accept': 'application/json' },
      tags: { operation: 'workflow.history', name: 'workflow.history' },
    },
  );
}

// GET /v1/instances?... — measured as `workflow.list`.
export function listInstances(baseUrl, opts) {
  const params = [];
  if (opts && opts.page !== undefined) params.push(`page=${opts.page}`);
  if (opts && opts.pageSize !== undefined) params.push(`pageSize=${opts.pageSize}`);
  if (opts && opts.status) params.push(`status=${encodeURIComponent(opts.status)}`);
  if (opts && opts.definitionId) params.push(`definitionId=${encodeURIComponent(opts.definitionId)}`);
  const query = params.length ? `?${params.join('&')}` : '';
  return http.get(
    `${baseUrl}/v1/instances${query}`,
    {
      headers: { 'Accept': 'application/json' },
      tags: { operation: 'workflow.list', name: 'workflow.list' },
    },
  );
}

// POST /v1/instances/{id}/cancel — measured as `workflow.cancel`.
export function cancelInstance(baseUrl, instanceId, reason) {
  const body = reason ? JSON.stringify({ reason }) : '{}';
  return http.post(
    `${baseUrl}/v1/instances/${instanceId}/cancel`,
    body,
    { headers: jsonHeaders(), tags: { operation: 'workflow.cancel', name: 'workflow.cancel' } },
  );
}

// POST /v1/instances/{instanceId}/signals/{waitStepId} — measured as `workflow.signal`.
export function signalWaitStep(baseUrl, instanceId, waitStepId, vars) {
  return http.post(
    `${baseUrl}/v1/instances/${instanceId}/signals/${waitStepId}`,
    vars ? JSON.stringify(vars) : '{}',
    { headers: jsonHeaders(), tags: { operation: 'workflow.signal', name: 'workflow.signal' } },
  );
}

// POST /v1/instances/{instanceId}/user-tasks/{userTaskStepId}/complete — measured as `workflow.task_complete`.
export function completeUserTask(baseUrl, instanceId, userTaskStepId, variables) {
  const body = { variables: variables || {} };
  return http.post(
    `${baseUrl}/v1/instances/${instanceId}/user-tasks/${userTaskStepId}/complete`,
    JSON.stringify(body),
    { headers: jsonHeaders(), tags: { operation: 'workflow.task_complete', name: 'workflow.task_complete' } },
  );
}

export const STATUS = {
  TERMINAL: TERMINAL_STATUSES,
  NON_TERMINAL: NON_TERMINAL_STATUSES,
  ALL: KNOWN_STATUSES,
};
