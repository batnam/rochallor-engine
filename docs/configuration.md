# Configuration Reference

All engine configuration is read from environment variables. An optional YAML file at `/etc/workflow/engine.yaml` provides a secondary source (env vars always win).

| Variable | Default | Description |
|----------|---------|-------------|
| `WE_POSTGRES_DSN` | *(required)* | libpq connection string, e.g. `postgres://user:pass@host:5432/db?sslmode=disable` |
| `WE_DISPATCH_MODE` | `polling` | `polling` or `kafka_outbox`. Opts into event-driven dispatch. |
| `WE_KAFKA_TRANSPORT` | `plaintext` | `plaintext` or `sasl_scram_tls`. Required if `WE_DISPATCH_MODE=kafka_outbox`. |
| `WE_KAFKA_SEED_BROKERS` | `""` | Comma-separated list of brokers, e.g. `localhost:9092`. Required if `kafka_outbox`. |
| `WE_KAFKA_SASL_MECHANISM` | `SCRAM-SHA-512` | `SCRAM-SHA-256` or `SCRAM-SHA-512`. Required for `sasl_scram_tls`. |
| `WE_KAFKA_SASL_USERNAME` | `""` | Required for `sasl_scram_tls`. |
| `WE_KAFKA_SASL_PASSWORD` | `""` | **ENV ONLY, NEVER ON DISK**. Required for `sasl_scram_tls`. |
| `WE_KAFKA_TLS_CA_PATH` | `""` | Optional CA path for self-signed brokers in `sasl_scram_tls`. |
| `WE_KAFKA_TLS_SERVER_NAME` | `""` | Optional TLS ServerName override. |
| `WE_OUTBOX_BATCH_SIZE` | `200` | Relay drain batch limit. |
| `WE_REST_PORT` | `8080` | HTTP/REST listener port |
| `WE_GRPC_PORT` | `9090` | gRPC server port |
| `WE_METRICS_PORT` | `9091` | Prometheus `/metrics` endpoint port |
| `WE_LOG_LEVEL` | `info` | Minimum log level: `debug`, `info`, `warn`, `error` |
| `WE_AUDIT_LOG_ENABLED` | `true` | Record engine actions to `audit_log` table |
| `DB_MAX_CONNS` | `runtime.NumCPU() * 4` (floor 4) | Maximum pgxpool connections. Tune per deployment to bound Postgres `max_connections` usage across replicas. |
| `DB_MIN_CONNS` | `0` | Minimum idle pgxpool connections held open. `0` preserves pgxpool's on-demand behaviour. Must be `≤ DB_MAX_CONNS`. |

## Kafka Partition Assignment Strategy

The Workflow Engine and all SDKs (Go, Java, Node.js, Python) utilize the `CooperativeStickyAssignor` (or `cooperative-sticky` in `librdkafka`-based clients) by default. 

This strategy enables incremental rebalancing, allowing consumers to keep their assigned partitions during a rebalance if they are not being moved to another member. This avoids "stop-the-world" pauses and is highly recommended for stable operations in Kubernetes environments.

While the default is standardized for stability, users can override this in the SDKs by providing custom Kafka properties during initialization if absolutely necessary.

## Engine performance metrics

The engine exposes the following Prometheus series (in addition to `workflow_*`, `job_*`, `http_*`, and `grpc_*`):

| Metric | Type | Purpose |
|--------|------|---------|
| `engine_db_transaction_duration_seconds` | Histogram (`tx_type`) | Wall-clock Begin → Commit/Rollback per logical engine transaction |
| `engine_db_lock_wait_duration_seconds` | Histogram (`operation`) | Pre-acquire wait for `FOR UPDATE` / `FOR UPDATE SKIP LOCKED` |
| `engine_job_timeout_total` | Counter | Jobs whose lease expired and were recovered by the lease sweeper |
| `engine_job_pickup_latency_seconds` | Histogram | End-to-end `time.Since(job.created_at)` at successful worker claim. Primary signal for the < 50ms-p95 target. |

## Multi-replica coordination

Sweepers (`job` lease expiry and `boundary_event_schedule` timer firing) are gated behind distinct PostgreSQL advisory locks (`pg_try_advisory_lock`) so that across N replicas only one replica sweeps per interval. No cluster configuration is required — every engine replica tries to acquire the lock each tick; losers skip silently until the current leader disconnects.

## Partial JSONB updates

Updates to `workflow_instance.variables` emit chained `jsonb_set` calls per dirty top-level key instead of rewriting the entire JSONB blob. For ≥ 256 KB payloads this reduces WAL volume by ≥ 40% on single-key updates.
