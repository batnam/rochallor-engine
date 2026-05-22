// Strict pass/fail thresholds for the rochallor-engine perf load test.
//
// These are the default thresholds (thresholdsLineage = "default").
// Overrides can be supplied via --threshold-overrides.
//
// Threshold decisions: specs/003-performance-load-testing/research.md
// Contract:           specs/003-performance-load-testing/data-model.md §ThresholdSet

// If PERF_THRESHOLD_OVERRIDES_JSON is set, the engineer supplied an override JSON
// object (via --threshold-overrides in run.sh). We merge it over the defaults so
// that override runs are structurally valid but clearly marked "overridden" in reports.
const _overridesJson = (typeof __ENV !== 'undefined' && __ENV.PERF_THRESHOLD_OVERRIDES_JSON) || null;
const _overrides = _overridesJson ? JSON.parse(_overridesJson) : {};

const _defaults = {
  // Error rate < 0.1% per operation (rate<0.001 = fraction)
  'http_req_failed{operation:workflow.start}':    ['rate<0.001'],
  'http_req_failed{operation:workflow.poll}':     ['rate<0.001'],
  'http_req_failed{operation:workflow.list}':     ['rate<0.001'],
  'http_req_failed{operation:workflow.history}':  ['rate<0.001'],

  // p95 latency per operation
  'http_req_duration{operation:workflow.start}':   ['p(95)<1000'],  // 1 s
  'http_req_duration{operation:workflow.poll}':    ['p(95)<500'],   // 500 ms
  'http_req_duration{operation:workflow.list}':    ['p(95)<500'],
  'http_req_duration{operation:workflow.history}': ['p(95)<500'],

  // Zero workflows still non-terminal 10 s after steady-state ends
  'workflows_unfinished_after_steady_state': ['count==0'],

  // Error rate < 0.1% for new operations
  'http_req_failed{operation:workflow.cancel}':        ['rate<0.001'],
  'http_req_failed{operation:workflow.signal}':         ['rate<0.001'],
  'http_req_failed{operation:workflow.task_complete}':  ['rate<0.001'],

  // p95 latency for new operations (500 ms)
  'http_req_duration{operation:workflow.cancel}':        ['p(95)<500'],
  'http_req_duration{operation:workflow.signal}':        ['p(95)<500'],
  'http_req_duration{operation:workflow.task_complete}': ['p(95)<500'],

};

export const THRESHOLDS = Object.assign({}, _defaults, _overrides);

// Additional thresholds for the boundary-events scenario only.
// Merged in by boundary-events.js because the Counter is only defined there.
export const BOUNDARY_THRESHOLDS = {
  'boundary_events_fired': ['count>0'],
};

// Human-readable descriptions used in summary.md breach reporting.
export const THRESHOLD_DESCRIPTIONS = {
  'http_req_failed{operation:workflow.start}':    'workflow.start error rate < 0.1%',
  'http_req_failed{operation:workflow.poll}':     'workflow.poll error rate < 0.1%',
  'http_req_failed{operation:workflow.list}':     'workflow.list error rate < 0.1%',
  'http_req_failed{operation:workflow.history}':  'workflow.history error rate < 0.1%',
  'http_req_duration{operation:workflow.start}':  'workflow.start p95 < 1000 ms',
  'http_req_duration{operation:workflow.poll}':   'workflow.poll p95 < 500 ms',
  'http_req_duration{operation:workflow.list}':   'workflow.list p95 < 500 ms',
  'http_req_duration{operation:workflow.history}':'workflow.history p95 < 500 ms',
  'workflows_unfinished_after_steady_state':       'zero unfinished workflows 10 s after steady-state',
  'http_req_failed{operation:workflow.cancel}':        'workflow.cancel error rate < 0.1%',
  'http_req_failed{operation:workflow.signal}':         'workflow.signal error rate < 0.1%',
  'http_req_failed{operation:workflow.task_complete}':  'workflow.task_complete error rate < 0.1%',
  'http_req_duration{operation:workflow.cancel}':       'workflow.cancel p95 < 500 ms',
  'http_req_duration{operation:workflow.signal}':       'workflow.signal p95 < 500 ms',
  'http_req_duration{operation:workflow.task_complete}':'workflow.task_complete p95 < 500 ms',
  'boundary_events_fired':                              'at least one boundary event fired',
};

// Version of these defaults — captured in summary.json so historical runs
// can still be interpreted even if thresholds are later tightened.
export const THRESHOLDS_VERSION = '1.0';

// gRPC thresholds — used by scenarios/thousand-concurrent-grpc.js.
// Same latency bars as REST; error rate uses custom Rate metric (no built-in grpc_req_failed).
export const GRPC_THRESHOLDS = {
  'grpc_req_duration{operation:workflow.start}':   ['p(95)<1000'],
  'grpc_req_duration{operation:workflow.poll}':    ['p(95)<500'],
  'grpc_req_duration{operation:workflow.history}': ['p(95)<500'],
  'grpc_req_duration{operation:workflow.list}':    ['p(95)<500'],
  'grpc_error_rate{operation:workflow.start}':     ['rate<0.001'],
  'grpc_error_rate{operation:workflow.poll}':      ['rate<0.001'],
  'grpc_error_rate{operation:workflow.history}':   ['rate<0.001'],
  'grpc_error_rate{operation:workflow.list}':      ['rate<0.001'],
  'workflows_unfinished_after_steady_state':       ['count==0'],
};
