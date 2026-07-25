# Incident List Query-Plan Baseline

## Purpose

This artifact captures the initial query shapes used to qualify the Rochallor
Monitor Incident list. It is evidence for later performance testing, not a
production performance guarantee.

## Dataset

The automated verification seeds 100,000 failed Step Executions across:

- 20 Workflow Definition IDs
- 50 step IDs
- Service and non-service step types
- 50,000 related Jobs across 10 Job types
- Failed and Cancelled Process Instances
- Distinct UTC occurrence timestamps

The test applies all engine-owned migrations, runs `ANALYZE`, and executes the
queries through the monitor's `SELECT`-only PostgreSQL role.

## Captured query shapes

`workflow-monitor-bff/test/incident-query-plan.spec.ts` captures
`EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` plans for:

1. An unfiltered first page ordered by occurrence time and Incident ID.
2. A cursor page filtered by exact definition ID, exact Job type, and an
   inclusive-from/exclusive-to UTC occurrence range.

Every plan must execute successfully and return no more than the requested
51 rows (50 visible rows plus one look-ahead row).

## Initial index constraints

The unchanged engine schema indexes Step Executions by Process Instance and
start time, but not by failed status or `ended_at`. It also has no index on
`job.step_execution_id`. The Incident query therefore deduplicates Job context
once before joining it to canonical failed Step Executions, avoiding a
correlated Job-table scan for every Incident.

No engine migration is introduced in this version. Any required engine-owned
indexes must be proposed as a separate engine change.

## Reproduction

Run:

```sh
cd workflow-monitor-bff
DOCKER_HOST=unix:///Users/batnamv/.docker/run/docker.sock pnpm test -- \
  test/incident-query-plan.spec.ts
```

Set `DOCKER_HOST` to the local Docker socket when it differs from the example.
