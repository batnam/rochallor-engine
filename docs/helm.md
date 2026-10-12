# Helm Chart — Rochallor Workflow Engine

Deploy the Rochallor Workflow Engine on Kubernetes with a single `helm install` command.

---

## Quick Install

### Polling mode (default)

```bash
# Install (provide your Postgres DSN inline — use existingSecret in production)
helm install rochallor oci://ghcr.io/batnam/charts/rochallor \
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
helm install rochallor oci://ghcr.io/batnam/charts/rochallor \
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
helm upgrade rochallor oci://ghcr.io/batnam/charts/rochallor \
  --set podDisruptionBudget.enabled=true \
  --set podDisruptionBudget.minAvailable=1

# 4. Enable the migrations hook for controlled schema upgrades
helm upgrade rochallor oci://ghcr.io/batnam/charts/rochallor \
  --set migrations.enabled=true

# 5. Set resource requests to match your observed usage
helm upgrade rochallor oci://ghcr.io/batnam/charts/rochallor \
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
helm upgrade rochallor oci://ghcr.io/batnam/charts/rochallor --version <new-version> --reuse-values
```

Controlled upgrade with explicit migration hook:

```bash
helm upgrade rochallor oci://ghcr.io/batnam/charts/rochallor --version <new-version> \
  --reuse-values \
  --set migrations.enabled=true
```

The `pre-upgrade` hook runs `/engine -migrate-only` and reports the process exit code. It exits after applying migrations without starting HTTP/gRPC servers, Kafka consumers or sweepers. Once the hook Job succeeds, Helm proceeds with the Deployment rollout. Use an engine image that supports this command, as described in the [candidate compatibility guidance](#readiness-engine-version-and-migration-hooks-chart-020-candidate).

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

The [Helm publish workflow](https://github.com/batnam/rochallor-engine/blob/main/.github/workflows/publish-helm.yml) verifies, packages and pushes the chart to `oci://ghcr.io/batnam/charts` on `helm-v<x.y.z>` tags. The chart version is independent of the engine version pinned by `appVersion` or `image.tag`.

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
helm install rochallor oci://ghcr.io/batnam/charts/rochallor
```

---

## Examples

### Minimal (polling, inline DSN — dev only)

```bash
helm install rochallor oci://ghcr.io/batnam/charts/rochallor \
  --set postgresql.dsn="postgres://workflow:workflow@postgres:5432/workflow?sslmode=disable"
```

### Production polling (3 replicas, Secrets, PDB)

```bash
helm install rochallor oci://ghcr.io/batnam/charts/rochallor \
  --set replicaCount=3 \
  --set postgresql.existingSecret=rochallor-db \
  --set podDisruptionBudget.enabled=true \
  --set engine.db.maxConns=8
```

### Kafka + SASL/TLS

```bash
helm install rochallor oci://ghcr.io/batnam/charts/rochallor \
  -f deploy/helm/rochallor/values-kafka.yaml \
  --set postgresql.existingSecret=rochallor-db \
  --set kafka.existingSecret=rochallor-kafka \
  --set kafka.seedBrokers="kafka-0.kafka:9092,kafka-1.kafka:9092,kafka-2.kafka:9092" \
  --set kafka.tlsCaPath=/etc/ssl/certs/kafka-ca.pem
```

### GitOps (ArgoCD / Flux)

> **Note:** The chart is distributed via OCI only (`oci://ghcr.io/batnam/charts/rochallor`).
> A traditional Helm HTTP repo (GitHub Pages + `helm repo add`) is not provided — add one via
> `helm/chart-releaser-action` if shorthand `repo/chart` syntax is needed.



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

## Readiness, engine version and migration hooks (chart 0.2.0 candidate)

Chart and engine versions are independent. `Chart.appVersion` pins `v1.2.0`, the
candidate engine providing `/readyz` and `-migrate-only`; publish that verified
engine before publishing this chart. A Helm tag changes only the chart version.
`image.tag` overrides the engine tag for an explicitly tested pair. Do not point
this chart at an older engine lacking those capabilities.

Readiness uses `/readyz` (2 s timeout, 5 s period, two failures); liveness stays on
`/healthz`. The startup probe allows roughly 95 s for migration/startup before
liveness is enabled. Database failure removes a pod from service without a
liveness restart. An initialized outbox runtime may remain ready while Kafka is
unavailable; monitor backlog/oldest-row age and relay failures separately.

`migrations.enabled=true` runs `/engine -migrate-only` as a **pre-upgrade** hook;
installation still uses normal engine startup migrations. The Job and Deployment
use the same engine image. Only the DB DSN is supplied to the Job: Kafka and
runtime listener configuration are unnecessary. A nonzero exit or the 300 s
job deadline fails the hook and blocks Deployment rollout.

An `existingSecret` must exist with the intended DSN **before** upgrade. For a
chart-managed DSN, a hook Secret of weight -10 provides the incoming value before
the Job of weight -5; the ordinary release Secret is not updated until later.
This hook Secret is replaced on each upgrade and retained for the next upgrade;
Helm does not remove hook resources on uninstall. After uninstall, explicitly
delete `<fullname>-migrations-db` when it is no longer needed. Failed migration
Jobs retain logs until the next upgrade. Avoid overriding reserved `helm.sh/*`
hook annotations with `migrations.annotations`.

Roll back the chart while retaining the newer engine first; only then restore
an older engine with its compatible `/healthz` chart. Keep the migrated schema
when compatible. Never use `-migrate-down` as application rollback: it drops all
migrated schema.

Verification packages the chart once, checks both dispatch modes and image
overrides, then tests a real old→new upgrade, database outage/recovery, empty/
pending/no-change migrations, failed hooks blocking rollout and rollback in a
disposable kind cluster. Publication downloads and pushes those same bytes.

```sh
helm package deploy/helm/rochallor --destination /tmp/helm-candidate
python3 deploy/helm/test/verify.py \
  --chart=/tmp/helm-candidate/rochallor-0.2.0.tgz \
  --image=rochallor-engine:local --output=testresults/helm
```

Prerequisites: Helm, Docker with API 1.48+, kubectl, kind 0.33.0, Python with PyYAML; candidate,
Postgres and baseline engine images already available locally. The runner creates
a dedicated cluster and kubeconfig and deletes them on success/failure. kind
installation follows its [official quick start](https://kind.sigs.k8s.io/docs/user/quick-start/).

The cluster verifier uses [Docker platform-specific image export](https://docs.docker.com/reference/cli/docker/image/save/)
to import the exact local image platform, including registry images whose other
architecture layers have not been pulled.
