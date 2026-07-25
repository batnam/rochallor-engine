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

After the BFF is running on port `3000`, start the
[Monitor frontend](../workflow-monitor/README.md).

See the [Monitor operator guide](../docs/monitor.md) for the recommended
read-only database grants, schema compatibility, and deployment security.
