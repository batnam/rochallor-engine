# perf — Performance & Load Testing

This directory holds the on-demand load test for the rochallor-engine. It boots an isolated Docker Compose stack, drives the engine to **1,000 concurrent active workflow instances** with k6, and emits a pass/fail report under `reports/<run-id>/`.

## Prerequisites

| Tool | Version | Verify |
|---|---|---|
| Docker | any recent | `docker version` |
| Docker Compose | v2 (built into Docker CLI) | `docker compose version` |
| k6 | 1.7.1 (pinned) | `k6 version` |
| git | any | `git --version` |
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
3. Boots `perf/docker-compose.yml`: postgres + engine + 8 × Go worker replicas. Waits for `/healthz` to return 200.
4. Runs surface-drift canary: uploads `perf/definitions/service-chain.json`, starts a canary workflow, polls its status — aborts cleanly if the engine API surface no longer matches the contract.
5. Runs `perf/scenarios/thousand-concurrent.js` against the engine: ramp-up 2 min → steady-state 10 min @ 1,000 VUs → ramp-down 1 min.
6. k6 `teardown()`: waits 10 s then counts non-terminal workflows from this run.
7. Snapshots engine `/metrics` before compose teardown.
8. Stops `host-stats` sampler, tears the Compose stack down, removes the postgres volume.
9. Writes `perf/reports/<run-id>/{summary.md, summary.json, k6-output.json, k6-summary-export.json, host-stats.json, engine-metrics-snapshot.txt}`.
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
| Run against a specific engine image instead of the local source | `./perf/run.sh --engine-image ghcr.io/.../workflow-engine:<digest>` |
| Run k6 inside a container (no host install) | `./perf/run.sh --containerized-k6` |
| Shorter steady-state for a quick smoke | `./perf/run.sh --steady 2m` |
| Full smoke (~30 s, 50 VUs) — verify the harness, not the engine | `./perf/run.sh --smoke` |
| Add an engineer note to the report | `./perf/run.sh --note "after PR#42"` |
| Compare against a previous run | `./perf/run.sh --compare-to 20260505-101010-deadbee` |
| Compare against the latest passing run | `./perf/run.sh --compare-to-latest` |
| Loosen thresholds for a diagnostic run (NOT a passing run) | `./perf/run.sh --threshold-overrides perf/lib/thresholds.relaxed.js` |

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

## Reading the report

After a run completes, open:

```text
perf/reports/<run-id>/summary.md
```

The first line tells you pass/fail. The TL;DR tells you why (or, on a pass, the headline numbers). For deep dives:

- `summary.json` — diff two runs by hand or with `jq`.
- `summary.json` → `hostStatsSummary.generatorLikelySaturated == true` ⇒ **the laptop, not the engine, was the bottleneck**. Re-run on a sized environment, or accept that this run is not authoritative. (Raw samples are in `host-stats.json`.)
- `engine-metrics-snapshot.txt` — engine's own counters at end-of-run, useful for understanding internal state (job queue depth, dispatch latency histograms, etc.) when something looks off.

## Scenarios

### `thousand-concurrent` (default)

A linear three-step service-task chain (`go-step-a/b/c`). Each VU drives one active workflow instance. See `perf/definitions/service-chain.json`.

### `loan-chain-full`

The full loan-approval pipeline: `workflow-loan-application` (`autoStartNextWorkflow` chains to) `workflow-loan-disbursement`. Each VU iteration starts an application with `creditScore=600`, `fraudScore=0.0`, `loanAmount=600_000_000`, which routes through:

- App: `validate → parallel(credit-score, fraud-screen) → join → compute-risk → route → manual-review-task` → VU completes the user task with `reviewDecision=APPROVED` → `process-review-decision → auto-approve → end-approved`.
- Disb (auto-chained): `compute-disbursement → route → senior-approval-task` → completed asynchronously by the `senior_task_completer` side scenario with `seniorDecision=APPROVED` → `check-senior-decision → prepare-disbursement → transfer-funds → notify-customer → end-disbursed`.

That path exercises every step type the engine supports (`SERVICE_TASK`, `PARALLEL_GATEWAY`, `JOIN_GATEWAY`, `TRANSFORMATION`, `DECISION`, `USER_TASK`, `END`) on every iteration.

Verification is **aggregate at teardown**: k6 drains any straggling senior-approval user tasks, sleeps 30 s, then counts non-terminal instances of both definitions. Either count above zero fails the run via `chain_app_unfinished_after_steady_state` / `chain_disb_unfinished_after_steady_state`. The compose stack is torn down with `down -v` between runs, so any non-terminal instance the engine reports at teardown belongs to this run.

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

### "the engine fails when intentionally constrained"

Reduce engine DB connections in the Compose stack (env: `WE_PG_MAX_CONNS=4`) and rerun:

```sh
WE_PG_MAX_CONNS=4 ./perf/run.sh --note "constrained DB pool"
```

Expect: `result=fail`, `summary.md` names the breached threshold (likely `http_req_duration{operation:workflow.poll}` or `http_req_failed`), and `failures.engine_error > 0`.

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
./perf/run.sh --compare-to <baseline-runId>
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
├── docker-compose.yml         # isolated SUT: postgres + engine + 8 go workers
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
