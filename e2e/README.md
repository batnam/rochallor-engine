# E2E Tests

End-to-end integration tests that spin up the full stack (PostgreSQL, engine, SDK workers) via Docker Compose and run 32 scenario-based tests (8 types × 4 SDKs) against it.

## Prerequisites

- Docker + Docker Compose v2
- Go 1.22+
- Run all commands from the **repo root** (`rochallor-engine/`)

## Run

### Polling Mode (Default)
This is the standard mode where workers poll the engine for jobs.
```sh
# Run all tests
./e2e/run.sh

# Run a single SDK suite
./e2e/run.sh --sdk=go
```

### Kafka Mode (Event-Driven)
In this mode, the engine pushes jobs to Kafka (Redpanda) and workers consume from topics.
```sh
# Run all tests via Kafka
WE_DISPATCH_MODE=kafka_outbox ./e2e/run.sh

# Run a single SDK suite via Kafka
WE_DISPATCH_MODE=kafka_outbox ./e2e/run.sh --sdk=java
```

## Dispatch Modes

The suite supports two fully parallel architectures:

| Mode | Env Var | Description |
|------|---------|-------------|
| **Polling** | `WE_DISPATCH_MODE=polling` | **Default.** Workers call `POST /v1/jobs/poll`. Simplest operation. |
| **Kafka Outbox** | `WE_DISPATCH_MODE=kafka_outbox` | **Event-Driven.** Engine writes to `dispatch_outbox` table, a relay pushes to Kafka, and workers consume from topics. |

When running in `kafka_outbox` mode:
1. A **Redpanda** container is automatically started.
2. A **redpanda-setup** container pre-creates the necessary `workflow.jobs.<jobType>` topics.
3. The **Engine** disables polling endpoints and starts the Transaction Outbox relay.
4. **Workers** switch from the polling `Runner` to the `KafkaRunner`.

## Port defaults

| Service        | Host port |
|----------------|-----------|
| Engine REST    | 18080     |
| Engine gRPC    | 19090     |
| Engine metrics | 19091     |
| PostgreSQL     | 5433      |
| Redpanda Kafka | 9092      |
| Redpanda Admin | 9644      |

Override via env vars before running:

```sh
ENGINE_REST_PORT=28080 POSTGRES_PORT=6433 WE_DISPATCH_MODE=kafka_outbox ./e2e/run.sh
```

## Logs

Container logs are written to `e2e/logs/` on every run (including successes):

```
e2e/logs/compose.log        # all services combined
e2e/logs/engine.log
e2e/logs/worker-go.log
e2e/logs/worker-python.log
e2e/logs/worker-node.log
e2e/logs/worker-java.log
```

## Structure

```
e2e/
├── run.sh                   # entry point
├── docker-compose.yml       # full stack definition (profiles: python, node, java)
├── .env                     # default port configuration
├── scenarios/               # workflow definition JSON files (6 per SDK)
│   ├── go/
│   ├── python/
│   ├── node/
│   └── java/
├── runner/                  # Go test runner (go run .)
│   ├── main.go
│   ├── client.go
│   └── scenarios/           # one .go file per scenario type
│       ├── suite.go
│       ├── linear.go
│       ├── decision.go
│       ├── parallel.go
│       ├── user_task.go
│       ├── timer.go
│       └── retry_fail.go
└── workers/                 # SDK worker implementations
    ├── go/
    ├── python/
    ├── node/
    └── java/
```

## Adding a scenario

1. Add a workflow definition JSON under `scenarios/<sdk>/`.
2. Register the required job type handlers in `workers/<sdk>/`.
3. Add a `Run<Scenario>(t, client, scenariosDir, prefix)` function in `runner/scenarios/`.
4. Add the scenario to the `suite` slice in `runner/main.go`'s `runSDKSuite`.
