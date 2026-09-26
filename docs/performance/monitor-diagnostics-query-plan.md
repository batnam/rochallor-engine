# Monitor Diagnostic Query-Plan Baseline

## Scope and reproduction

The P1/P2 diagnostics use existing Engine tables and indexes. Monitor performs
no migrations. These measurements are regression baselines for fixed fixtures,
not production latency guarantees.

From the repository root, with Docker available:

```sh
pnpm --dir workflow-monitor-bff exec jest --runInBand \
  test/execution-context-query-plan.spec.ts test/overview-query-plan.spec.ts
```

Both suites apply all Engine migrations to disposable PostgreSQL 16 Alpine
containers, seed data, run `ANALYZE`, and execute production SQL through a
SELECT-only role. Three warmed `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` samples
produce a median execution time. Shared blocks are root hits plus reads;
scanned rows count relation-node rows and filter/recheck removals multiplied by
loops. Temporary blocks and complete plans are recorded in
`workflow-monitor-bff/testresults/query-plans/`, which CI already archives.

## Fixtures and budgets

Context uses 100,000 instances, each with three parallel current executions
(service task, wait, user task), 200,000 jobs (half retired), 100,000 open user
tasks, and 100,000 pending timers. The measured read requests one instance's
three current steps. Separate behavioral tests cover missing/conflicting rows,
obsolete timers, terminal states, and response truncation.

Overview uses 100,000 instances across 20 definitions and two versions, all five
instance statuses, two current-step IDs, and 400,000 executions (three service
attempts plus a wait per instance). The aggregate tests measure all definition
groups and one definition/version's current steps. Drill-down uses the real
instance query with a current-step and absolute age cutoff.

Measured on 2026-09-25 with local Docker and Node.js 24.15.0:

| Production query | Median ms | Shared blocks | Scanned rows | Maximum ms / blocks / rows |
| --- | ---: | ---: | ---: | --- |
| Current execution context, three parallel steps | 9.630 | 2,367 | 200,004 | 250 / 10,000 / 250,000 |
| Definition/version counts | 11.611 | 1,445 | 100,040 | 500 / 10,000 / 150,000 |
| Current-step counts and age | 25.203 | 26,499 | 15,000 | 500 / 50,000 / 100,000 |
| Step-age instance drill-down | 11.503 | 13,936 | 10,000 | 500 / 50,000 / 100,000 |

All four plans recorded zero temporary blocks. Overview's aggregate envelope
contains one SQL row with bounded JSON items; API tests independently verify
item limits, count accuracy, cursor scope, and exact drill-down results.

## Query shape and index decision

Joining definition names before grouping initially used 28,741 shared blocks.
Grouping and limiting definition/version counts before the name join reduced
that to 1,445 without widening the budget.

Repeated lateral reads of jobs and timers for three parallel executions
initially scanned 600,006 rows in 29.158 ms with 7,071 shared blocks, exceeding
the 250,000-row budget. The final context query materializes the instance's
live jobs and pending timers once, then matches execution IDs. It reads task
evidence only for user tasks and job evidence only for service tasks. This
reduced scanned rows to 200,004 and retained the original budget.

Existing Engine indexes satisfy these measured budgets after query changes;
no Engine index migration was added. The context query still scans substantial
live-job/pending-timer data because those relations lack suitable per-instance
indexes for every predicate. Overview must aggregate retained records, and
pagination bounds only its response. Large histories, many parallel steps,
skewed definitions, large payloads, cold caches, and concurrent Engine writes
need additional measurements before capacity planning. Any future index must
be evaluated with its write overhead and shipped through Engine migrations,
never created by Monitor.

Incident latest-attempt metadata is covered by the updated
[Incident baseline](incident-list-query-plan.md); the existing
[instance-list baseline](process-instance-list-query-plan.md) also continues to
pass. Connection/pool timeouts and per-statement budgets remain enabled for all
these queries.
