# E2E Tests

End-to-end integration tests that spin up the full stack (PostgreSQL, engine, SDK workers) via Docker Compose and run scenario-based tests against it.

Each scenario type runs against every combination of **SDK** (go, python, node, java) and **transport** (rest, grpc), giving full coverage of both API surfaces.

## Prerequisites

- Docker + Docker Compose v2
- Go 1.22+
- Run all commands from the **repo root** (`rochallor-engine/`)

## Quick start

```sh
# REST transport, polling dispatch, all SDKs (default)
./e2e/run.sh

# Single SDK
./e2e/run.sh --sdk=go
```

## Transport

The `TRANSPORT` variable controls which engine API the test runner uses to orchestrate workflows (upload definitions, start/query instances, complete user tasks, signal waits). Workers always use their own SDK client internally.

| Value | Description |
|-------|-------------|
| `rest` | **Default.** All orchestration calls go through the REST API (`http://localhost:ENGINE_REST_PORT`). |
| `grpc` | All orchestration calls go through the gRPC API (`localhost:ENGINE_GRPC_PORT`). |
| `all` | Run the full suite twice — once per transport — in the same stack. |

Set via environment variable or `--transport` flag:

```sh
# gRPC transport
TRANSPORT=grpc ./e2e/run.sh

# Both transports in one run
TRANSPORT=all ./e2e/run.sh

# Flag form
./e2e/run.sh --transport=grpc --sdk=go
```

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

# Kafka dispatch + both transports
WE_DISPATCH_MODE=kafka_outbox TRANSPORT=all ./e2e/run.sh --sdk=java
```

When running in `kafka_outbox` mode:
1. A **Kafka** container is automatically started.
2. A **kafka-setup** container pre-creates the necessary `workflow.jobs.<jobType>` topics.
3. The **engine** disables the polling endpoint and starts the Transaction Outbox relay.
4. **Workers** switch from `Runner` to `KafkaRunner`.

## Variable reference

| Variable | Default | Description |
|----------|---------|-------------|
| `WE_DISPATCH_MODE` | `polling` | Worker dispatch: `polling` or `kafka_outbox` |
| `TRANSPORT` | `rest` | Test runner transport: `rest`, `grpc`, or `all` |
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

All 10 scenario types run identically against both clients — the transport is transparent to scenario logic.

## Adding a scenario

1. Add a workflow definition JSON under `scenarios/<sdk>/`.
2. Register the required job type handlers in `workers/<sdk>/`.
3. Add a `Run<Scenario>(t, client, scenariosDir, prefix)` function in `runner/scenarios/`.
4. Add the scenario to the `suite` slice in `runner/main.go`'s `runSDKSuite`.

The scenario runs automatically against all enabled transports with no further changes.
