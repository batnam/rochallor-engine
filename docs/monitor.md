# Rochallor Monitor

Rochallor Monitor is a read-only web application for viewing workflow
executions, failures, and variables.

Monitor reads the Rochallor Engine database directly. It only performs read
operations and does not call the Workflow Engine API.

Monitor runs as an independent deployment. It can keep working when the
Workflow Engine process is unavailable, as long as PostgreSQL is available and
the database schema is compatible.

## Quick start

The Monitor quick-start stack contains two services:

- `frontend`: the React application served by Nginx.
- `bff`: the read-only API that queries the Engine database.

It does not start PostgreSQL, run migrations, or start the Workflow Engine.

### Prerequisites

You need:

- Docker with Docker Compose v2.
- A local clone of this repository.
- A migrated Rochallor Engine PostgreSQL database.

The default commands expect the Engine quick-start database on host port
`5434`.

### 1. Start the Engine quick-start stack

From the repository root, run:

```bash
docker compose -f deploy/docker-compose.quickstart.yml up -d
```

This starts PostgreSQL, the Workflow Engine, and Workflow Modeller. PostgreSQL
is available on host port `5434`.

### 2. Start Rochallor Monitor

Run:

```bash
docker compose -f deploy/docker-compose.monitor.quickstart.yml up -d
```

The default Monitor configuration connects to the Engine database at
`host.docker.internal:5434`.

The default local credentials are for quick-start use only. Use a dedicated
read-only database role in production.

### 3. Check the containers

Run:

```bash
docker compose -f deploy/docker-compose.monitor.quickstart.yml ps
```

The frontend starts after the BFF connects to PostgreSQL successfully.

If the BFF is not healthy, view its logs:

```bash
docker compose -f deploy/docker-compose.monitor.quickstart.yml logs bff
```

### 4. Open Monitor

Open [http://localhost:13001](http://localhost:13001).

![Rochallor Monitor showing Process Instance filters and execution statuses](assets/rochallor-monitor.png)

The left sidebar changes the Monitor view. The filter card narrows the results.
The main card shows Process Instances and their current statuses.

A new database may show an empty list. Create and run a workflow through the
Engine first. See [Getting Started](getting-started.md#5-upload-a-workflow-definition).

### Connect to a different database

Set `MONITOR_POSTGRES_DSN` when the Engine database is not using the default
local address:

```bash
export MONITOR_POSTGRES_DSN="postgres://workflow_monitor:replace-me@db.example:5432/workflow?sslmode=require"
docker compose -f deploy/docker-compose.monitor.quickstart.yml up -d
```

The hostname in the DSN must be reachable from the BFF container.

To use another public port, set `MONITOR_PORT`:

```bash
MONITOR_PORT=14001 \
  docker compose -f deploy/docker-compose.monitor.quickstart.yml up -d
```

Then open `http://localhost:14001`.

## How to use Monitor

Monitor is for observation only. It cannot retry, cancel, repair, or change a
workflow.

### Find Process Instances

Select **Process Instances** in the sidebar.

Use the filters to search by Workflow Definition, status, Business Key, or
start time. Select **Apply Filters** to update the list.

For a known ID, paste it into **Instance ID** and select **Open by Instance ID**.
Surrounding whitespace is removed; other characters are preserved. An unknown
ID shows a not-found page. Browser Back and Forward restore the prior view.

**Definition Version** narrows a selected definition. **Current Step ID** also
requires a definition and matches only `ACTIVE`/`WAITING` instances currently at
that step. **Step Started Before (UTC)** requires a current step and matches
its latest `RUNNING` execution with a start time strictly before that cutoff.
These fields are also populated by Overview drill-down links.

Use **Newest**, **Previous**, and **Next** to move through pages. Select a
Process Instance ID to open its details.

The list includes Business Key, start time, and elapsed time. For running
instances, elapsed time is measured at the last successful list update, so it
does not advance as if fresh while the database is unavailable. For finished
instances it uses the recorded completion time. Start-time inputs use UTC,
with an inclusive **From** and exclusive **To** bound. Invalid ranges are
explained beside the editable filters.

The status badges use these states:

- `ACTIVE`: the workflow is running.
- `WAITING`: the workflow is waiting for an external action or event.
- `COMPLETED`: the workflow finished successfully.
- `FAILED`: the workflow stopped because of a failure.
- `CANCELLED`: the workflow was cancelled.

### Save frequently used filters

Process Instances and Incidents each have **Saved filters**. Apply the desired
search first, enter a **Filter name**, then select **Save applied filters**.
Choose a saved filter to apply, rename, or delete it. Applying starts at the
first page. Time bounds remain absolute UTC values, including an Overview age
cutoff; they do not become rolling windows.

Preferences are kept in this browser's local storage, independently for each
list. They survive reloads but are not shared between browsers. Only IDs,
names, and supported filter fields are stored; cursors and result data are not.
The limits per list are 20 filters, 80 characters per name, 4,096 characters per
filter query, and a 64 KiB stored document. Names must be unique ignoring case.
Invalid versions, unsupported fields, malformed data, and unavailable storage
show an error without disabling ordinary searches. **Clear saved filters**
resets preferences for that list.

### Find work through Overview

Select **Overview** in the sidebar. **Workflows by version** counts `ACTIVE`,
`WAITING`, and `FAILED` instances across all retained records, including old
instances. A version with only completed or cancelled instances has zero in
these columns. A definition without any retained instance does not appear.
This view does not calculate completion rates or historical trends.

Use **Search workflows** to find a workflow by any part of its name or ID,
ignoring case and surrounding whitespace. Press Enter or **Search** to apply
the filter across all workflow pages. Each version is matched against its own
name. **Clear search** restores all workflows. Searches are kept in the URL
for reload and browser Back/Forward; changing the search resets pagination
and the selected workflow's current-step panel.

Select **Inspect** for a version to see its current steps. Each instance counts
once per step; a parallel instance may appear at several steps, so adding the
step counts does not give a distinct instance total. Terminal instances are
excluded. The age threshold defaults to 1,800 seconds (30 minutes) and is
editable. It measures the latest running execution's start time. Missing or
non-running execution evidence appears in **Age unavailable**. Exceeding this
threshold is an investigation aid, not proof that a workflow is stuck or an SLA
has been breached.

Select a count to open the matching Process Instances search. Age links carry
the exact absolute cutoff observed by the summary. The destination performs a
fresh read, so data may have changed since that observation. Both tables are
paginated, refresh every 15 seconds while visible, and retain stale results
with a warning after a refresh failure. A database error is never shown as a
healthy zero count.

### Inspect a Process Instance

The detail page shows the status, Workflow Definition, and Business Key.

![Rochallor Monitor showing a Process Instance execution diagram](assets/rochallor-monitor-detail.png)

The **Overview** tab contains the Execution Diagram, current execution context,
and Step Executions history.
The diagram shows the path through the Workflow Definition.

Select a step in the diagram to highlight its executions. The table shows each
attempt, its status, start and end times, and snapshot availability.

**Current execution context** explains each current step independently for
`ACTIVE` and `WAITING` instances. Selecting a diagram step narrows this section.
It shows a signal wait, open user-task assignment, an available service job, or
the recorded worker and lock for a locked job. Task assignee/group values are
recorded task data. Pending boundary timers show their deadline, target, and
interrupting flag, with at most 100 timers per current execution.

Ages and expired deadlines use the displayed database observation time. An
expired lock does not prove that a worker is dead. Job deliveries and retries
remaining are separate from step attempt numbers; there is no retry ETA or
exact job-duration history. Missing/conflicting records and unsupported step
types show an unavailable reason. A completed join execution can still be a
current token position; Monitor does not infer its gateway wait condition.
Terminal instances have no active execution context.

Switch history from **Table** to **Timeline** for chronological step attempts
and overlapping durations. Select **Inspect attempt** to highlight the step
and load that attempt's snapshots. Running durations use the last successful
history update; an unrecorded terminal end time has no inferred duration. When
history is paginated, the timeline explicitly represents only the loaded page.
Use the existing previous/next/newest history controls to inspect other pages.
Gaps between records do not establish queue time or a wait reason.

Use **View Incident** on a failed execution, or the Incident link for the
selected failed step, to open its Error Details. Cancelled instances do not
offer Incident links. Diagram code loads only when a detail page is opened.

The **Variables** tab shows the current Process Variables. It also lists input
and output Variable Snapshots recorded at Step Execution boundaries.

Variable Snapshots are not a complete history of every variable change.

Snapshot meaning also depends on the step type. A Service Task output records
the variables returned by the worker, which can be only a partial update.
Current User Task, Wait, and Transformation completion paths record the merged
variable state. Do not assume input and output are always complete before/after
states, or that every difference was caused by the selected step when branches
run concurrently.

Expanding snapshots also opens a comparison of top-level keys. **Returned-variable
changes** compares only keys returned by a Service Task. For input
`{"a":1,"b":2}` and output `{"a":3}`, it shows `a: 1 → 3`; it does not mark `b`
as removed. **Recorded document differences** compares supported full-state
outputs, with a caveat about concurrent branches. Unknown semantics are labelled
as raw document comparison. Missing keys, JSON null, false, zero, types, arrays,
and nested objects remain distinct; raw snapshot views remain available.

Comparison requires both snapshots to be recorded and within the BFF document
limit. Processing is additionally bounded to 256 KiB combined, 10,000 values,
64 nesting levels, and the first 100 changed top-level keys. Larger comparisons
explain the limit instead of blocking the page. An expanded running attempt
reloads when history reports a changed status or snapshot availability; a failed
reload retains its last data and offers **Retry snapshots**.

### Investigate Incidents

Select **Incidents** in the sidebar.

Filter Incidents by Workflow Definition, job type, or occurrence time. Select
an Incident ID to open its details.

The detail page shows the failed step, attempt, time, job context, and Error
Details. Use the Process Instance link to open the related execution.

An Incident represents a failed Step Execution, not a separately tracked
open/resolved issue. A failure can remain in this history after a later attempt
succeeds. Incidents from cancelled instances are excluded. **Historical failure**
means a later step attempt exists; **Latest failed attempt** means this remains
the latest attempt. The latest attempt's status is shown with a link to the
instance and step. A later successful attempt does not establish a stored
resolution time or prove that the entire instance is healthy.

### Refresh and stale data

Monitor refreshes Process Instance and Incident lists, and Incident detail,
every five seconds while the browser tab is visible. Overview uses 15 seconds.
Invalid filter requests do not poll until corrected.

On a Process Instance detail page, **Refresh** updates status, diagram, and
execution context,
then the current history page and Current Variables if the Variables tab is
open. The same cycle runs every five seconds for `ACTIVE` or `WAITING`
instances, pauses in hidden tabs, and refreshes on return or reconnection.
Cycles do not overlap. After observing a terminal status, history is refreshed
once more before polling stops. A failed part of the cycle keeps polling for
recovery, including after a terminal transition. Refresh keeps the selected
step and history page; use **Newest Step Execution page** to see new attempts
while browsing older history.

Each section shows its last successful update in UTC and the age of that
result. If a refresh fails, the last successful result stays visible with a
warning for that section. A section with no cached result shows a loading or
error state instead. One section may succeed while another remains stale.

The detail API reads status, definition, overlay, and current execution context
in one consistent database snapshot. History and variables are separate API reads; a refresh cycle does
not promise a single snapshot across the whole page.

Cached results live in the browser's in-memory TanStack Query cache. Reloading
or closing the page clears them. A BFF restart does not erase data already
displayed in an open browser. There is no BFF result cache or offline result
storage. Saved filter preferences persist separately in local storage.

## Manage the quick-start stack

### Update Monitor

Pull the latest images and recreate changed containers:

```bash
docker compose -f deploy/docker-compose.monitor.quickstart.yml pull
docker compose -f deploy/docker-compose.monitor.quickstart.yml up -d
```

### Stop Monitor

Run:

```bash
docker compose -f deploy/docker-compose.monitor.quickstart.yml down
```

This stops only Monitor. It does not stop the Engine quick-start stack or
delete the Engine database.

## Troubleshooting

### The BFF remains unhealthy

Check that PostgreSQL is running, the DSN is correct, and Engine migrations
have been applied.

```bash
docker compose -f deploy/docker-compose.quickstart.yml ps
docker compose -f deploy/docker-compose.monitor.quickstart.yml logs bff
```

The default DSN uses `host.docker.internal:5434`. The Monitor Compose file adds
the required host mapping on Linux.

### Port 13001 is already in use

Choose another port:

```bash
MONITOR_PORT=14001 \
  docker compose -f deploy/docker-compose.monitor.quickstart.yml up -d
```

### Monitor opens but shows no data

An empty database has no Process Instances or Incidents to display. Upload a
Workflow Definition and start an instance through the Engine.

If data existed before, check the BFF logs for a database or schema error.

### Monitor shows stale data

The browser cannot refresh its cached result. Check PostgreSQL availability,
the BFF logs, and database timeout settings. Monitor will refresh again when
the database is available, or you can use **Refresh** immediately.

## Production deployment

The browser uses the same-origin `/api` path. Nginx proxies that path to the
BFF, so the BFF does not need a public port.

```text
Browser ── /api ──> Nginx ──> Monitor BFF ── SELECT ──> PostgreSQL
                              (no Engine API dependency)
```

### Use a read-only database role

Use a dedicated role instead of the Engine's write credentials. Replace the
database, schema, and password for your environment.

```sql
CREATE ROLE workflow_monitor LOGIN PASSWORD 'replace-me';
GRANT CONNECT ON DATABASE workflow TO workflow_monitor;
GRANT USAGE ON SCHEMA public TO workflow_monitor;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO workflow_monitor;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT ON TABLES TO workflow_monitor;
```

Run `ALTER DEFAULT PRIVILEGES` as the role that owns and creates the Engine
tables.

Do not grant create, insert, update, delete, truncate, trigger, or migration
permissions.

### Access and transport boundary

Process Variables, Variable Snapshots, and Error Details may contain sensitive
business data.

Authentication and TLS termination are exclusively external infrastructure
responsibilities. To keep this project simple, do not implement authentication
features, authentication middleware, or TLS/authentication deployment templates
in this repository. Monitor contains no login, session, token, Basic auth, or
SSO implementation. Deployment operators manage access and transport outside
the project.

### Bound database resources

The BFF uses its own pool and identifies sessions as `rochallor-monitor`.
These positive-integer environment settings also work in the quick-start Compose file:

| Setting | Default | Meaning |
| --- | ---: | --- |
| `MONITOR_POSTGRES_POOL_MAX` | 5 | Maximum connections per BFF process |
| `MONITOR_POSTGRES_CONNECTION_TIMEOUT_MS` | 2000 | Maximum wait for connection establishment or a free pool slot |
| `MONITOR_POSTGRES_STATEMENT_TIMEOUT_MS` | 3000 | Server-side timeout for each SQL statement |

Budget connections across **all** BFF replicas: three replicas at the default
can open up to 15 connections. Increasing browser sessions adds requests to
these pools; it does not create a pool per browser. Query timeouts cancel SQL
on PostgreSQL, and failed detail transactions roll back before returning a
connection. These are per-statement limits, not an end-to-end request deadline.
DSN/session overrides must agree with your resource policy.

The query-plan tests run the production list queries on 100,000-row fixtures,
enforce latency/scan/buffer budgets, and write full plans to
`workflow-monitor-bff/testresults/query-plans/`. CI archives these results.
See the [instance baseline](performance/process-instance-list-query-plan.md)
and [Incident baseline](performance/incident-list-query-plan.md) for measured
results, thresholds, and index evaluation. The
[diagnostic query baseline](performance/monitor-diagnostics-query-plan.md)
covers execution context, Overview aggregates, and step-age drill-down.

### Keep schemas compatible

Monitor reads Engine tables directly and does not run migrations. Deploy Engine
migrations before the matching Monitor release.

| Monitor revision | Engine schema | Verification |
| --- | --- | --- |
| Current source (frontend/BFF package manifests: `0.1.0`) | Engine migrations `0001` through `0011` in this repository, PostgreSQL 16 | Automated API and schema compatibility tests |
| Separately released or older images | Only the explicitly tested Engine/Monitor pair | Do not infer compatibility from `latest` or matching package numbers |

The BFF checks required columns, PostgreSQL types, and read permissions on
`workflow_instance`, `workflow_definition`, `step_execution`, `job`, `user_task`,
and `boundary_event_schedule` during startup and every readiness request. The
context checks include job worker/lock/retry fields, task assignment, and timer
boolean/deadline fields. Checks use `LIMIT 0`; they do not scan
workflow data. Extra columns are allowed. Startup fails with a table-specific
diagnostic on an incompatible schema, before the HTTP listener opens.

Route production traffic using `/health/ready`, which returns 503 after a
schema or database availability failure; `/health/live` only reports process
liveness. Compose's startup health dependency is not ongoing traffic routing.
The schema check does not validate every stored definition JSON document or
the meaning of status values. Test Monitor against staging whenever a migration
changes columns, types, or Engine semantics, and record the tested release pair.

## Limitations

- Authentication, authorization, TLS termination, and ingress policy are
  deployment responsibilities.
- Monitor can observe workflows but cannot change them.
- Direct database reads require compatible Engine and Monitor releases.
- Cached stale data is held in browser memory and is lost after a page reload.
- A coordinated refresh does not provide one database snapshot across APIs.
- Database limits apply per BFF process and per SQL statement.
- Large Incident history may require database performance tuning.
- Overview aggregates scan retained data; pagination limits the response, not
  all database work.
- Timeline and boundary snapshots are not a complete event or variable audit log.
- Arbitrary business-variable and historical-snapshot searches are unavailable.

## Feature roadmap

The four first deliveries are available:

1. Current waits and execution context, including later attempts after
   a failure.
2. Overview by workflow and step, with counts and links to matching
   instances.
3. Execution timeline and comparison of recorded input/output snapshots.
4. Direct Instance ID lookup and named saved filters.

Selected business-variable searches remain deferred until concrete fields are
identified and query cost is measured.

See the [feature roadmap and implementation plan](monitor-roadmap.md) for
scope, dependencies, implementation tasks, acceptance criteria, and the
documentation updates required for each delivery.
