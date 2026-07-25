# Process Instance List Query-Plan Baseline

## Purpose

This artifact captures the initial query shapes used to qualify the Rochallor
Monitor Process Instance list. It is evidence for later performance testing,
not a production performance guarantee.

## Dataset

The automated verification seeds 100,000 Process Instances across:

- 20 Workflow Definition IDs
- All five native Process Instance statuses
- Unique business keys
- Distinct UTC start timestamps

The test applies all engine-owned migrations, runs `ANALYZE`, and executes the
queries through the monitor's `SELECT`-only PostgreSQL role.

## Captured query shapes

`workflow-monitor-bff/test/process-instance-query-plan.spec.ts` captures
`EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` plans for:

1. An unfiltered first page ordered by `started_at DESC, id DESC`.
2. A cursor page filtered by exact definition ID, multiple statuses, and an
   inclusive-from/exclusive-to UTC range.
3. A page filtered by exact business key.

Every plan must execute successfully and return no more than the requested
51 rows (50 visible rows plus one look-ahead row).

## Initial index constraints

The unchanged engine schema currently provides separate indexes for
`started_at DESC`, `(definition_id, status)`, and `business_key`. It does not
provide a composite `(started_at DESC, id DESC)` index. PostgreSQL may
therefore add a sort or incremental sort for stable cursor ordering.

No engine migration is introduced in this version. Any required engine-owned
index must be proposed as a separate engine change.

## Reproduction

Run:

```sh
cd workflow-monitor-bff
DOCKER_HOST=unix:///Users/batnamv/.docker/run/docker.sock pnpm test -- \
  test/process-instance-query-plan.spec.ts
```

Set `DOCKER_HOST` to the local Docker socket when it differs from the example.
