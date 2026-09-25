# Process Instance List Query-Plan Baseline

## Purpose

This artifact records measured regression budgets for the production Monitor
Process Instance list queries. It is not a production latency guarantee.

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

The tests invoke the production query classes, capture their SQL and parameters,
then run three warmed `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` samples. The
cursor case uses the actual cursor returned by the first page. Each plan must
return between 1 and 51 rows (50 visible rows plus one look-ahead row).

Latency is the median PostgreSQL execution time, excluding network and browser
rendering. Shared blocks are root-level hits plus reads, without summing the
same buffers again from child nodes. Scanned rows sum relation-node rows and
filter/recheck removals, multiplied by actual loops. Temporary buffer I/O and
full plans are recorded too. Results live in `testresults/query-plans/` under the
BFF; CI archives this directory even when a budget fails.

Measurements below were made on 2026-09-25 with PostgreSQL 16 Alpine in local
Docker and Node.js 24.15.0. Fixed fixtures and warm-cache medians reduce noise;
budgets allow headroom for shared CI hosts. Compare plans before changing a
threshold. Repeat with production-like payload sizes, skew, concurrency, and
hardware before using these values for capacity planning.

## Measured baseline and budgets

| Query | Median ms | Shared blocks | Scanned rows | Maximum ms / blocks / rows |
| --- | ---: | ---: | ---: | --- |
| First page | 0.029 | 5 | 52 | 250 / 250 / 1,000 |
| Filtered cursor | 0.111 | 26 | 1,041 | 250 / 2,000 / 10,000 |
| Exact business key | 0.014 | 4 | 1 | 250 / 250 / 1,000 |

## Index evaluation

The unchanged engine schema currently provides separate indexes for
`started_at DESC`, `(definition_id, status)`, and `business_key`. It does not
provide a composite `(started_at DESC, id DESC)` index. PostgreSQL may
therefore add a sort or incremental sort for stable cursor ordering.

The current instance queries read few rows/blocks on this fixture. There is
no measured reason here to add an Engine-owned index; remeasure with skewed
start times and production filters before proposing one.

## Reproduction

Run:

```sh
cd workflow-monitor-bff
pnpm test -- \
  test/process-instance-query-plan.spec.ts
```

Docker must be available. Set `DOCKER_HOST` only if your local environment
requires a non-default Docker socket.
