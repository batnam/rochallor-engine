# Rochallor Monitor BFF

NestJS read-only API for Rochallor Monitor. It queries an already migrated
Rochallor Engine PostgreSQL database and should use a dedicated read-only
database role outside local development.

## Prerequisites

- Node.js 24.15.0
- pnpm 9.12.3
- PostgreSQL containing the current Rochallor Engine schema

The BFF does not run database migrations. Start the workflow engine at least
once against the database before starting the BFF, or apply the matching engine
migrations separately.

## Run locally

From the repository root:

```bash
cd workflow-monitor-bff
pnpm install --frozen-lockfile

export MONITOR_POSTGRES_DSN="postgres://workflow:workflow@localhost:5434/workflow?sslmode=disable"
export PORT=3000

pnpm build
pnpm start
```

The process reads configuration from its environment; it does not load `.env`
files automatically. Re-run `pnpm build` and restart the process after changing
the TypeScript source.

Once started, the following endpoints are available:

- API: `http://localhost:3000/api/v1`
- Swagger UI: `http://localhost:3000/api-docs`
- OpenAPI JSON: `http://localhost:3000/openapi.json`
- Liveness: `http://localhost:3000/health/live`
- Readiness: `http://localhost:3000/health/ready`

## Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `MONITOR_POSTGRES_DSN` | Yes | — | PostgreSQL connection string for the migrated engine database. |
| `PORT` | No | `3000` | HTTP listener port. |
| `MONITOR_MAX_JSON_DOCUMENT_BYTES` | No | `5242880` | Maximum definition or snapshot JSON document size returned by the BFF. |
| `MONITOR_POSTGRES_POOL_MAX` | No | `5` | Maximum connections per BFF process. |
| `MONITOR_POSTGRES_CONNECTION_TIMEOUT_MS` | No | `2000` | Connection establishment and pool wait limit. |
| `MONITOR_POSTGRES_STATEMENT_TIMEOUT_MS` | No | `3000` | PostgreSQL timeout for each statement. |

## Diagnostic API

- `GET /api/v1/process-instances/:id` returns `observedAt` and per-current-step
  `executionContext` alongside the instance, pinned definition, and overlay in
  the same `REPEATABLE READ READ ONLY` transaction. Context includes current
  job/task evidence and up to 100 pending timers per execution. Missing or
  conflicting evidence is explicitly unavailable; terminal context is empty.
- `GET /api/v1/overview` groups retained instances by definition/version with
  `active`, `waiting`, and `failed` counts. Optional `workflow` searches by a
  case-insensitive literal substring of the name or ID, ignoring surrounding
  whitespace, before pagination. Cursors are bound to the applied search.
  `GET /api/v1/overview/steps` requires
  `definitionId` and `definitionVersion`, and accepts `minStepAgeSeconds`
  (default 1800). It counts distinct current instances, aged running executions,
  and unavailable ages. Both return observation time, counting scope, and opaque
  cursors; `pageSize` defaults to 50 and is capped at 100.
- Instance list filters include `definitionVersion` and `currentStepId` (both
  require `definitionId`), and exclusive UTC `stepStartedBefore` (requires
  `currentStepId`). Step filters match only current `ACTIVE`/`WAITING` instances;
  age uses the latest running execution. Cursors are bound to all filters.
- Incident list/detail includes `historical` and `latestAttempt` metadata.
  Incidents remain failed execution history, not open/resolved issue records.
- Step snapshot responses include `outputInterpretation`: `variableDelta` for
  service tasks, `fullState` for user tasks/waits/transformations, and `unknown`
  for other types. Existing document availability and size limits still apply.

Swagger documents the response schemas. Startup/readiness validate required
types and read grants on all six consumed tables, including `user_task` and
`boundary_event_schedule`. Engine migrations remain unchanged and Engine-owned.
The BFF never migrates, writes workflow data, or calls Engine APIs. This project
must not implement authentication or TLS termination/templates.

## Checks

Run commands from `workflow-monitor-bff/`:

```bash
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

The test suite uses Docker to create isolated PostgreSQL containers where
database integration is required.

Query-plan tests use production SQL and the SELECT-only role, with 100,000
instances and representative attempts/jobs/tasks/timers. They enforce latency,
scan, and shared-buffer budgets and write plans to `testresults/query-plans/`.
See the [diagnostic query baseline](../docs/performance/monitor-diagnostics-query-plan.md)
for fixture scope and limitations.

After the BFF is running on port `3000`, start the
[Monitor frontend](../workflow-monitor/README.md).

See the [Monitor operator guide](../docs/monitor.md) for the recommended
read-only database grants, schema compatibility, and deployment security.
