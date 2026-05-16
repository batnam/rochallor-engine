# Helm Chart — Rochallor Workflow Engine

Deploy the Rochallor Workflow Engine on Kubernetes with a single `helm install` command.

---

## Quick Install

### Polling mode (default)

```bash
# Add the Rochallor Helm repository
helm repo add rochallor oci://ghcr.io/batnam/charts
helm repo update

# Install (provide your Postgres DSN inline — use existingSecret in production)
helm install rochallor rochallor/rochallor \
  --set postgresql.dsn="postgres://workflow:workflow@postgres:5432/workflow?sslmode=require"
```

Engine is ready at `http://<service-ip>:8080`. PostgreSQL migrations run automatically on first startup.

### Kafka + Outbox mode

```bash
# Create secrets first (recommended — keep credentials out of helm values)
kubectl create secret generic rochallor-db \
  --from-literal=dsn="postgres://workflow:workflow@postgres:5432/workflow?sslmode=require"

kubectl create secret generic rochallor-kafka \
  --from-literal=username="<kafka-user>" \
  --from-literal=password="<kafka-password>"

# Install with the kafka overlay
helm install rochallor rochallor/rochallor \
  -f https://raw.githubusercontent.com/batnam/rochallor-engine/main/deploy/helm/rochallor/values-kafka.yaml \
  --set postgresql.existingSecret=rochallor-db \
  --set kafka.existingSecret=rochallor-kafka \
  --set kafka.seedBrokers="kafka-0:9092,kafka-1:9092,kafka-2:9092"
```

---

## Dispatch Modes

| | Polling (default) | Kafka + Outbox |
|---|---|---|
| **Infrastructure** | PostgreSQL only | PostgreSQL + Kafka / Redpanda |
| **Throughput** | < ~3,000 jobs/sec | > ~3,000–5,000 jobs/sec |
| **Delivery** | At-least-once | At-least-once |
| **Scaling** | Sub-linear (PG contention) | ~Linear |
| **Ops overhead** | Minimal | Kafka cluster + topic management |
| **Value `engine.dispatchMode`** | `polling` | `kafka_outbox` |

> **Rule of thumb:** stay on polling unless you have measured throughput that saturates the polling path. Adding Kafka to avoid a measured problem is a net regression.

---

## Configuration Reference

### Core

| Key | Default | Description |
|-----|---------|-------------|
| `replicaCount` | `1` | Number of engine pods. |
| `image.repository` | `ghcr.io/batnam/rochallor-engine` | Container image. |
| `image.tag` | `""` (uses `appVersion`) | Image tag override. |
| `image.pullPolicy` | `IfNotPresent` | Kubernetes imagePullPolicy. |

### Engine

| Key | Default | Description |
|-----|---------|-------------|
| `engine.dispatchMode` | `polling` | `polling` or `kafka_outbox`. |
| `engine.logLevel` | `info` | `debug`, `info`, `warn`, `error`. |
| `engine.auditLogEnabled` | `"true"` | Write to `audit_log` table. |
| `engine.rest.port` | `8080` | HTTP/REST listener port. |
| `engine.grpc.port` | `9090` | gRPC listener port. |
| `engine.metrics.port` | `9091` | Prometheus `/metrics` port. |
| `engine.db.maxConns` | `""` | Max pool connections per replica. Empty = `4 × CPU` (floor 4). |
| `engine.db.minConns` | `""` | Min idle connections. Empty = `0`. |

### PostgreSQL

| Key | Default | Description |
|-----|---------|-------------|
| `postgresql.dsn` | `""` | libpq DSN. Required unless `existingSecret` is set. |
| `postgresql.existingSecret` | `""` | Name of an existing Secret. Skips chart-managed Secret creation. |
| `postgresql.existingSecretKey` | `dsn` | Key inside the Secret. |

### Kafka (`engine.dispatchMode = kafka_outbox`)

| Key | Default | Description |
|-----|---------|-------------|
| `kafka.seedBrokers` | `""` | Comma-separated `host:port` list. |
| `kafka.transport` | `plaintext` | `plaintext` or `sasl_scram_tls`. |
| `kafka.saslMechanism` | `SCRAM-SHA-512` | `SCRAM-SHA-256` or `SCRAM-SHA-512`. |
| `kafka.saslUsername` | `""` | SASL principal (use `existingSecret` in production). |
| `kafka.saslPassword` | `""` | SASL credential (use `existingSecret` in production). |
| `kafka.tlsCaPath` | `""` | Path to custom CA bundle. Empty = system CA. |
| `kafka.tlsServerName` | `""` | TLS SNI override. Empty = broker hostname. |
| `kafka.outboxBatchSize` | `200` | Outbox relay batch size (rows per transaction). |
| `kafka.existingSecret` | `""` | Existing Secret with SASL credentials. |
| `kafka.existingSecretUsernameKey` | `username` | Key for SASL username. |
| `kafka.existingSecretPasswordKey` | `password` | Key for SASL password. |

### Migrations Job

| Key | Default | Description |
|-----|---------|-------------|
| `migrations.enabled` | `false` | Run migrations as a `pre-upgrade` Helm hook. |
| `migrations.activeDeadlineSeconds` | `300` | Job timeout (seconds). |
| `migrations.annotations` | `{}` | Extra annotations (e.g. ArgoCD sync-wave). |

> **Note:** Migrations always run automatically at engine startup whether the Job is enabled or not. Enable the Job only when you want explicit schema promotion before the Deployment rollout.

### Service

| Key | Default | Description |
|-----|---------|-------------|
| `service.type` | `ClusterIP` | Kubernetes Service type. |
| `service.annotations` | `{}` | Extra Service annotations. |

### Autoscaling

| Key | Default | Description |
|-----|---------|-------------|
| `autoscaling.enabled` | `false` | Enable HorizontalPodAutoscaler. |
| `autoscaling.minReplicas` | `1` | Minimum replicas. |
| `autoscaling.maxReplicas` | `5` | Maximum replicas. |
| `autoscaling.targetCPUUtilizationPercentage` | `80` | CPU target. |

### Pod Disruption Budget

| Key | Default | Description |
|-----|---------|-------------|
| `podDisruptionBudget.enabled` | `false` | Enable PodDisruptionBudget. |
| `podDisruptionBudget.minAvailable` | `1` | Minimum available pods. |

---

## Production Checklist

```bash
# 1. Store credentials in Secrets (never inline in values)
kubectl create secret generic rochallor-db \
  --from-literal=dsn="postgres://..."

# 2. Size the connection pool for your replica count
#    Total connections = replicaCount × maxConns
#    Example: 3 replicas × 16 = 48 PG connections

# 3. Enable PDB for zero-downtime upgrades
helm upgrade rochallor rochallor/rochallor \
  --set podDisruptionBudget.enabled=true \
  --set podDisruptionBudget.minAvailable=1

# 4. Enable the migrations hook for controlled schema upgrades
helm upgrade rochallor rochallor/rochallor \
  --set migrations.enabled=true

# 5. Set resource requests to match your observed usage
helm upgrade rochallor rochallor/rochallor \
  --set resources.requests.cpu=200m \
  --set resources.requests.memory=128Mi \
  --set resources.limits.memory=256Mi
```

### Connection pool sizing

```
total_pg_connections = (engine_replicas × engine.db.maxConns)
                     + worker_sdk_connections
                     + other_clients          # modeller, admin tools
```

Set `postgres.max_connections` on your cluster with ~30% headroom above this total.

### Kafka topic pre-creation (kafka_outbox mode)

Kafka topics must exist before jobs are dispatched:

```
Format:     workflow.jobs.<stepId>
Example:    workflow.jobs.process-payment, workflow.jobs.send-email
Partitions: 8 (recommended)
Replication factor: 3 (or match your Kafka cluster's RF)
```

---

## Upgrade Guide

Standard upgrade (migrations run automatically in new pods):

```bash
helm upgrade rochallor rochallor/rochallor --reuse-values
```

Controlled upgrade with explicit migration hook:

```bash
helm upgrade rochallor rochallor/rochallor \
  --reuse-values \
  --set migrations.enabled=true
```

The `pre-upgrade` hook starts the engine, waits for the health endpoint (confirming migrations completed), then shuts it down. Once the hook Job succeeds, Helm proceeds with the Deployment rollout.

---

## Monitoring

The engine exposes Prometheus metrics at `:9091/metrics`. Key metrics:

| Metric | Alert threshold |
|--------|----------------|
| `engine_job_pickup_latency_seconds` (p95) | > 50ms |
| `engine_job_timeout_total` rate | Sudden spike |
| `workflow_job_failed_total` rate | Sustained increase |

Port-forward metrics locally:

```bash
kubectl port-forward svc/rochallor 9091:9091
curl http://localhost:9091/metrics
```

---

## Publishing to Artifact Hub

The `Release` CI job (`.github/workflows/publish.yml`) automatically packages and pushes the chart to `oci://ghcr.io/batnam/charts` on every version tag.

To list the chart on [Artifact Hub](https://artifacthub.io):

1. Sign in at [artifacthub.io](https://artifacthub.io)
2. Go to **Control Panel → Repositories → Add**
3. Set:
   - **Name:** `rochallor`
   - **Kind:** Helm charts (OCI)
   - **URL:** `oci://ghcr.io/batnam/charts`
4. Copy the `repositoryID` returned and paste it into `deploy/helm/artifacthub-repo.yml`

After registration, Artifact Hub scans the OCI registry automatically on each push and reads metadata from `Chart.yaml`.

Users can then install directly:

```bash
helm install rochallor oci://ghcr.io/batnam/charts/rochallor --version 1.0.0
```

---

## Examples

### Minimal (polling, inline DSN — dev only)

```bash
helm install rochallor rochallor/rochallor \
  --set postgresql.dsn="postgres://workflow:workflow@postgres:5432/workflow?sslmode=disable"
```

### Production polling (3 replicas, Secrets, PDB)

```bash
helm install rochallor rochallor/rochallor \
  --set replicaCount=3 \
  --set postgresql.existingSecret=rochallor-db \
  --set podDisruptionBudget.enabled=true \
  --set engine.db.maxConns=8
```

### Kafka + SASL/TLS

```bash
helm install rochallor rochallor/rochallor \
  -f deploy/helm/rochallor/values-kafka.yaml \
  --set postgresql.existingSecret=rochallor-db \
  --set kafka.existingSecret=rochallor-kafka \
  --set kafka.seedBrokers="kafka-0.kafka:9092,kafka-1.kafka:9092,kafka-2.kafka:9092" \
  --set kafka.tlsCaPath=/etc/ssl/certs/kafka-ca.pem
```

### GitOps (ArgoCD / Flux)

```yaml
# argocd Application
spec:
  source:
    repoURL: oci://ghcr.io/batnam/charts
    chart: rochallor
    targetRevision: "1.0.0"
    helm:
      valueFiles:
        - values-kafka.yaml
      parameters:
        - name: postgresql.existingSecret
          value: rochallor-db
        - name: kafka.existingSecret
          value: rochallor-kafka
```
