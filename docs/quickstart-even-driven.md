# Quickstart: Enable Event-Driven Dispatch Locally

**Feature**: Event-Driven Job Dispatch via Kafka + Transaction Outbox (Opt-In)

This is the operator-facing walkthrough for turning on `kafka_outbox` mode on a developer machine and verifying it end-to-end against the existing engine. It is also what the integration tests under `workflow-engine/test/kafka_outbox/` will encode.

---

## Prerequisites

- Docker running (used for Postgres + Kafka).
- Go 1.26.2 and `make` installed.
- You have run the feature's migration. With the existing migration runner this is automatic on engine boot; to run explicitly: `cd workflow-engine && make migrate-up`.

---

## Path A — Default polling mode still works (regression check)

Do this first. It proves the polling path is unaffected.

```bash
cd workflow-engine
docker compose up -d postgres              # no kafka profile
make run                                   # WE_DISPATCH_MODE unset
```

In another shell, register a trivial definition and create an instance via the existing REST API. A Go SDK worker running in polling mode claims and completes the job exactly as it does today. No Kafka, no outbox.

Expected:
- `SELECT count(*) FROM dispatch_outbox;` returns `0` (migration created the table; it is not used).
- No `workflow_engine_dispatch_*` metrics are exposed on `:9091/metrics`.
- Logs contain no references to Kafka.

---

## Path B — Opt in to event-driven mode

### B.1. Start Postgres + Redpanda

Use the Kafka docker-compose profile that this feature adds:

```bash
cd workflow-engine
docker compose --profile kafka up -d postgres redpanda
```

Redpanda is used instead of Apache Kafka for local dev because it boots in ~2s, speaks the same wire protocol, and has no JVM dependency. Production stays on Kafka.

### B.2. Pre-create the topics

Topics are not auto-created by the engine. When running in `kafka_outbox` mode, whenever you register a step handler into the Registry, the engine will push job messages to a corresponding Kafka topic formatted as `workflow.jobs.<step_id>`.

Therefore, for each new `job_type` (or `step.id`) registered, you **must ensure there is a GitOps or IaC (Infrastructure as Code) step to explicitly create this topic on Kafka** before it can be used.

For example, for the `send-email` job, you need to create the `workflow.jobs.send-email` topic:

```bash
kafka-topics --create --if-not-exists --bootstrap-server kafka:29092 --partitions 1 --replication-factor 1 --topic workflow.jobs.send-email
```

### B.3. Export Kafka env vars (plaintext for local mode)

```bash
export WE_POSTGRES_DSN='postgres://workflow:workflow@localhost:5432/workflow'
export WE_DISPATCH_MODE=kafka_outbox
export WE_KAFKA_TRANSPORT=plaintext
export WE_KAFKA_SEED_BROKERS=localhost:9092
```

### B.4. Boot the engine

```bash
make run
```

Expected boot-time validation:

```
[dispatch] mode=kafka_outbox
[dispatch] kafka transport=plaintext brokers=[localhost:9092]
[dispatch] validating topics: workflow.jobs.send-email
[dispatch] all topics present
[dispatch] advisory-lock handshake ok
[dispatch] migration 0009_dispatch_outbox applied
[dispatch] relay started; this replica is leader=true
```

If any of those checks fail (bad creds, missing topic, unreachable broker), the engine exits with a non-zero status and a message naming the specific cause.

### B.5. Verify end-to-end

1. Create a workflow instance that emits a `send-email` job.
2. Observe:
   - `SELECT count(*) FROM dispatch_outbox;` briefly shows `1`, then drops to `0` once the relay publishes.
   - A message appears on `workflow.jobs.send-email` (inspect with `rpk topic consume workflow.jobs.send-email -n 1`).
   - A Go SDK worker running `KafkaRunner` picks up the message, runs the handler, and calls `complete_job` via the existing REST/gRPC endpoint.
   - The engine records the job as `COMPLETED` exactly as in polling mode.
   - An `audit_log` row with action `DISPATCHED_VIA_BROKER` was written at publish time.

### B.6. Metrics to eyeball at `/metrics`

- `workflow_engine_dispatch_outbox_backlog` — should be 0 at steady state.
- `workflow_engine_dispatch_relay_publish_total{result="success"}` — increments per publish.
- `workflow_engine_dispatch_relay_leader` — `1` on this replica.
- `workflow_engine_kafka_consumer_lag_seconds{topic="workflow.jobs.send-email", group="workflow.workers.send-email"}` — low single-digit seconds while a worker is running.

Polling-mode metrics (`job_pickup_latency`, `lock_wait_seconds`) are absent in this mode — that's intentional (FR-010).

---

## Path C — Secured mode (SASL/SCRAM over TLS)

Only the env vars change. The rest of the walkthrough is the same. Use a broker that's configured for TLS + SASL:

```bash
export WE_DISPATCH_MODE=kafka_outbox
export WE_KAFKA_TRANSPORT=sasl_scram_tls
export WE_KAFKA_SEED_BROKERS=broker-1.example.com:9093,broker-2.example.com:9093
export WE_KAFKA_SASL_MECHANISM=SCRAM-SHA-512
export WE_KAFKA_SASL_USERNAME=workflow-engine
export WE_KAFKA_SASL_PASSWORD='*** env-only, never on disk ***'
# Optional: WE_KAFKA_TLS_CA_PATH=/etc/ssl/kafka-ca.pem
```

Expected boot differences:

```
[dispatch] kafka transport=sasl_scram_tls mechanism=SCRAM-SHA-512
[dispatch] TLS handshake ok
[dispatch] SASL auth ok as user=workflow-engine
```

A missing SASL password, missing TLS CA when a self-signed broker is used, or a reachable-but-unauthorized principal all produce a loud fatal startup error with a specific cause message. No silent fallback to plaintext.

---

## Path D — Failover drill

1. Run two engine replicas (both pointed at the same Postgres).
2. One of them will hold the advisory lock and emit `workflow_engine_dispatch_relay_leader=1`. The other emits `0` and does no Kafka work.
3. Kill the leader (or just close its DB connection).
4. Within one retry interval (default 5s), the other replica acquires the lock. Outbox drain continues without operator action.

This is the mechanism that gives the feature HA without adding new infrastructure.

---

## Path E — Roll back to polling

1. Stop the engine.
2. `unset WE_DISPATCH_MODE` (or set it to `polling`).
3. Restart.

Expected behaviour:
- Engine boots without initialising any Kafka client.
- Any rows remaining in `dispatch_outbox` are **not** published — they stay there as "historical" rows. The polling path is now active. Jobs created from this point on are claimed via the existing `FOR UPDATE SKIP LOCKED` poll. Any pre-existing jobs that were already in `UNLOCKED` state are also picked up.
- If you want to drain the outbox before switching back, flip back to `kafka_outbox` long enough for the backlog gauge to reach 0, then switch. (Operator choice; neither behaviour loses jobs)

---