# Incident List Query-Plan Baseline

## Purpose

This artifact records measured regression budgets for the production Monitor
Incident list queries. It is not a production latency guarantee.

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
| First page | 195.629 | 362,450 | 240,020 | 1,000 / 500,000 / 400,000 |
| Filtered cursor | 31.443 | 22,456 | 60,001 | 500 / 50,000 / 100,000 |

The small result limit does not imply cheap work: the first page still joins
and sorts substantial history. Resource limits bound this work, but larger
deployments still need workload-specific tuning.

## Index evaluation

The unchanged engine schema indexes Step Executions by Process Instance and
start time, but not by failed status or `ended_at`. It also has no index on
`job.step_execution_id`. The Incident query therefore deduplicates Job context
once before joining it to canonical failed Step Executions, avoiding a
correlated Job-table scan for every Incident.

An isolated test database was used to measure these candidate indexes together:

```sql
CREATE INDEX monitor_experiment_failed_order
  ON step_execution (ended_at DESC, id DESC) WHERE status = 'FAILED';
CREATE INDEX monitor_experiment_job_latest
  ON job (step_execution_id, created_at DESC, id DESC) INCLUDE (job_type, status);
```

| Query | Before ms | With candidates ms | Shared blocks before → after | Temp blocks before → after |
| --- | ---: | ---: | --- | --- |
| First page | 192.604 | 187.787 | 362,450 → 363,085 | 6,978 → 5,973 |
| Filtered cursor | 31.203 | 24.554 | 22,456 → 23,091 | 1,005 → 0 |

These indexes reduce sorting/spill for the filtered case but do not materially
reduce first-page work. The global latest-job subquery and joins remain costly.
No Engine migration is justified by this experiment alone, especially given
write overhead. Any further proposal should compare query shape and index
changes together, preserving latest-job/filter semantics, on representative
history and concurrent Engine writes. The candidate indexes were created only
inside a disposable test database.

## Reproduction

Run:

```sh
cd workflow-monitor-bff
pnpm test -- \
  test/incident-query-plan.spec.ts
```

Docker must be available. Set `DOCKER_HOST` only if your local environment
requires a non-default Docker socket.
