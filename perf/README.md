# perf — Performance & Load Testing

This directory holds the on-demand load test for rochallor-engine. It boots a private Docker Compose stack, runs a configurable k6 workload (default target: 1,000 VUs), and emits a verified pass/fail report under `reports/<run-id>/`. A configured target is not evidence that the engine sustained that load; inspect the resulting metrics, outcomes and generator utilization.

## Prerequisites

| Tool | Version | Verify |
|---|---|---|
| Docker | any recent | `docker version` |
| Docker Compose | v2 (built into Docker CLI) | `docker compose version` |
| k6 | 1.7.1 (pinned) | `k6 version` |
| git | any | `git --version` |
| Node.js | 24 in CI | `node --version` |
| Python | 3.12+ | `python3 --version` |
| Bash | 3.2+ | `bash --version` |
| Workstation | ≥ 8 cores, ≥ 16 GB RAM recommended | `nproc`, `free -m` |

If `k6` is missing, install via your package manager (`brew install k6`, `apt install k6`, etc.) or use `--containerized-k6` (see Common variations below) to run k6 inside a container.

## Run the default scenario

From the repo root:

```sh
./perf/run.sh
```

That single command:

1. Detects the engine git SHA (`workflow-engine/` HEAD).
2. Pre-flight checks: Docker available, k6 version matches the pinned version. Warns (non-fatal) if ports `18080` or `19091` appear occupied.
3. Boots `perf/docker-compose.yml`: PostgreSQL + engine + 10 Go worker replicas by default. Each run has its own Compose project/network and disposable database. Waits for `/healthz` to return 200.
4. Runs surface-drift canary: uploads `perf/definitions/service-chain.json`, starts a canary workflow, polls its status — aborts cleanly if the engine API surface no longer matches the contract.
5. Runs `perf/scenarios/thousand-concurrent.js` against the engine: ramp-up 2 min → steady-state 10 min @ 1,000 VUs → ramp-down 1 min.
6. k6 `teardown()`: waits 10 s then counts non-terminal workflows from this run.
7. Snapshots engine `/metrics` before compose teardown.
8. Reads every workflow row and pending chain from the private database. Both supported workloads require every instance, including the canary and chained children, to be `COMPLETED`.
9. Validates metrics, outcomes, sampling and any baseline comparison; writes `summary.md`/`summary.json`, raw k6 data, `workflow-outcomes.json`, `host-stats.json`, image IDs and the engine metrics snapshot. Cleanup saves Compose logs and removes this run's containers/network/volumes, including on failure.
10. Prints the run-id and the `summary.md` path. **Exit code 0 on pass, non-zero on fail.**

Expected first output (pass case):

```text
[perf] runId=20260506-143022-a1b2c3d
[perf] engine build=a1b2c3d (workflow-engine)
[perf] booting compose stack...
[perf] engine healthy after 11s
[perf] uploaded definition: id=def_01HXYZ...
[perf] surface drift check: OK
[perf] running scenario thousand-concurrent (target 1000 VUs, 13 min total)...
[perf] ... k6 output ...
[perf] teardown: 0 unfinished workflows
[perf] PASS — see perf/reports/20260506-143022-a1b2c3d/summary.md
```

## Common variations

| You want to … | Command |
|---|---|
| Run the 1,000-VU scenario over gRPC | `./perf/run.sh --grpc` |
| Stress the full loan workflow chain (app → disbursement) | `./perf/run.sh --scenario=loan-chain-full` |
| Same, over gRPC | `./perf/run.sh --scenario=loan-chain-full --grpc` |
| Same, kafka_outbox dispatch | `./perf/run.sh --scenario=loan-chain-full --kafka` |
| Run against a specific engine image instead of the local source | `./perf/run.sh --engine-image=ghcr.io/OWNER/rochallor-engine@sha256:DIGEST` |
| Run k6 inside a container (no host install) | `./perf/run.sh --containerized-k6` |
| Shorter steady-state for a quick smoke | `./perf/run.sh --steady=2m` |
| Full smoke (~30 s, 50 VUs) — verify the harness, not the engine | `./perf/run.sh --smoke` |
| Add an engineer note to the report | `./perf/run.sh --note "after PR#42"` |
| Compare against a previous run | `./perf/run.sh --compare-to=RUN_ID` |
| Compare against the latest passing run | `./perf/run.sh --compare-to-latest` |
| Supply threshold overrides for a diagnostic run | `./perf/run.sh --threshold-overrides=/absolute/path/to/thresholds.json` |

## Options

| Flag | Default | Description |
|---|---|---|
| `--grpc` | — | Run k6 scenario and workers over gRPC (default: REST) |
| `--ramp-up=<d>` | `2m` | k6 ramp-up duration |
| `--steady=<d>` | `10m` | k6 steady-state duration |
| `--ramp-down=<d>` | `1m` | k6 ramp-down duration |
| `--target-vus=<n>` | `1000` | Concurrent VUs at peak (1 VU = 1 active workflow) |
| `--smoke` | — | 50-VU, 30-second harness-validation run |
| `--engine-image=<ref>` | — | Use a pre-built engine image instead of local source |
| `--containerized-k6` | — | Run k6 inside a container (no host k6 install needed) |
| `--compare-to=<runId>` | — | Compare against a prior run |
| `--compare-to-latest` | — | Compare against the most recent passing run |
| `--note=<text>` | — | Attach an engineer note to the run report |
| `--threshold-overrides=<f>` | — | Override strict thresholds from a file |
| `--scenario=<name>` | `thousand-concurrent` | Scenario to run: `thousand-concurrent` or `loan-chain-full` |

`--threshold-overrides` sets `summary.json.thresholdsLineage = "overridden"`. A "pass" with overrides is **not** the same as a strict pass — the report makes this explicit.

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `PERF_ENGINE_REST_PORT` | `18080` | Host port for the engine REST API |
| `PERF_ENGINE_GRPC_PORT` | `19090` | Host port for the engine gRPC API |
| `PERF_ENGINE_METRICS_PORT` | `19091` | Host port for the engine `/metrics` endpoint |
| `PERF_POSTGRES_PORT` | `5434` | Host port for the private test database |
| `PERF_WORKER_REPLICAS` | `10` | Go worker replica count, recorded in the report |

Do not run unrelated stacks during performance comparisons or soak tests. To debug the harness alongside a development stack, use different values for all four exposed ports; those short diagnostic runs are not capacity measurements.

The polling pool reuses the E2E Go worker, with two slots and a 500 ms poll interval per replica. Record the replica count and check worker utilization: this pool can limit workflow throughput before the engine does. A passing configured VU target does not establish maximum engine capacity.

The service-chain/loan-chain pool uses its normal runners with heartbeat disabled. Enabled polling heartbeat is measured separately by `perf/heartbeat.py` and the SDK heartbeat suites; a general service-chain soak is not a long-handler renewal test.

## Harness verification and comparison contract

```sh
node --test perf/test/report.test.cjs
python3 -m unittest discover -s perf/test -p '*test.py' -v
python3 perf/test/postgres_stats.py
python3 perf/test/request_groups.py
```

Fixtures cover exact REST/gRPC count, percentile and error-rate aggregation; missing/malformed data; FAILED/CANCELLED outcomes; incompatible baselines; regressions including errors rising from zero; and k6 threshold formats. Full `run.sh` tests use local fake commands to prove report, outcome-query, k6 and cleanup failures propagate. The CI workflow also runs a real short workload in polling/Kafka × REST/gRPC.

Reports use schema version 2.0. Streaming k6 data is read line by line, and malformed lines are fatal. Required operations must have steady-state samples and matching latency/error sample counts. Unexecuted operations are omitted instead of represented by fabricated zero latency. Missing outcome or utilization evidence fails the gate.

REST helpers give each operation a stable k6 `name` tag. k6 groups both `name` and `url` metrics under that value, while requests retain their actual workflow IDs and query strings. The report rejects ungrouped REST samples so longer runs cannot silently accumulate one metric series per workflow URL. `request_groups.py` checks 160 actual requests across eight operations using native k6, or the pinned container image when native k6 is absent.

Comparison requires a passing v2 baseline with identical harness hash, scenario/configuration, dispatch mode, transport, workers, threshold overrides, generator mode and host/toolchain. Missing or incompatible baselines fail; `--compare-to-latest` selects the latest passing report and still enforces compatibility. Older reports from the broken harness cannot serve as baselines. A p95 increase over 15%, throughput decrease over 10%, or absolute error-rate increase over 0.0005 makes the whole command fail, even if k6's absolute thresholds pass.

Generator CPU is measured from the actual k6 process or container and normalized by available CPU cores. A peak at or above 90% of that capacity fails the load gate. The report records raw samples, engine/worker/PostgreSQL utilization and the container image IDs actually used. This screening does not prove absence of every possible generator bottleneck.

Resource samples also retain native process `rssMB`, or Docker's `containerMemoryUsage` when k6 runs in a container. Docker memory usage is not reported as process RSS.

## Reading the report

After a run completes, open:

```text
perf/reports/<run-id>/summary.md
```

The first line tells you pass/fail. The TL;DR tells you why (or, on a pass, the headline numbers). For deep dives:

- `summary.json` — diff two runs by hand or with `jq`.
- `summary.json` → `hostStatsSummary.generatorLikelySaturated == true` invalidates the load result; rerun at an appropriate load or on a larger generator. Raw samples are in `host-stats.json`.
- `engine-metrics-snapshot.txt` — engine's own counters at end-of-run, useful for understanding internal state (job queue depth, dispatch latency histograms, etc.) when something looks off.

## Scenarios

### `thousand-concurrent` (default)

A linear three-step service-task chain (`go-step-a/b/c`). Each VU drives one active workflow instance. See `perf/definitions/service-chain.json`.

### `loan-chain-full`

The full loan-approval pipeline: `workflow-loan-application` (`autoStartNextWorkflow` chains to) `workflow-loan-disbursement`. Each VU iteration starts an application with `creditScore=600`, `fraudScore=0.0`, `loanAmount=600_000_000`, which routes through:

- App: `validate → parallel(credit-score, fraud-screen) → join → compute-risk → route → manual-review-task` → VU completes the user task with `reviewDecision=APPROVED` → `process-review-decision → auto-approve → end-approved`.
- Disb (auto-chained): `compute-disbursement → route → senior-approval-task` → completed asynchronously by one `senior_task_completer` VU with `seniorDecision=APPROVED` → `check-senior-decision → prepare-disbursement → transfer-funds → notify-customer → end-disbursed`.

That path exercises every step type the engine supports (`SERVICE_TASK`, `PARALLEL_GATEWAY`, `JOIN_GATEWAY`, `TRANSFORMATION`, `DECISION`, `USER_TASK`, `END`) on every iteration.

The completer runs serially because user tasks have no claim operation for coordinating concurrent completers. `PERF_SENIOR_COMPLETER_VUS` defaults to 1; other values are rejected. This driver can limit loan-chain throughput, so this workload does not isolate maximum engine capacity.

At teardown k6 continues draining senior-approval tasks for at least 30 seconds, including tasks reached by delayed chained workflows, then checks non-terminal instances of both definitions. The command additionally reads every workflow and pending chain from the private database; FAILED, CANCELLED, unfinished or missing outcomes fail. The stack is removed with `down -v` between runs.

Transport: REST by default; add `--grpc` to drive the same chain over gRPC (uses `loan-chain-full-grpc.js`). Both transports support `--kafka` (engine + workers run in `kafka_outbox` dispatch mode; the k6 client side is unaffected).

## Acceptance scenarios

### "the engine survives 1,000 concurrent workflows"

```sh
./perf/run.sh
```

### "the engine survives 1,000 concurrent workflows with protocol is GPRC"
```sh
./perf/run.sh --grpc
```

### "the engine survives 1,000 concurrent workflows with Dispatch Mode is Kafka Outbox"
```sh
./perf/run.sh --kafka
```

### "the engine survives 1,000 concurrent workflows with Dispatch Mode is Kafka Outbox and protocol is GRPC"
```sh
./perf/run.sh --grpc --kafka
```

### "the engine survives 1,000 concurrent workflows with Scenarios is  `loan-chain-full`"
```sh
./perf/run.sh --scenario=loan-chain-full
```

### "the engine survives 1,000 concurrent workflows with Scenarios is  `loan-chain-full` and with Dispatch Mode is Kafka Outbox and protocol is GRPC"
```sh
./perf/run.sh --scenario=loan-chain-full --grpc --kafka
```

Expect: `result=pass`, all four operations within their thresholds, 0 unfinished workflows.

### Negative gate checks

`node --test perf/test/report.test.cjs` deliberately injects wrong metrics, business outcomes, k6 failures and cleanup failures. Each must make the complete command fail. This verifies the gate itself without treating an arbitrary DB-pool limit as a guaranteed performance regression.

### Reproducibility

Run twice in a row on the same SHA:

```sh
./perf/run.sh
./perf/run.sh --compare-to-latest
```

Expect: the second run's `summary.md` Comparison section reports `verdict: within-noise` (p95 within ±15%, throughput within ±10%).

### Regression detection

After establishing a baseline run, intentionally regress the engine (e.g., add `time.Sleep(50 * time.Millisecond)` in the workflow start handler), rebuild, rerun:

```sh
./perf/run.sh --compare-to=<baseline-runId>
```

Expect: `summary.md` Comparison section reports `verdict: regressed` and lists the affected operations with deltaPct > 15%.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `[perf] k6 version mismatch` | Local k6 not pinned version | Install pinned k6 OR use `--containerized-k6` |
| `[perf] port 18080 in use` | Old `e2e/` or other Compose stack still up | `docker compose -f e2e/docker-compose-polling.yml down`, or stop the offending process |
| `[perf] surface drift detected` | Engine REST API changed shape | See `contracts/engine-endpoints.md` — the perf scenario must be updated alongside |
| `result=fail` but `generatorLikelySaturated=true` | Laptop saturated, not the engine | Run on a beefier machine, OR reduce target VUs (`--target-vus=500` for a diagnostic) |
| `result=fail` with all-zero metrics | Engine never became healthy | Check `docker compose logs engine` |
| `unfinishedWorkflows.count > 0` after steady-state | Some workflows stuck in non-terminal state | Inspect a few `sampleIds` via `GET /v1/instances/{id}/history` from the engine before teardown — engine likely lost a job dispatch |

## Layout

```text
perf/
├── README.md                  # this file
├── docker-compose.yml         # isolated SUT: postgres + engine + configurable Go workers
├── docker-compose.image.yml   # overlay for --engine-image (replaces build with pre-built image)
├── run.sh                     # single-command entry point
├── scenarios/                 # k6 scenarios (one file per scenario)
├── definitions/               # workflow definitions under load
│   ├── service-chain.json     # default 3-step chain
│   └── loan-chain/            # full loan-application + disbursement chain
├── lib/                       # shared k6 helpers + host-stats sampler
└── reports/                   # per-run artefacts (gitignored)
```

Each of these is a candidate for a future iteration once the strict-bar 1,000-VU gate is consistently green.

## Paired heartbeat cost measurement

After T02 and the SDK E2E gates, run `python3 perf/heartbeat.py --sdk=<go|node|python|java>
--transport=<rest|grpc> --engine-image=<verified-engine-image>
--worker-image=<verified-polling-E2E-worker-image> --output=<evidence-directory>`
(on one shell line). The selected worker must include the heartbeat E2E registry.
Run alone, without another load test or a heavy build on the host.

The command creates two disposable Compose stacks, renewing off then on, using
the same pinned engine/worker bytes, two handler slots, 30-second leases,
10-second renewal intervals and eight 12-second jobs by default. Every workflow
must complete with one handler call, one job attempt and unchanged retry budget.
It records actual `pg_stat_user_tables.n_tup_upd`, transaction mean latency from
engine metrics, start/workflow p95, throughput, timeouts and sampled CPU/memory.
After the workload, it captures metrics/resources and stops the engine before
reading cumulative DB statistics. It waits for the DB client connections to
close, which flushes backend-local counters; a fixed sleep can miss committed
updates. The recorded `writerConnections` must be zero. The real PostgreSQL
regression command above verifies this collection boundary.
Renewal must add at least two accepted heartbeats per job, and the additional job
updates must account for every accepted heartbeat. Workflow p95 may not regress
by >15%, throughput by >10%; start p95 must stay <1 second, workflow p95 <30
seconds, and generator CPU below 90% capacity. Missing evidence fails the command.

This is a controlled renewal-cost check, not a peak-capacity benchmark or the
M3 60-minute soak. The common k6 harness still owns the four dispatch/transport
baseline comparisons and the two dispatch-mode soaks. Gate fixtures:
`python3 -m unittest discover -s perf/test -p '*test.py' -v`.

The heartbeat cost command uses a Python driver at concurrency 2. It reuses the T02 resource sampler: fields named `k6Process`/`k6CapacityPctPeak` refer to that Python driver in these off/on reports, not to a k6 process. The separate k6 benchmark and soak reports still sample actual k6.
