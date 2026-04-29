# Development Guide

## Repository layout

```
rochallor-engine/
├── proto/
│   └── workflow/v1/engine.proto        # Canonical wire contract (source of truth)
│
├── workflow-engine/                    # Core Go engine
│   ├── cmd/engine/main.go              # Entry point
│   ├── api/
│   │   ├── gen/workflow/v1/            # Generated proto code (not committed)
│   │   └── openapi/rest-openapi.yaml   # REST OpenAPI spec
│   ├── internal/
│   │   ├── api/                        # REST + gRPC server handlers
│   │   ├── boundary/                   # Timer boundary event sweeper
│   │   ├── config/                     # Environment-variable config loader
│   │   ├── definition/                 # Definition parsing, validation, repository
│   │   ├── expression/                 # Boolean expression evaluator (lexer + parser)
│   │   ├── id/                         # ULID generators
│   │   ├── instance/                   # Workflow instance lifecycle (core logic)
│   │   ├── job/                        # Job polling, lease sweeper, retry
│   │   ├── obs/                        # Logger + Prometheus metrics
│   │   └── storage/postgres/           # DB connection pool + migration runner
│   ├── migrations/                     # Numbered SQL migration files (up + down)
│   ├── test/
│   │   ├── contract/                   # REST contract replay tests
│   │   ├── fixtures/                   # Shared JSON fixtures
│   │   └── integration/                # End-to-end lifecycle tests (testcontainers)
│   ├── Makefile
│   └── docker-compose.yml              # PostgreSQL for local dev
│
├── workflow-sdk-go/                    # Go SDK (REST + gRPC)
├── workflow-sdk-java/                  # Java SDK (REST + gRPC)
├── workflow-sdk-node/                  # Node/TypeScript SDK (REST + gRPC)
└── workflow-sdk-python/                # Python SDK (REST only)
```

---

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| Go | 1.22+ | [go.dev/dl](https://go.dev/dl) |
| Docker + Compose | 24+ | [docs.docker.com](https://docs.docker.com/get-docker/) |
| protoc | 3.21+ | `brew install protobuf` / `apt install protobuf-compiler` |
| protoc-gen-go | latest | `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` |
| protoc-gen-go-grpc | latest | `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest` |
| golangci-lint | latest | `brew install golangci-lint` |
| Python | 3.10+ | Python SDK development |
| Node.js | 20+ | Node/TypeScript SDK development |
| Java | 21+ | Java SDK development |
| Gradle | 8+ (wrapper included) | Java SDK — no install needed, use `./gradlew` |

---

## Initial setup

```bash
git clone https://github.com/batnam/rochallor-engine.git
cd rochallor-engine

# 1. Start PostgreSQL
docker compose -f workflow-engine/docker-compose.yml up -d postgres

# 2. Generate proto code (required before first build)
cd workflow-engine
make proto-gen

# 3. Build and start the engine
export WE_POSTGRES_DSN="postgres://workflow:workflow@localhost:5434/workflow?sslmode=disable"
make run
```

The engine runs migrations automatically on startup. Ports:

| Port | Protocol | Purpose |
|------|----------|---------|
| `8080` | HTTP | REST API |
| `9090` | gRPC | gRPC API |
| `9091` | HTTP | Prometheus `/metrics` |

---

## Engine development

All commands below run from `workflow-engine/`.

### Build

```bash
make build
# Output: bin/engine
```

### Run

```bash
export WE_POSTGRES_DSN="postgres://workflow:workflow@localhost:5432/workflow?sslmode=disable"
make run          # compiles then starts the engine
```

### Lint

```bash
make lint         # runs golangci-lint on ./...
```

---

## Testing

### Unit tests

Fast, no external dependencies. Run them often.

```bash
cd workflow-engine
go test ./internal/...
```

To run a single package:

```bash
go test ./internal/expression/...
go test ./internal/definition/...
```

To run a single test:

```bash
go test -run TestEvaluate ./internal/expression/...
```

With race detector (recommended before opening a PR):

```bash
go test -race ./internal/...
```

---

Available integration test suites:

| File | What it covers |
|------|---------------|
| `lifecycle_loan_application_test.go` | Full loan workflow — SERVICE_TASK, DECISION, parallel gateway |
| `lifecycle_parallel_test.go` | PARALLEL_GATEWAY + JOIN_GATEWAY fan-out / fan-in |
| `lifecycle_transformation_test.go` | TRANSFORMATION step, `${now()}`, boolean expressions |
| `lifecycle_document_verification_test.go` | USER_TASK, boundary timer events |
| `boundary_timer_test.go` | Timer sweeper firing and re-routing |
| `job_lease_sweeper_test.go` | Lease expiry and job reclaim |
| `job_retry_test.go` | Retry budget decrement and exhaustion |
| `job_poll_contention_test.go` | Concurrent worker poll under SKIP LOCKED |
| `definition_repository_test.go` | Definition upload, version increment, retrieval |
| `migration_test.go` | All migrations run up and down cleanly |

---

### Contract tests

Validates the running engine against the REST OpenAPI spec. Requires the engine to be up.

```bash
# Terminal 1: start the engine
export WE_POSTGRES_DSN="postgres://workflow:workflow@localhost:5432/workflow?sslmode=disable"
cd workflow-engine && make run

# Terminal 2: run contract replay
cd workflow-engine
WE_REST_PORT=8080 make contract-replay
```

---

### SDK tests

```bash
# Go SDK
cd workflow-sdk-go
go test ./...

# Go SDK with race detector
go test -race ./...

# Python SDK
cd workflow-sdk-python
pip install -e ".[dev]"
pytest tests/

# Node/TypeScript SDK
cd workflow-sdk-node
npm install
npm test

# Java SDK
cd workflow-sdk-java
./gradlew test

# Java SDK with verbose output
./gradlew test --info
```

---

## Database migrations

Migrations live in `workflow-engine/migrations/` as numbered SQL files (`0001_*.up.sql` / `0001_*.down.sql`). The engine runs them automatically at startup via the embedded migration runner.

### Apply all pending migrations

```bash
export WE_POSTGRES_DSN="postgres://workflow:workflow@localhost:5432/workflow?sslmode=disable"
cd workflow-engine
make migrate
```

### Roll back all migrations

```bash
make migrate-down
```

### Adding a new migration

1. Create two files in `workflow-engine/migrations/`:
   - `NNNN_description.up.sql` — forward migration
   - `NNNN_description.down.sql` — rollback (must cleanly undo the `.up.sql`)
2. Use the next sequential number (e.g. `0008_` if `0007_` already exists).
3. Add the filename pair to `workflow-engine/migrations/embed.go` so the Go embed includes it.
4. Run `make migrate` to verify the up migration applies cleanly.
5. Run `make migrate-down && make migrate` to verify the round-trip.

---

## Proto code generation

`proto/workflow/v1/engine.proto` is the single source of truth for the gRPC API. Generated Go code is **not committed** — regenerate it whenever you change the proto file.

```bash
cd workflow-engine
make proto-gen
# Writes:  api/gen/workflow/v1/engine.pb.go
#          api/gen/workflow/v1/engine_grpc.pb.go
```

`make proto-gen` checks for `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc` before running and prints an install hint if any are missing.

### Workflow for proto changes

1. Edit `proto/workflow/v1/engine.proto`.
2. Run `make proto-gen` to regenerate Go stubs.
3. Update `workflow-engine/api/openapi/rest-openapi.yaml` to mirror the change.
4. Update the REST handler in `internal/api/rest/` and gRPC handler in `internal/api/grpc/`.
5. Update any affected SDK clients (each SDK has its own proto copy under `workflow-sdk-*/internal/gen/`).
6. Run `go test ./...` + `make test-integration` to confirm nothing is broken.

---

## Environment variables

All engine configuration is via environment variables. An optional YAML file at `/etc/workflow/engine.yaml` provides a secondary source — env vars always win.

| Variable | Default | Description |
|----------|---------|-------------|
| `WE_POSTGRES_DSN` | *(required)* | libpq connection string, e.g. `postgres://user:pass@host:5432/db?sslmode=disable` |
| `WE_REST_PORT` | `8080` | HTTP/REST listener port |
| `WE_GRPC_PORT` | `9090` | gRPC server port |
| `WE_METRICS_PORT` | `9091` | Prometheus `/metrics` endpoint port |
| `WE_LOG_LEVEL` | `info` | Minimum log level: `debug`, `info`, `warn`, `error` |
| `WE_AUDIT_LOG_ENABLED` | `true` | Record engine actions to `audit_log` table |

For local development, set them in your shell or a `.env` file (sourced manually — the engine does not auto-load `.env`):

```bash
export WE_POSTGRES_DSN="postgres://workflow:workflow@localhost:5432/workflow?sslmode=disable"
export WE_LOG_LEVEL=debug
```

---

## Useful development workflows

### Full check before a PR

```bash
cd workflow-engine

make lint
go test -race ./internal/...
make test-integration
WE_REST_PORT=8080 make contract-replay   # requires engine running in another terminal
```

### Iterate on the expression evaluator

The expression package has no external dependencies and a comprehensive test suite — fast feedback loop:

```bash
cd workflow-engine
go test -v -run TestEvaluate ./internal/expression/...
```

### Iterate on a specific lifecycle scenario

```bash
cd workflow-engine
go test -v -tags integration -run TestLifecycleLoanApplication ./test/integration/...
```

### Check Prometheus metrics locally

While the engine is running:

```bash
curl -s http://localhost:9091/metrics | grep workflow_engine
```

Key metrics:

| Metric | Description |
|--------|-------------|
| `workflow_engine_job_poll_latency_seconds` | End-to-end latency from job creation to first lock acquisition |
| `workflow_engine_job_lock_conflicts_total` | Poll rounds that found zero available jobs (all locked by other workers) |

---

## Internal package map

| Package | Responsibility |
|---------|---------------|
| `internal/instance` | Core lifecycle: `Start`, `Advance`, `CompleteJobAndAdvance`, `DispatchBoundaryStep`. All state mutations happen in a single PostgreSQL transaction. |
| `internal/definition` | Definition parsing (`parser.go`), type definitions (`types.go`), validator (`validator.go`), and repository (DB read/write). |
| `internal/expression` | Lexer (`lexer.go`) + recursive descent parser/evaluator (`evaluator.go`). Evaluates boolean expressions against workflow variable maps. |
| `internal/job` | `poll.go` — `FOR UPDATE SKIP LOCKED` job claim. `lease_sweeper.go` — returns expired locks to `UNLOCKED`. `retry.go` — decrements retry budget and re-queues or fails. |
| `internal/boundary` | Timer sweeper: scans `boundary_event_schedule` for fired timers and calls `DispatchBoundaryStep`. |
| `internal/api/rest` | Chi router, REST handlers, request/response structs, format guard middleware. |
| `internal/api/grpc` | gRPC server implementation, bridges proto types to service layer. |
| `internal/config` | Reads `WE_*` env vars; merges with optional YAML file. |
| `internal/obs` | `slog`-based structured logger and Prometheus metrics registration. |
| `internal/storage/postgres` | `pgxpool` setup and embedded migration runner. |
| `internal/id` | ULID generators for instance IDs, step execution IDs, job IDs, etc. |

---

## Debugging

### Increase log verbosity

```bash
export WE_LOG_LEVEL=debug
make run
```

Debug logs include transaction boundaries, step dispatch decisions, expression evaluation results, and job poll outcomes.

### Inspect the database directly

```bash
docker exec -it $(docker ps -qf name=postgres) psql -U workflow -d workflow
```

Useful queries:

```sql
-- View all active instances and their current steps
SELECT id, status, current_step_ids, started_at FROM workflow_instance WHERE status = 'ACTIVE';

-- View jobs pending pickup
SELECT id, job_type, status, retries_remaining, created_at FROM job WHERE status = 'UNLOCKED' ORDER BY created_at;

-- View step execution history for an instance
SELECT step_id, step_type, status, started_at, ended_at, failure_reason
  FROM step_execution WHERE instance_id = '<id>' ORDER BY started_at;

-- View upcoming boundary timer events
SELECT instance_id, target_step_id, fire_at, interrupting
  FROM boundary_event_schedule WHERE fired_at IS NULL ORDER BY fire_at;
```

### Common issues

**`api/gen/workflow/v1/*.pb.go` not found / build fails**

Generated files are not committed. Run `make proto-gen` once after cloning.

**Integration tests fail with "cannot connect to Docker"**

testcontainers-go needs the Docker daemon running. Check with `docker ps`.

**`make migrate` fails with "relation already exists"**

The migration was partially applied. Connect to PostgreSQL and manually check `schema_migrations`, then roll back with `make migrate-down` before re-running.

**Worker polls but never picks up jobs**

Check that the `jobType` registered in your handler matches exactly (case-sensitive) the `jobType` field in the workflow definition step.

**Instance stays `ACTIVE` after all steps complete**

The workflow definition is missing an `END` step reachable from every terminal branch, or a `DECISION` step has no matching branch. Check `failure_reason` in `workflow_instance` and `step_execution` tables.

---

## End-to-End Testing

The project maintains a comprehensive E2E test suite that verifies the engine and SDKs work together across multiple languages.

### Purpose
- Validate engine orchestration logic (Parallel Gateways, Decisions, Chaining).
- Verify SDK worker implementations in Go, Java, Node.js, and Python.
- Ensure cross-SDK compatibility and consistent engine behavior.

### Execution
For detailed instructions on running the E2E suite, refer to the [E2E README](../e2e/README.md).

Quick run command (requires Docker):
```bash
./e2e/run.sh --sdk=go
```
