#!/usr/bin/env python3
"""Verify one packaged chart on an isolated, disposable kind cluster."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import time
import uuid

import yaml

ROOT = Path(__file__).resolve().parents[3]


def run(*args, input=None, check=True, timeout=180, env=None):
    result = subprocess.run(args, input=input, text=True, capture_output=True,
                            timeout=timeout, env=env)
    if check and result.returncode:
        raise RuntimeError(f"{args[0]} failed ({result.returncode}): {result.stdout}\n{result.stderr}")
    return result


def eventually(check, seconds=90):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if check():
            return
        time.sleep(1)
    raise AssertionError("condition did not become true before deadline")


def render_checks(chart, output):
    metadata = yaml.safe_load(run("helm", "show", "chart", str(chart)).stdout)
    assert metadata["version"] != metadata["appVersion"].removeprefix("v"), metadata
    for mode in ("polling", "kafka_outbox"):
        for tag in (None, "v9.8.7-test"):
            flags = ["--set", f"engine.dispatchMode={mode}", "--set", "migrations.enabled=true",
                     "--set", "postgresql.dsn=postgres://workflow:workflow@db/workflow?sslmode=disable"]
            if tag:
                flags += ["--set", f"image.tag={tag}"]
            run("helm", "lint", str(chart), *flags)
            rendered = run("helm", "template", "probe", str(chart), *flags).stdout
            (output / f"render-{mode}-{'override' if tag else 'default'}.yaml").write_text(rendered)
            docs = list(yaml.safe_load_all(rendered))
            deployment = next(d for d in docs if d and d["kind"] == "Deployment")
            job = next(d for d in docs if d and d["kind"] == "Job")
            engine = deployment["spec"]["template"]["spec"]["containers"][0]
            migrate = job["spec"]["template"]["spec"]["containers"][0]
            assert engine["image"].endswith(":" + (tag or metadata["appVersion"]))
            assert engine["image"] == migrate["image"]
            assert engine["readinessProbe"]["httpGet"]["path"] == "/readyz"
            assert engine["livenessProbe"]["httpGet"]["path"] == "/healthz"
            assert engine["readinessProbe"]["timeoutSeconds"] >= 1
            assert migrate["command"] == ["/engine", "-migrate-only"]
            assert "envFrom" not in migrate and len(migrate["env"]) == 1
    print("PASS lint/render: both modes, default/override image, independent versions", flush=True)


def cluster_checks(args, output):
    name = "rochallor-gate-" + uuid.uuid4().hex[:8]
    kubeconfig = output / "kubeconfig"
    env = dict(os.environ, KUBECONFIG=str(kubeconfig))
    commands = []

    def kubectl(*flags, **kwargs):
        return run("kubectl", *flags, env=env, **kwargs)

    def sql(statement, database="workflow"):
        return kubectl("exec", "db", "--", "psql", "-U", "workflow", "-d", database,
                       "-v", "ON_ERROR_STOP=1", "-Atc", statement).stdout.strip()

    def pods(release):
        return json.loads(kubectl("get", "pods", "-l", f"app.kubernetes.io/instance={release}",
                                  "-o", "json").stdout)["items"]

    def engine_pod(release):
        active = [p for p in pods(release) if p["metadata"]["labels"].get("app.kubernetes.io/component") != "migrations"
                  and not p["metadata"].get("deletionTimestamp")]
        return max(active, key=lambda p: p["metadata"]["creationTimestamp"])

    def ready(release):
        return any(c["type"] == "Ready" and c["status"] == "True"
                   for c in engine_pod(release)["status"].get("conditions", []))

    def helm(release, chart, image, *, database="workflow", mode="polling", hook=False,
             secret=False, success=True):
        repository, tag = image.rsplit(":", 1)
        values = {"image": {"repository": repository, "tag": tag, "pullPolicy": "Never"},
                  "engine": {"dispatchMode": mode}, "kafka": {"seedBrokers": "absent-broker:9092"},
                  "postgresql": {"dsn": f"postgres://workflow:workflow@db/{database}?sslmode=disable"},
                  "migrations": {"enabled": hook, "activeDeadlineSeconds": 25}}
        if secret:
            values["postgresql"] = {"existingSecret": "external-db"}
        filename = output / "test-values.yaml"
        filename.write_text(yaml.safe_dump(values))
        before = time.monotonic()
        result = run("helm", "upgrade", "--install", release, str(chart), "-f", str(filename),
                     "--wait", "--timeout", "90s", env=env, check=False, timeout=150)
        commands.append({"release": release, "mode": mode, "hook": hook, "database": database,
                         "existingSecret": secret, "exit": result.returncode,
                         "seconds": round(time.monotonic() - before, 2)})
        (output / f"helm-{len(commands)}.log").write_text(result.stdout + result.stderr)
        assert (result.returncode == 0) == success, result.stdout + result.stderr

    try:
        result = run(args.kind, "create", "cluster", "--name", name, "--kubeconfig", str(kubeconfig),
                     "--wait", "120s", timeout=300)
        (output / "kind-create.log").write_text(result.stdout + result.stderr)
        # Export only the installed platform. Docker's image store can retain
        # a multi-platform index without the other architectures' layer blobs;
        # kind's all-platform import would otherwise fail on registry images.
        platform = run("docker", "version", "--format", "{{.Server.Os}}/{{.Server.Arch}}").stdout.strip()
        with tempfile.TemporaryDirectory(prefix="rochallor-kind-images-") as temporary:
            archive = str(Path(temporary) / "image.tar")
            for image in dict.fromkeys([args.image, args.baseline_image, "postgres:16-alpine"]):
                run("docker", "image", "save", "--platform=" + platform, "-o", archive, image, timeout=300)
                run(args.kind, "load", "image-archive", archive, "--name", name, timeout=300)
        kubectl("apply", "-f", "-", input=yaml.safe_dump_all([
            {"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "db", "labels": {"app": "db"}},
             "spec": {"containers": [{"name": "db", "image": "postgres:16-alpine", "imagePullPolicy": "Never",
                 "env": [{"name": k, "value": v} for k, v in {
                     "POSTGRES_DB": "workflow", "POSTGRES_USER": "workflow", "POSTGRES_PASSWORD": "workflow"}.items()]}]}},
            {"apiVersion": "v1", "kind": "Service", "metadata": {"name": "db"},
             "spec": {"selector": {"app": "db"}, "ports": [{"port": 5432, "targetPort": 5432}]}}
        ]))
        kubectl("wait", "--for=condition=Ready", "pod/db", "--timeout=90s")
        eventually(lambda: kubectl("exec", "db", "--", "pg_isready", "-h", "127.0.0.1", "-U", "workflow",
                                   check=False).returncode == 0)

        # A real old chart/image pair, then new chart/new image; the incoming DSN
        # points to an empty DB. The pre-upgrade Secret must precede the Job.
        helm("probe", args.baseline_chart, args.baseline_image)
        assert ready("probe")
        sql("CREATE DATABASE workflow_next")
        helm("probe", args.chart, args.image, database="workflow_next", hook=True)
        assert sql("SELECT version || ':' || dirty FROM schema_migrations", "workflow_next") == "11:false"
        assert ready("probe")
        helm("probe", args.chart, args.image, database="workflow_next", hook=True)

        # Pending migration and no-change cases against a pre-existing Secret.
        sql("CREATE DATABASE workflow_pending")
        for migration in sorted((ROOT / "workflow-engine/migrations").glob("*.up.sql"))[:-1]:
            sql(migration.read_text(), "workflow_pending")
        sql("CREATE TABLE schema_migrations(version bigint PRIMARY KEY, dirty boolean NOT NULL); "
            "INSERT INTO schema_migrations VALUES(10, false)", "workflow_pending")
        kubectl("create", "secret", "generic", "external-db", "--from-literal=dsn="
                "postgres://workflow:workflow@db/workflow_pending?sslmode=disable")
        helm("probe", args.chart, args.image, hook=True, secret=True)
        assert sql("SELECT version || ':' || dirty FROM schema_migrations", "workflow_pending") == "11:false"
        helm("probe", args.chart, args.image, hook=True, secret=True)

        # No live Kafka broker is needed for the migration CLI. The initialized
        # engine remains ready and can buffer durable outbox rows.
        for mode in ("polling", "kafka_outbox"):
            helm("probe", args.chart, args.image, hook=True, secret=True, mode=mode)
            pod = engine_pod("probe")
            assert ready("probe")
            (output / f"{mode}-before-outage.json").write_text(json.dumps(pod, indent=2))
            uid = pod["metadata"]["uid"]
            restarts = pod["status"]["containerStatuses"][0]["restartCount"]
            db_pod = json.loads(kubectl("get", "pod", "db", "-o", "json").stdout)
            node = db_pod["spec"]["nodeName"]
            container_id = db_pod["status"]["containerStatuses"][0]["containerID"].removeprefix("containerd://")
            # Freeze the entire DB container. Signalling just PID 1 would leave
            # existing PostgreSQL backend processes servicing pooled sessions.
            run("docker", "exec", node, "ctr", "-n", "k8s.io", "tasks", "pause", container_id)
            try:
                eventually(lambda: not ready("probe"), 35)
                # More than a full liveness failure window: readiness alone must
                # remove traffic without restarting an otherwise live process.
                for _ in range(80):
                    time.sleep(1)
                after = engine_pod("probe")
                (output / f"{mode}-during-outage.json").write_text(json.dumps(after, indent=2))
                assert after["metadata"]["uid"] == uid
                assert after["status"]["containerStatuses"][0]["restartCount"] == restarts
                assert not ready("probe")
            finally:
                run("docker", "exec", node, "ctr", "-n", "k8s.io", "tasks", "resume", container_id)
            eventually(lambda: ready("probe"))
            print(f"PASS {mode}: DB outage removes readiness; no liveness restart; recovery", flush=True)

        # A migration error must prevent Deployment rollout, not be masked by a
        # shell exit 0. The failed hook's actual logs/exit code are retained.
        before = json.loads(kubectl("get", "deployment", "probe-rochallor", "-o", "json").stdout)
        sql("UPDATE schema_migrations SET dirty=true", "workflow_pending")
        helm("probe", args.chart, args.image, hook=True, secret=True, success=False)
        after = json.loads(kubectl("get", "deployment", "probe-rochallor", "-o", "json").stdout)
        assert before["metadata"]["generation"] == after["metadata"]["generation"]
        failed = kubectl("get", "pods", "-l", "job-name=probe-rochallor-migrations", "-o", "json").stdout
        (output / "failed-migration-pods.json").write_text(failed)
        for pod in json.loads(failed)["items"]:
            status = pod["status"]["containerStatuses"][0]["state"]["terminated"]
            assert status["exitCode"] != 0
            log = kubectl("logs", pod["metadata"]["name"]).stdout
            assert "REST listening" not in log and "dispatch runtime" not in log
            (output / (pod["metadata"]["name"] + ".log")).write_text(log)
        sql("UPDATE schema_migrations SET dirty=false", "workflow_pending")
        helm("probe", args.chart, args.image, database="missing_database", hook=True, success=False)
        after = json.loads(kubectl("get", "deployment", "probe-rochallor", "-o", "json").stdout)
        assert before["metadata"]["generation"] == after["metadata"]["generation"]
        print("PASS dirty migration and DB error: failed hook blocks Deployment rollout", flush=True)

        # Roll back chart/config while retaining the capable engine first, then
        # the real previous pair. No schema down migration is performed.
        helm("probe", args.baseline_chart, args.image)
        assert ready("probe")
        result = run("helm", "rollback", "probe", "1", "--wait", "--timeout", "90s", env=env)
        (output / "rollback.log").write_text(result.stdout + result.stderr)
        assert ready("probe")
        print("PASS old → new upgrade, compatible chart rollback, old pair rollback", flush=True)
        (output / "result.json").write_text(json.dumps({"result": "PASS", "commands": commands,
            "chartSha256": hashlib.sha256(Path(args.chart).read_bytes()).hexdigest(),
            "engineImage": args.image, "engineImageId": run("docker", "image", "inspect", args.image,
                "--format", "{{.Id}}").stdout.strip(), "baselineImage": args.baseline_image}, indent=2))
    finally:
        (output / "commands.json").write_text(json.dumps(commands, indent=2))
        result = kubectl("get", "pods", "-o", "wide", check=False)
        (output / "pods.log").write_text(result.stdout + result.stderr)
        result = kubectl("get", "events", "--sort-by=.lastTimestamp", check=False)
        (output / "events.log").write_text(result.stdout + result.stderr)
        result = run(args.kind, "delete", "cluster", "--name", name, check=False)
        if result.returncode:
            raise RuntimeError("failed to delete disposable kind cluster: " + result.stderr)
        kubeconfig.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--chart", required=True, help="Exact packaged .tgz that will be published")
    parser.add_argument("--image", required=True, help="Locally available candidate engine image")
    parser.add_argument("--baseline-image", default="ghcr.io/batnam/rochallor-engine:v1.1.2")
    parser.add_argument("--baseline-chart", help="Directory/package of the previous chart")
    parser.add_argument("--baseline-ref", default="engine-v1.1.2")
    parser.add_argument("--kind", default="kind")
    parser.add_argument("--output", default="testresults/helm")
    parser.add_argument("--render-only", action="store_true")
    args = parser.parse_args()
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    render_checks(Path(args.chart).resolve(), output)
    if not args.render_only:
        with tempfile.TemporaryDirectory(prefix="rochallor-old-chart-") as temporary:
            if not args.baseline_chart:
                archive = Path(temporary) / "chart.tar"
                run("git", "archive", args.baseline_ref, "deploy/helm/rochallor", "-o", str(archive))
                with tarfile.open(archive) as source:
                    source.extractall(temporary, filter="data")
                args.baseline_chart = str(Path(temporary) / "deploy/helm/rochallor")
            cluster_checks(args, output)


if __name__ == "__main__":
    main()
