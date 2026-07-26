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

Use **Newest**, **Previous**, and **Next** to move through pages. Select a
Process Instance ID to open its details.

The status badges use these states:

- `ACTIVE`: the workflow is running.
- `WAITING`: the workflow is waiting for an external action or event.
- `COMPLETED`: the workflow finished successfully.
- `FAILED`: the workflow stopped because of a failure.
- `CANCELLED`: the workflow was cancelled.

### Inspect a Process Instance

The detail page shows the status, Workflow Definition, and Business Key.

![Rochallor Monitor showing a Process Instance execution diagram](assets/rochallor-monitor-detail.png)

The **Overview** tab contains the Execution Diagram and Step Executions table.
The diagram shows the path through the Workflow Definition.

Select a step in the diagram to highlight its executions. The table shows each
attempt, its status, start and end times, and snapshot availability.

The **Variables** tab shows the current Process Variables. It also lists input
and output Variable Snapshots recorded at Step Execution boundaries.

Variable Snapshots are not a complete history of every variable change.

### Investigate Incidents

Select **Incidents** in the sidebar.

Filter Incidents by Workflow Definition, job type, or occurrence time. Select
an Incident ID to open its details.

The detail page shows the failed step, attempt, time, job context, and Error
Details. Use the Process Instance link to open the related execution.

### Refresh and stale data

Monitor refreshes list data in the background. Use **Refresh** when you need an
immediate update.

If PostgreSQL becomes unavailable after data was loaded, Monitor keeps the last
successful result visible and shows a stale-data warning.

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

The BFF cannot refresh its cached result. Check PostgreSQL availability and the
BFF logs. Monitor will refresh again when the database is available.

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

### Protect sensitive data

Process Variables, Variable Snapshots, and Error Details may contain sensitive
business data.

Put authentication and authorization in front of Monitor. Restrict network
access to the BFF, use TLS, and store database credentials in a secret store.

### Keep schemas compatible

Monitor reads Engine tables directly and does not run migrations. Deploy Engine
migrations before the matching Monitor release.

Test Monitor against a staging database after a migration changes a table,
column, or enum that Monitor reads.

## Limitations

- Authentication, authorization, TLS termination, and ingress policy are
  deployment responsibilities.
- Monitor can observe workflows but cannot change them.
- Direct database reads require compatible Engine and Monitor releases.
- Cached stale data is stored in the BFF process and is lost after a restart.
- PostgreSQL pool sizing and query timeouts use driver defaults.
- Large Incident history may require database performance tuning.
