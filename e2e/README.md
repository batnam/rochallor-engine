# E2E Tests

End-to-end integration tests that spin up the full stack (PostgreSQL, engine, SDK workers) via Docker Compose and run scenario-based tests against it.

Each scenario type runs against every combination of **SDK** (go, python, node, java) and **transport** (rest, grpc), giving full coverage of both API surfaces.

## Prerequisites

- Docker + Docker Compose v2
- Go 1.22+
- Run all commands from the **repo root** (`rochallor-engine/`)

## Quick start

Node workers install the verified SDK tarball instead of copying SDK source.
Before a run with `--sdk=node` or `--sdk=all`, prepare it once (Node 24+ and
`protoc` required on the host):

```sh
(cd workflow-sdk-node && npm ci && npm run package:verify)
```

Keep `workflow-sdk-node/artifacts/sdk.tgz` and `SHA256SUMS` together. Docker
checks the checksum before installation; `run.sh` never rebuilds the SDK.
Python and Java workers likewise consume packaged candidates. Prepare their
wheel or Maven repository before `--sdk=python`, `--sdk=java` or `--sdk=all`:

```sh
(cd workflow-sdk-python && python -m pip install '.[dev]' && python scripts/verify-package.py)
(cd workflow-sdk-java && python3 scripts/verify-package.py) # JDK 21
```

`--sdk=<language>` starts only that SDK's worker and infrastructure, including
for shared loan scenarios. Run stacks sequentially: they share local ports and
the `e2e-net` network.

For a Node candidate, run each worker transport explicitly for both modes:

```sh
for mode in polling kafka_outbox; do
  for transport in rest grpc; do
    WE_DISPATCH_MODE="$mode" ./e2e/run.sh --sdk=node --transport="$transport"
  done
done
```

```sh
# REST transport, polling dispatch, all SDKs (default)
./e2e/run.sh

# Single SDK
./e2e/run.sh --sdk=go
```

## Monitor browser tests

The Monitor Playwright suite combines the GHCR-based quick-start definition
with a local-build override. This keeps the deployment file suitable for quick
deployment while ensuring E2E tests exercise the source in the current
checkout:

```sh
pnpm --dir workflow-monitor test:e2e
```

The test setup creates and seeds a temporary PostgreSQL database, supplies its
DSN through the environment, then runs the equivalent of:

```sh
docker compose \
  --project-name rochallor-monitor-e2e \
  -f deploy/docker-compose.monitor.quickstart.yml \
  -f e2e/docker-compose-monitor.yml \
  up --build --detach
```

For manual use, set `MONITOR_POSTGRES_DSN` to an already migrated engine
database before running that Compose command. Stop the stack with:

```sh
docker compose \
  --project-name rochallor-monitor-e2e \
  -f deploy/docker-compose.monitor.quickstart.yml \
  -f e2e/docker-compose-monitor.yml \
  down --volumes --remove-orphans
```

## Transport

The `--transport` flag (or `TRANSPORT` env var) controls the transport used by **both** the test runner and the SDK workers:

- **Test runner** — which API surface it uses to orchestrate workflows (upload definitions, start/query instances, complete user tasks, signal waits).
- **SDK workers** — which client they use to poll for jobs and report completions/failures.

`run.sh` automatically sets `WORKER_TRANSPORT=$TRANSPORT` before starting the Docker Compose stack, so both layers are always aligned.

| Value | Test runner | Workers |
|-------|-------------|---------|
| `rest` | **Default.** REST API (`http://localhost:ENGINE_REST_PORT`) | REST (`ENGINE_REST_URL`) |
| `grpc` | gRPC API (`localhost:ENGINE_GRPC_PORT`) | gRPC (`ENGINE_GRPC_HOST`) |
| `all` | Runs the orchestration suite twice | Legacy mode does not switch worker transport; use separate `rest`/`grpc` runs for SDK verification |

Set via environment variable or `--transport` flag:

```sh
# gRPC transport for both runner and workers
TRANSPORT=grpc ./e2e/run.sh

# Both worker transports in separate stacks
for transport in rest grpc; do ./e2e/run.sh --transport="$transport"; done

# Flag form
./e2e/run.sh --transport=grpc --sdk=go
```

`run.sh` sets `WORKER_TRANSPORT` to the selected transport; an externally set
value is overwritten. For mixed transports, start Compose and the Go runner
directly.

## Dispatch mode

The `WE_DISPATCH_MODE` variable controls how the engine delivers jobs to workers.

| Mode | Env var | Description |
|------|---------|-------------|
| **Polling** | `WE_DISPATCH_MODE=polling` | **Default.** Workers call `POST /v1/jobs/poll`. Simplest operation. |
| **Kafka Outbox** | `WE_DISPATCH_MODE=kafka_outbox` | **Event-Driven.** Engine writes to `dispatch_outbox` table; a relay pushes to Kafka; workers consume from topics. |

```sh
# Kafka dispatch, default REST transport
WE_DISPATCH_MODE=kafka_outbox ./e2e/run.sh

# Kafka dispatch + gRPC transport
WE_DISPATCH_MODE=kafka_outbox TRANSPORT=grpc ./e2e/run.sh

# Kafka dispatch + gRPC worker and runner
WE_DISPATCH_MODE=kafka_outbox ./e2e/run.sh --sdk=java --transport=grpc
```

When running in `kafka_outbox` mode:
1. A **Kafka** container is automatically started.
2. A **kafka-setup** container pre-creates the necessary `workflow.jobs.<jobType>` topics.
3. The **engine** starts the Transaction Outbox relay; the polling endpoint is disabled.
4. **Workers** switch from `Runner` to `KafkaRunner`.

## Callback ordering regressions

Set `E2E_SDK_FAULTS=1` to enable the SDK release gates. Every reusable SDK
verification workflow enables it for polling/Kafka × REST/gRPC:

```sh
E2E_SDK_FAULTS=1 WE_DISPATCH_MODE=polling ./e2e/run.sh --sdk=go --transport=rest
```

The fault proxy runs between workers and the engine. It injects failures before
callbacks or drops acknowledgements after the engine commits. Tests assert
persisted workflow outcomes/history, handler counts and retry budgets. Polling
also checks claimed capacity, real lease expiry, stale callbacks and shutdown.
Kafka checks actual committed offsets across pending records, crashes,
rebalance and shutdown. Its final poison case first proves the worker is ready,
then requires a decoding-error log at the injected offset and no progress past
that record. Disposable topics/volumes are removed during teardown; offsets are
never reset to bypass the assertion.

`job-callbacks` exercises completion/failure after completion, cancellation,
automatic retry and manual retry. It checks that duplicate failure consumes
one retry, the replacement gets a new job ID, stale callbacks cannot advance
it, and repeated completion preserves the first output.

`job-lease-recovery` waits for the real 30-second lease and 15-second sweeper,
then has the same worker claim the replacement. Old complete/fail callbacks
must not change the new attempt. Expect this scenario to take 30–45 seconds.

Both scenarios drive callbacks through the selected REST/gRPC client using
reserved job types, without database edits or extra API fields. In Kafka mode,
`job-callbacks` consumes actual `JobDispatchEvent` records from the broker;
`job-lease-recovery` runs only in polling mode, where leases apply. PostgreSQL
integration tests additionally decode persisted Kafka outbox events and verify
atomic rollback, duplicate dispatch suppression and callback races.

Run the database regression suite independently from `workflow-engine/`:

```sh
go test -tags integration ./test/invariants/... -count=1
go test -race -tags integration ./test/invariants -run '^TestJob(Callbacks|Retry)' -count=1
```

## Timer and chaining recovery regressions

`timer-recovery` creates interrupting/non-interrupting timers and timers that
should be suppressed after completion or cancellation. It kills the isolated
Compose engine with `SIGKILL`, leaves it offline past the deadlines, then restarts
it and verifies the SDK completes each eligible target exactly once. A second
restart must preserve instance/history snapshots without dispatching again.

`chain-recovery` completes a parent while its target definition is absent,
restarts the engine, then publishes the target. It verifies exactly one child and
one grandchild complete, preserving variables and business key. Another restart
must not create extra instances. The existing `chaining` and `chain-business-key`
scenarios now require actual child completion.

These recovery cases run for every SDK/REST/gRPC/dispatch combination in the
existing CI matrix. They require the local Compose stack started by `run.sh`,
since they deliberately kill and restart its engine service. PostgreSQL and Kafka
stay running; the scenarios do not edit database state or add production test APIs.

The database suite additionally injects failures before commit, terminates a
PostgreSQL connection immediately after timer claim or before commit, and checks
concurrent timer/chain delivery,
parent rollback/commit visibility, child rollback, outbox atomicity, schema and
business-key recovery, and timer/completion races:

```sh
cd workflow-engine
go test -tags integration ./test/invariants/... -count=1
go test -race -tags integration ./test/invariants -run '^(TestTimer|TestWorkflowChain)' -count=1
```

## Variable reference

| Variable | Default | Description |
|----------|---------|-------------|
| `WE_DISPATCH_MODE` | `polling` | Worker dispatch: `polling` or `kafka_outbox` |
| `TRANSPORT` | `rest` | Select explicit `rest` or `grpc` for matched worker and runner gates. |
| `WORKER_TRANSPORT` | `$TRANSPORT` | Set automatically by `run.sh`; external values are overwritten. |
| `E2E_SDK_FAULTS` | `0` | Set `1` for SDK callback, polling and Kafka recovery gates. |
| `ENGINE_REST_PORT` | `18080` | Host port mapped to engine REST (`:8080`) |
| `ENGINE_GRPC_PORT` | `19090` | Host port mapped to engine gRPC (`:9090`) |
| `ENGINE_METRICS_PORT` | `19091` | Host port mapped to engine metrics (`:9091`) |
| `POSTGRES_PORT` | `5433` | Host port mapped to PostgreSQL |

Override multiple at once:

```sh
ENGINE_REST_PORT=28080 ENGINE_GRPC_PORT=29090 TRANSPORT=all WE_DISPATCH_MODE=kafka_outbox ./e2e/run.sh
```

## Logs

Container logs are written to `e2e/logs/` on every run (including successes):

```
e2e/logs/compose.log
e2e/logs/engine.log
e2e/logs/worker-go.log
e2e/logs/worker-python.log
e2e/logs/worker-node.log
e2e/logs/worker-java.log
```

## Structure

```
e2e/
├── run.sh                          # entry point
├── docker-compose-polling.yml      # stack for polling dispatch mode
├── docker-compose-kafka-outbox.yml # stack for kafka_outbox dispatch mode
├── docker-compose-monitor.yml      # local-build override for Monitor E2E
├── scenarios/                      # workflow definition JSON files (per SDK)
│   ├── go/
│   ├── python/
│   ├── node/
│   ├── java/
│   └── loan-approval-workflow/     # shared multi-workflow scenario
├── runner/                         # Go test runner (go run .)
│   ├── main.go                     # entry point; flag parsing; suite orchestration
│   ├── client.go                   # RestEngineClient (implements ClientIface via REST)
│   ├── grpc_client.go              # GrpcEngineClient (implements ClientIface via gRPC)
│   └── scenarios/                  # one .go file per scenario type
│       ├── suite.go                # ClientIface, TestReporter, PollUntilTerminal
│       ├── audit.go                # per-instance audit log writer
│       ├── linear.go
│       ├── decision.go
│       ├── parallel.go
│       ├── user_task.go
│       ├── timer.go
│       ├── wait_signal.go
│       ├── retry_fail.go
│       ├── chaining.go
│       ├── signalwaitstep_completeusertask.go
│       └── loan_approval.go
└── workers/                        # SDK worker implementations
    ├── go/
    ├── python/
    ├── node/
    └── java/
```

## How transport works

There are two independent transport layers:

### Test runner (orchestration plane)

`ClientIface` is the minimal engine API surface used by every scenario:

```go
type ClientIface interface {
    UploadDefinition(ctx, defJSON []byte) error
    StartInstance(ctx, defID string, vars map[string]any) (string, error)
    GetInstance(ctx, id string) (Instance, error)
    GetHistory(ctx, id string) ([]StepExecution, error)
    CompleteUserTaskByStableID(ctx, instanceID, userTaskStepID string, vars map[string]any) error
    SignalWait(ctx, instanceID, waitStepID string, vars map[string]any) error
}
```

`RestEngineClient` (`client.go`) implements it over HTTP/JSON.  
`GrpcEngineClient` (`grpc_client.go`) implements the same interface over gRPC, converting camelCase scenario JSON into proto messages on the fly.

All scenario types run identically against both clients — the transport is transparent to scenario logic.

### SDK workers (job execution plane)

Workers read `WORKER_TRANSPORT` at startup and select their engine client accordingly:

| SDK | REST client | gRPC client |
|-----|-------------|-------------|
| Go | `client.NewRest(...)` | `client.NewGrpc(...)` |
| Node | `RestEngineClient` | `GrpcEngineClient` |
| Java | `RestEngineClient` | `GrpcEngineClient` |
| Python | `RestEngineClient` | `GrpcEngineClient` |

Both clients implement the same `EngineClient` interface (`PollJobs`, `CompleteJob`, `FailJob`), so the `Runner` and `KafkaRunner` work unchanged with either transport.

## Adding a scenario

1. Add a workflow definition JSON under `scenarios/<sdk>/`.
2. Register the required job type handlers in `workers/<sdk>/`.
3. Add a `Run<Scenario>(t, client, scenariosDir, prefix)` function in `runner/scenarios/`.
4. Add the scenario to the `suite` slice in `runner/main.go`'s `runSDKSuite`.

The scenario runs automatically against all enabled transports with no further changes.

`E2E_ENGINE_IMAGE=<local-image>` runs an already built engine candidate (Compose
build is disabled for that service). This pins one image across the 16 SDK
combinations. Health checks use `/readyz` by default; set
`E2E_ENGINE_HEALTH_PATH=healthz` when deliberately testing an older engine.
`E2E_WORKER_IMAGE=<local-image>` similarly pins an existing worker for one named
SDK and skips package preparation/build. Without that override, workers consume
the verified SDK package artifacts. `E2E_LOGS_DIR` selects a directory for each
run's container/audit logs and `images.txt` records the running image identities.

## Optional heartbeat and compatibility gates

Set `E2E_HEARTBEAT=1` for the polling heartbeat suite. It uses a separate registry
so the existing heartbeat-off fault scenarios remain unchanged. The suite checks
a real 90-second handler with a 30-second lease, completion/failure callbacks
pending through renewals, worker death, stale callbacks, cancellation and an
interrupting timer. It asserts workflow outcome, handler count, attempt count,
retry budget and accepted/requested heartbeats. Both REST and gRPC are required.

```sh
E2E_SDK_FAULTS=1 E2E_HEARTBEAT=1 ./e2e/run.sh --sdk=go --transport=rest
```

`E2E_SCENARIOS` selects comma-separated scenario names for a focused compatibility
probe. Unknown names fail. Leave it unset for the full release suite. After
preparing the verified worker and an engine v1.1.2 image, test both configurations:

```sh
export E2E_ENGINE_IMAGE=heartbeat-engine-baseline E2E_ENGINE_HEALTH_PATH=healthz
export E2E_WORKER_IMAGE=verified-go-worker WE_DISPATCH_MODE=polling
E2E_HEARTBEAT=0 E2E_SCENARIOS=linear,retry-fail,transformation \
  E2E_LOGS_DIR=e2e/logs/old-engine-off ./e2e/run.sh --sdk=go --transport=rest
E2E_HEARTBEAT=1 E2E_HEARTBEAT_UNSUPPORTED=1 E2E_SCENARIOS=sdk-heartbeat-unsupported \
  E2E_LOGS_DIR=e2e/logs/old-engine-on ./e2e/run.sh --sdk=go --transport=rest
```

The explicit unsupported probe requires the worker to report a fatal unsupported
heartbeat error before running the handler, with exactly one rejected renewal
and no business callback. SDK verification workflows run these probes for each
polling transport, then measure renewal cost with `perf/heartbeat.py` using the
same worker bytes. None of these focused probes substitutes for the full matrix.

`python3 e2e/mixed-engine.py --baseline=/path/to/engine-v1.1.2 --engine=/path/to/candidate`
starts both native engine binaries against one disposable PostgreSQL database.
It verifies REST claim/callback across replicas in both directions, then sends
renewals for one job to each version: the new replica accepts, the old returns
404 without mutating the job. This gate demonstrates why heartbeat must stay
off until every replica supports it. The separate SDK probes cover unsupported
behavior over both REST and gRPC. Omit `--engine` to build the current engine;
`--output` selects the saved binary identities, results and process/DB logs.
