# Monitor feature roadmap and implementation plan

Recorded: 2026-09-25. Baseline: `9507567`.

**Status: P1–P4 first deliveries implemented and verified locally.** This
document records the four feature groups selected after comparing Monitor's
operational features with Camunda Cockpit. The [operator guide](monitor.md)
describes the delivered behavior. P4.5–P4.7 remain deferred pending concrete
business-variable search fields and query measurements.

Completion is in the current working tree based on `9507567`; no completion
commit has been created. Changes are confined to Monitor frontend, Monitor BFF,
their tests/READMEs, and documentation/navigation. Engine code, schema, APIs,
SDKs, workers, Modeller, and deployment configurations are unchanged. No
authentication or TLS implementation was added.

## Priorities and scope

| Priority | Feature group | Operator outcome | Delivery |
| --- | --- | --- | --- |
| P1 | Explain current waits and execution context | Understand what each current step needs before it can progress, and distinguish earlier failures from the latest attempt | First |
| P2 | Overview by workflow and step | Find workflows with failed instances and steps with many or long-running instances, then open the matching list | After P1 semantics are established |
| P3 | Execution timeline and snapshot comparison | Follow attempts over time and inspect differences between recorded input and output | After P2 |
| P4 | Easier lookup and reusable filters | Open an instance by ID and reuse named searches; consider selected business-variable searches later | After P3; variable search is a conditional follow-up |

All four groups retain the read-only boundary: no retry, cancel, signal, task
completion, variable editing, migration, or batch-control actions. Monitor
continues to read PostgreSQL without requiring the Engine process or API.

**Do not implement authentication anywhere in this project.** Do not add login,
sessions, tokens, Basic authentication, SSO, authentication middleware, or TLS
and authentication deployment templates. Existing database access and the
read-only database role remain the infrastructure boundary.

The first deliveries use existing Engine records. Do not introduce an event
store, worker heartbeat service, metrics pipeline, new snapshot format, or
Engine state transitions as part of this plan. Any additional Engine data
needed later must be identified explicitly instead of inferred by Monitor.

## Facts that constrain the design

| Existing evidence | Consequence for implementation |
| --- | --- |
| `workflow_instance.current_step_ids` can contain multiple steps; `recomputeInstanceStatus` marks an instance `WAITING` when a remaining step is `WAIT` or `USER_TASK` | Explain every current step for both `ACTIVE` and `WAITING`. A service job waiting for a worker does not necessarily make the instance `WAITING`. |
| `job` stores worker, lock times, retries remaining, and creation time. Replacement deliveries create new job rows for the same step execution | Distinguish job delivery attempts from `step_execution.attempt_number`. Do not equate their counts or imply all replacements consume a business retry. |
| There is no persisted next-retry timestamp, worker heartbeat, or job completion/failure timestamp in `job` | Do not display a retry ETA, declare a worker dead, or draw exact job-duration bars. An expired lock is an observation, not proof of a cause. |
| `user_task` stores task status and assignment fields; `boundary_event_schedule` stores scheduled time, target, interrupting flag, and fired flag | Read assignment as task data only. Show pending boundary timers for the relevant live execution, not all unfired historical rows. A boundary timer is not necessarily the primary wait reason. |
| Current Incident queries select failed step executions and exclude cancelled instances | An Incident is failure history, not a persisted open/resolved incident lifecycle. Later attempts can exist after that failure. |
| Step input is captured at entry. `SERVICE_TASK` output is the worker's variable delta; `USER_TASK`, `WAIT`, and `TRANSFORMATION` output contains the merged state on their current completion paths | Compare snapshots according to their meaning. A key missing from a service-task output is not a deletion. Concurrent branches prevent attributing every full-state difference to one step. |

Code references for these decisions:

- `workflow-engine/internal/instance/step_helpers.go` and `handlers.go`
- `workflow-engine/internal/instance/lifecycle.go`, `complete_user_task.go`, and `signal_wait.go`
- `workflow-engine/internal/job/complete.go` and `retry.go`
- `workflow-engine/migrations/0002_workflow_instance.up.sql` through `0006_boundary_event_schedule.up.sql`
- `workflow-monitor-bff/src/modules/incidents/incident.queries.ts`

## P1 — Explain current waits and execution context

### User-visible behavior

Add an execution-context section to the Process Instance detail page, linked
to the selected diagram step. Show all current steps when none is selected.
Keep the instance's stored status unchanged. Measure elapsed time and expired
deadlines at the response's database observation time, and retain that reference
when displaying stale results.

| Evidence for a current step | Display |
| --- | --- |
| Running `WAIT` execution | Waiting for a signal to this step; step entry time and elapsed time |
| Running `USER_TASK` with an open task | Waiting for task completion; task ID, recorded assignee/group, and elapsed time |
| Running `SERVICE_TASK` with an `UNLOCKED` job | Job available for acquisition; job type, ID, creation time, and retries remaining |
| Running `SERVICE_TASK` with a `LOCKED` job | Recorded worker, lock acquisition/expiry, and retries remaining; flag a lease already expired at observation time |
| Pending timer attached to that running execution | Scheduled time, target step, interrupting flag, and whether the scheduled time has passed |
| Missing, conflicting, or unsupported evidence | Show the recorded step state and say the wait reason is unavailable; do not invent a reason |

A join can remain in `current_step_ids` after its latest execution row has
completed. Do not automatically classify that row as running or compute a
precise join-wait duration. Display the recorded context without duplicating
the Engine's gateway evaluator.

For each failed execution, show its relationship to the latest attempt of the
same instance and step. Use factual labels such as **Latest failed attempt**
or **Historical failure**, with a link and status for the later attempt. A later
running attempt on an `ACTIVE` or `WAITING` instance means retry is in progress;
a later completed attempt records that success. Neither creates a stored
`resolvedAt` or proves the whole instance is healthy. Preserve existing Incident
URLs and cancelled-instance exclusion.

### Implementation tasks

- [x] P1.1 — Define additive DTOs for observation time, per-step context, job/task/timer evidence, and unavailable reasons. Use the pinned definition version and deterministic execution ordering already used by the overlay.
- [x] P1.2 — Extend the existing detail query's `REPEATABLE READ READ ONLY` transaction to include current execution context. Scope reads to the instance and relevant execution IDs; return current job context without loading all delivery history or payloads. Detect conflicting live rows rather than silently choosing a healthy-looking one.
- [x] P1.3 — Extend schema readiness checks for the job fields, `user_task`, and `boundary_event_schedule` actually read. Verify column types and read permissions, including PostgreSQL boolean fields. Keep migrations owned by Engine.
- [x] P1.4 — Add latest-attempt context to Incident list/detail responses with bounded, parameterized queries. Use the same ordering as the execution overlay. Update DTOs and generated OpenAPI documentation.
- [x] P1.5 — Render execution context and historical-failure labels in the existing detail and Incident views. Include context in the coordinated refresh cycle; preserve stale data, selection, update time, and section warnings. Terminal instances have no active wait context.
- [x] P1.6 — Add database/API, frontend, integration, and browser scenarios below; document interpretation and limits in the operator guide.

**Acceptance:** one instance with parallel `WAIT`, `USER_TASK`, and service-job
steps shows each reason independently. Fixtures cover unlocked/locked/expired
jobs, replacement delivery versus manual step retry, pending/fired/obsolete
timers, nullable assignment, missing related rows, joins, and terminal states.
An old failed attempt remains available after a successful retry and is clearly
historical. Connection loss retains the last result; recovery refreshes it.
Missing new schema fields or read grants fail startup/readiness clearly. All
queries pass using the existing read-only test role.

## P2 — Overview by workflow and step

### User-visible behavior and counting rules

Add an **Overview** navigation entry. Preserve Process Instances as the current
home page. Show current `ACTIVE`, `WAITING`, and `FAILED` instance counts grouped
by definition ID and version. Counts describe retained database records at the
observation time, including old instances; they are not a completion-rate or
historical trend report.

Selecting a definition/version shows its current steps, number of distinct
instances at each step, and number whose running execution exceeds the selected
age threshold. Start with a visible, editable 30-minute threshold, described as
an investigation aid rather than an SLA or proof of a stuck workflow.

Count each instance once per definition/status and once per current step.
Parallel instances can appear under multiple steps, so step counts must not be
summed into an instance total. Exclude terminal instances from current-step
counts. Measure execution age from the matched current `RUNNING` execution's
`started_at`; show unavailable age separately where evidence is insufficient.
Do not count every historical failed attempt as a currently failed instance.

### Implementation tasks

- [x] P2.1 — Add read-only `GET /api/v1/overview` for paginated definition/version counts and `GET /api/v1/overview/steps` scoped to a required definition/version. Return observation time and explicit counting scope; use stable group ordering and page limits of at most 100.
- [x] P2.2 — Add the list-filter support needed for drill-down: `definitionVersion`, `currentStepId`, and an absolute `stepStartedBefore` cutoff. Reuse existing status/definition filters. Include every new filter in cursor fingerprints, URL parsing, validation, and OpenAPI. Define combinations and reject invalid ones with a specific 400 response.
- [x] P2.3 — Add the Overview route and summary tables. Poll only while visible, initially every 15 seconds; support manual refresh and stale-result warnings. Reuse the current freshness presentation. Do not fetch diagram code or snapshots for this page.
- [x] P2.4 — Link counts to filtered Process Instances. Carry the same definition version, step, statuses, and absolute age cutoff into the URL. Explain that the destination is a fresh read and may change after the summary observation.
- [x] P2.5 — Benchmark production aggregate and drill-down SQL with at least the existing 100,000-instance fixture scale, representative parallel instances, and many attempts. Record latency/scan/buffer budgets and plans before merging. Assess indexes from those results; a page limit bounds the response, not aggregate scan cost.
- [x] P2.6 — Add tests for distinct counting, mixed versions, old active instances, historical failures, age boundaries, missing execution evidence, pagination, drill-down, and stale refresh. Update the operator guide and performance notes.

**Acceptance:** on a fixed fixture, every count opens exactly the matching set
without duplicate instances. Parallel branches and retry history do not inflate
definition counts. An instance started long ago is still counted if active.
Age-filtered results use the same cutoff as the selected summary. Empty results
show zero; database errors show an error/stale state, never a healthy zero.
Query plans pass documented budgets within existing pool and statement limits.

## P3 — Execution timeline and snapshot comparison

### User-visible behavior

Provide a chronological view of Step Executions with attempt number, status,
start/end time, and recorded duration. Represent overlapping executions without
implying serial execution. Clicking an attempt opens its existing snapshot and
Incident links and selects the corresponding diagram step.

The initial timeline reuses paginated step history. Clearly identify the loaded
window and offer previous/next navigation; it must not look like a complete
execution trace when only one page is loaded. A gap between records is not
automatically queue time or an external wait. Job delivery history and exact
job-duration bars are outside this first timeline.

Compare input and output for one selected execution on demand. Start with a
table of top-level keys and expandable JSON values, with type-aware equality.
For service-task deltas, compare only keys present in the returned output and
label the view as returned-variable changes. For supported full-state outputs,
compare the two recorded documents and label differences as observed changes,
not proof that the selected step caused every change. Preserve raw JSON views.

### Implementation tasks

- [x] P3.1 — Add the timeline presentation to the existing lazy-loaded detail page and history query. Use `(startedAt, id)` for deterministic display ordering, preserve page/step selection, and label running durations at the last successful observation.
- [x] P3.2 — Add explicit snapshot interpretation metadata to the snapshot API (`variableDelta`, `fullState`, or `unknown`) based on verified Engine write paths and execution type. Keep existing present/not-recorded/too-large states. Use raw comparison only, with a clear caveat, for unknown semantics.
- [x] P3.3 — Add on-demand comparison in `VariableSnapshotInspector.tsx`. Distinguish missing keys, JSON null, false, zero, type changes, nested objects, and arrays. Do not treat omitted delta keys as removed variables or reconstruct a global post-step state.
- [x] P3.4 — Keep comparison work bounded: fetch only the selected execution, retain existing JSON size limits, cap displayed changes, and report truncation. Choose and test a processing limit for deeply nested/large values so permitted documents cannot freeze the page.
- [x] P3.5 — Test parallel overlap, equal timestamps, repeated steps, pagination boundaries, null end times, refresh errors, delta/full-state semantics, concurrent variable changes, absent snapshots, and oversized content. Update snapshot and history documentation with examples.

**Acceptance:** an operator can distinguish two step attempts from multiple job
deliveries within one attempt. Timeline paging never silently omits the fact
that more history exists. Given input `{"a":1,"b":2}` and service output
`{"a":3}`, comparison shows `a: 1 → 3` and does not mark `b` as deleted. Missing
or oversized snapshots are unavailable comparisons, not empty objects. Tests
do not claim a complete variable audit log from boundary snapshots.

## P4 — Easier lookup and reusable filters

### First delivery

Add **Open by Instance ID** using the existing detail route/API. Treat IDs as
opaque values, trim surrounding whitespace, encode the URL segment, and show a
clear not-found result. Keep Business Key search available for business lookup.

Add named saved filters for Process Instances and Incidents. Store only a
versioned, validated filter definition and display name in browser local
storage. Do not store cursors, result rows, variables, snapshots, or errors.
Saved filters survive reload in that browser; they are not shared between
browsers and are independent of the in-memory query-result cache.

### Implementation tasks

- [x] P4.1 — Add direct ID navigation and tests for whitespace, encoded characters, unknown IDs, and browser back/forward navigation. Reuse the existing detail endpoint.
- [x] P4.2 — Add save/apply/rename/delete for named filters using the existing URL filter contract, including P2 drill-down fields. Applying a saved filter resets pagination. Initially save absolute UTC bounds exactly as entered; do not silently turn them into rolling windows.
- [x] P4.3 — Bound saved-filter count/name/document size; validate storage versions and fields before applying. Handle unavailable storage or malformed entries without breaking ordinary searches. State the chosen limits in tests and documentation.
- [x] P4.4 — Test reload persistence, name collisions, invalid/stale filters, storage errors, clear/delete, URL round trips, and result-cache independence. Update the operator guide and frontend README.

**Acceptance:** a pasted valid ID opens the intended instance. Named searches
restore the same filter values after reload and clear any old cursor. Failure
to read/write local storage does not prevent normal browsing. Documentation
clearly distinguishes persisted filter preferences from non-persisted results.

### Conditional follow-up: selected business-variable search

Keep this part recorded as **deferred pending concrete search fields and query
measurements**; it does not block P1–P4's first deliveries.

- [ ] P4.5 — Identify the business keys/variables operators actually search. Propose exact-match, explicitly typed top-level variable predicates first; do not add arbitrary expressions, JSONPath, full-text search, or a generic query builder.
- [ ] P4.6 — Specify missing-versus-null and string/number/boolean semantics, permitted predicate count, URL/cursor behavior, and search against current `workflow_instance.variables` only. Historical snapshot search remains out of scope.
- [ ] P4.7 — Measure representative queries and choose any Engine-owned index migration from evidence. Ship only with read-only API tests, pagination/filter tests, query-plan budgets, and operator examples for the selected fields.

## Delivery sequence and code ownership

Implement and validate each group before starting the next. Within each group,
land the DTO/query changes before the consuming UI; update documentation and
OpenAPI in the same delivery as the behavior they describe. P2's drill-down
filters are part of P2 and do not wait for P4's saved-filter UI.

| Delivery | Main code areas | Documentation updated when delivered |
| --- | --- | --- |
| P1 | BFF process-instances/incidents modules and database readiness; detail and Incident routes | `docs/monitor.md`: context, Incident meaning, refresh, required schema; BFF README for API changes |
| P2 | New BFF overview module; instance-list DTO/query/filter handling; new frontend Overview route and navigation | `docs/monitor.md`: counting scope, thresholds, drill-down; `docs/performance/`: aggregate/query baselines |
| P3 | Step-history view; process-variable DTO/query; snapshot inspector | `docs/monitor.md`: timeline window, delta/full-state comparison and limits; BFF README for API changes |
| P4 | Instance-list navigation, list filter handling, saved-filter UI/storage | `docs/monitor.md` and frontend README: lookup, saved filters, persistence and supported searches |

Index changes, if measurements justify them, belong in
`workflow-engine/migrations/` and must be reflected in Monitor compatibility
documentation. Monitor never creates indexes or performs migrations itself.

## Verification and completion policy

For each delivery, add focused behavioral tests to the existing suites rather
than starting a second test framework. Exercise actual new SQL against
PostgreSQL with the read-only role; use frontend tests for presentation and
state changes, and integration/browser scenarios for the main operator flow.

From the repository root, run the relevant package checks:

```bash
pnpm --dir workflow-monitor-bff lint
pnpm --dir workflow-monitor-bff typecheck
pnpm --dir workflow-monitor-bff test
pnpm --dir workflow-monitor-bff build
pnpm --dir workflow-monitor lint
pnpm --dir workflow-monitor typecheck
pnpm --dir workflow-monitor test
pnpm --dir workflow-monitor build
pnpm --dir workflow-monitor test:integration
pnpm --dir workflow-monitor test:e2e
mkdocs build --strict --site-dir /tmp/rochallor-monitor-docs-site
```

Database, integration, and browser suites require Docker, as described in the
package READMEs. New endpoints must be covered by schema-readiness, OpenAPI,
read-only-role, validation, and representative query-plan checks as applicable.
Preserve the existing bundle split and coordinated-refresh behavior.

A group is complete only when its acceptance scenarios pass, measured database
cost is recorded for new queries, the operator guide describes the delivered
behavior, and this roadmap records validation and the completion revision (or
explicitly states that the verified changes are not yet committed).

## Local completion record — 2026-09-25

| Delivery | Verification |
| --- | --- |
| P1 | PostgreSQL/API fixtures for parallel waits, service job replacement, expired locks, open tasks, pending/obsolete timers, missing/conflicting evidence, joins and terminal states; readiness types/grants; latest-attempt Incident metadata; coordinated refresh and stale context; browser parallel-context and historical-failure flows |
| P2 | Distinct status/version/step counts, old instances, retry history, unavailable/exclusive ages, scoped cursors, invalid/injection-like inputs, exact drill-down, 15-second visible polling and stale recovery; 100,000-instance query budgets; real BFF integration and browser drill-down |
| P3 | Timeline overlap/order/page boundaries and incomplete durations; delta/full-state/unknown comparisons, missing/null/types/nested values, processing/truncation limits; on-demand snapshots, running-to-completed output refresh and retry after failure; real BFF and browser timeline/comparison flows |
| P4 | ID trimming/encoding/not-found and browser Back/Forward; saved-filter reload/apply/rename/delete/clear, pagination reset, full URL fields, bounds, collisions, malformed versions/data, storage denial/quota, list isolation, and selected-filter restoration after list reload |

Validation: BFF 94 tests across 15 suites; frontend 84 tests across 12 files;
one React → BFF → PostgreSQL integration scenario with no Engine process; and
11 Chromium browser scenarios against built Nginx/BFF containers. Both packages
pass lint, typecheck, and production build. The documentation passes strict
MkDocs build. The browser outage scenario stops/restarts only its disposable
PostgreSQL fixture, verifies stale rows and subsequent recovery, and checks
that sensitive fixture contents are absent from BFF logs.

The browser run also captures Overview, current context, timeline comparison,
and saved-filter screenshots under `workflow-monitor/test-results/`; these
were visually inspected. Initial JavaScript is approximately 268.53 kB
(80.63 kB gzip). The existing ELK/detail chunk remains lazy-loaded at about
1,464.46 kB (446.98 kB gzip); Vite still reports its chunk-size warning.

See the [diagnostic query baseline](performance/monitor-diagnostics-query-plan.md)
and updated [Incident baseline](performance/incident-list-query-plan.md) for
measured latency/scan/buffer budgets. Query changes removed repeated scans;
no Engine-owned index migration was needed on these fixtures. These local
measurements do not establish production capacity under concurrent Engine load.
