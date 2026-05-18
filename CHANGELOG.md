# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Released]

## [engine-v1.0.1] - 2026-05-19

Engine release focused on correlation between chained workflows.

### Added

- **`businessKey` propagation through chained workflows.** When a workflow with `autoStartNextWorkflow: true` reaches `END`, the engine now starts the next workflow with the parent's `businessKey` (previously the child was always created with `business_key = NULL`). Clients can locate the auto-started child by:

  ```http
  GET /v1/instances?definitionId=<child>&businessKey=<bk>
  ```

  This is the supported way to obtain the child instance id needed to complete its `USER_TASK`s. See [Workflow JSON Format — Finding the chained instance id](docs/workflow-format.md#finding-the-chained-instance-id).

- **Uniqueness constraint on `(definitionId, businessKey)` for in-flight instances.** A second `POST /v1/instances` carrying the same `definitionId` and non-empty `businessKey` as an existing instance in `ACTIVE` or `WAITING` status is rejected:
  - **REST:** `HTTP 409 Conflict` — body `{"error":"conflict","reason":"business key already in use by an in-flight instance of this definition"}`.
  - **gRPC:** `status.Code = ALREADY_EXISTS` with the same reason text.

  Re-runs are allowed once the prior instance reaches `COMPLETED`, `FAILED`, or `CANCELLED`. Parent and chained child may carry the same `businessKey` simultaneously (the constraint scopes to each `definitionId` independently).

- New sentinel error `instance.ErrBusinessKeyConflict` for callers that consume the engine packages directly.

### Fixed

- `GetInstanceForUpdate` did not load the `business_key` column, so resume paths (`CompleteJob`, user-task completion, signal-wait) re-hydrated the instance with `BusinessKey = nil`. The fix is part of why `businessKey` propagation works end-to-end: chains kicked off after a `SERVICE_TASK` worker callback (the common case) no longer lose the parent's correlation key.

### Migration

`migrations/0010_uniq_business_key_active.up.sql` adds a partial UNIQUE index — runs automatically on engine startup:

```sql
CREATE UNIQUE INDEX IF NOT EXISTS uniq_workflow_instance_bk_def_active
    ON workflow_instance (business_key, definition_id)
    WHERE business_key IS NOT NULL AND status IN ('ACTIVE', 'WAITING');
```

No data backfill required. Roll back with `0010_uniq_business_key_active.down.sql`.

### Compatibility notes

- Existing rows are unaffected.
- Callers that intentionally started multiple in-flight instances of the same definition with the same `businessKey` will now receive 409 / `ALREADY_EXISTS`. Callers that left `businessKey` empty are unaffected.
- Chained workflows that previously had to be located by listing all instances of the child definition can now use the `businessKey` filter — but only when the parent was started with a non-empty `businessKey`.

### Release

```bash
git tag engine-v1.0.1
git push origin engine-v1.0.1
```

Only the `Publish Engine` workflow runs; image lands at `ghcr.io/<owner>/rochallor-engine:v1.0.1` and `:latest`. See [Release Process](docs/release.md).

## [1.0.0] - 2026-05-16

Rochallor Engine v1.0.0: Lightweight, Language-Agnostic Workflow Orchestration

I'm thrilled to announce the inaugural v1.0.0 release of the Rochallor Workflow Engine!

Rochallor is built from the ground up to provide a lightweight, language-agnostic way to orchestrate long-running business processes. Written in Go and backed by PostgreSQL / Kafka (opt-in), it provides robust state management without the overhead of heavy frameworks.

### Key Features

- **Language-Agnostic SDKs**: First-class support for workers in Go, Java, Node/TypeScript, and Python. Build stateless workers in the language that fits your team.
- **Dual Dispatch Architecture**:
  - *Short-Polling (Default)*: Simple, infrastructure-light polling relying on PostgreSQL `FOR UPDATE SKIP LOCKED`. Perfect for standard workloads.
  - *Kafka + Transaction Outbox (Opt-in)*: High-throughput, event-driven push model for deployments that need to scale beyond database polling limits.
- **Rich Step Types**: Define complex graphs using `SERVICE_TASK`, `USER_TASK`, `DECISION`, `DECISION_TABLE`, `PARALLEL_GATEWAY`, `WAIT`, and more, entirely via JSON definitions.
- **Resilient Execution**: At-least-once delivery semantics, automatic lease expiration, idempotent completions, and built-in retry mechanisms.
- **Stateless by Design**: Workers hold no in-memory state between jobs, making scaling and redeployments trivial.
- **Visual Modeller**: Transform static JSON definitions into interactive, readable diagrams for instant architectural clarity.

Check out our [User Guide](README.md) to spin up the engine and run your first workflow!
