#!/usr/bin/env python3
"""Real PostgreSQL regression for reading cumulative writes before clients exit."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import uuid

spec = importlib.util.spec_from_file_location("heartbeat", Path(__file__).resolve().parents[1] / "heartbeat.py")
heartbeat = importlib.util.module_from_spec(spec)
spec.loader.exec_module(heartbeat)


class PostgreSQLStatistics(unittest.TestCase):
    def test_snapshot_flushes_a_committed_update_from_an_idle_writer(self):
        with tempfile.TemporaryDirectory(prefix="heartbeat-stats-") as directory:
            path = Path(directory) / "compose.json"
            # The engine service here is a client container holding an idle DB
            # writer. This isolates the same backend-flush boundary as the engine.
            path.write_text(json.dumps({"services": {
                "postgres": {"image": "postgres:16-alpine", "environment": {
                    "POSTGRES_USER": "workflow", "POSTGRES_PASSWORD": "workflow", "POSTGRES_DB": "workflow"},
                    "healthcheck": {"test": ["CMD-SHELL", "pg_isready -h 127.0.0.1 -U workflow -d workflow"],
                                    "interval": "1s", "timeout": "2s", "retries": 30}},
                "engine": {"image": "postgres:16-alpine", "entrypoint": ["sh", "-c", "trap 'exit 0' TERM; sleep 600 & wait"],
                    "environment": {"PGPASSWORD": "workflow"},
                    "depends_on": {"postgres": {"condition": "service_healthy"}}}
            }}))
            compose = ["docker", "compose", "-p", "stats-" + uuid.uuid4().hex[:12], "-f", str(path)]
            env = dict(os.environ)
            writer = None
            with (Path(directory) / "commands.log").open("w") as log:
                try:
                    subprocess.run([*compose, "up", "-d", "--wait"], env=env, stdout=log, stderr=log, check=True, timeout=60)
                    subprocess.run([*compose, "exec", "-T", "postgres", "psql", "-U", "workflow", "-d", "workflow",
                                    "-v", "ON_ERROR_STOP=1", "-c", "CREATE TABLE job(id int primary key,status text); INSERT INTO job VALUES(1,'LOCKED');"],
                                   env=env, stdout=log, stderr=log, check=True, timeout=10)
                    writer = subprocess.Popen([*compose, "exec", "-T", "engine", "psql", "-h", "postgres", "-U", "workflow", "-d", "workflow",
                                               "-qAt", "-v", "ON_ERROR_STOP=1"], env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                              stderr=log, text=True, bufsize=1)
                    # Force a flush, then commit within PGSTAT_MIN_INTERVAL.
                    # An otherwise idle backend retains this next update locally.
                    writer.stdin.write("SELECT pg_stat_force_next_flush();\nUPDATE job SET status='COMPLETED' WHERE id=1;\nSELECT 'committed';\n")
                    writer.stdin.flush()
                    for line in writer.stdout:
                        if line.strip() == "committed":
                            break
                    else:
                        self.fail("writer exited before committing")
                    snapshot = heartbeat.database_snapshot(compose, env, log)
                    self.assertEqual(snapshot["completedJobs"], 1)
                    self.assertEqual(snapshot["jobUpdates"], 1, "snapshot missed a committed update still held by the writer backend")
                    self.assertEqual(snapshot["writerConnections"], 0)
                    print("PASS: collected the committed update after the writer connection closed")
                finally:
                    if writer is not None:
                        try:
                            writer.stdin.close()
                        except BrokenPipeError:
                            pass
                        writer.wait(timeout=15)
                        writer.stdout.close()
                    subprocess.run([*compose, "down", "-v", "--remove-orphans"], env=env, stdout=log, stderr=log, check=True, timeout=30)


if __name__ == "__main__":
    unittest.main()
