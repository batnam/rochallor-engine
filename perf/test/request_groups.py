#!/usr/bin/env python3
"""Exercise real k6 request tags without turning workflow IDs into time series."""
from collections import Counter, defaultdict
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import unittest


class RequestGroups(unittest.TestCase):
    def test_dynamic_urls_keep_real_paths_and_bounded_metric_groups(self):
        requests = []

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                requests.append((self.command, self.path))
                self.rfile.read(int(self.headers.get("Content-Length", "0")))
                body = b'{"id":"fixture","status":"COMPLETED","items":[]}'
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            do_POST = do_GET

            def log_message(self, *_):
                pass

        server = ThreadingHTTPServer(("0.0.0.0", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(thread.join, 5)
        self.addCleanup(server.shutdown)
        client = Path(__file__).resolve().parents[1] / "lib/client.js"
        native = shutil.which("k6")
        address = "127.0.0.1" if native else "host.docker.internal"
        base = f"http://{address}:{server.server_port}"
        with tempfile.TemporaryDirectory(prefix="perf-request-groups-") as tmp:
            directory = Path(tmp)
            module = str(client) if native else "/work/client.js"
            script = directory / "requests.js"
            script.write_text("import * as api from " + json.dumps(module) + ";\n" + """
export const options = { vus: 1, iterations: 20, tags: { phase: 'steady-state' } };
export default function () {
  const base = __ENV.TEST_BASE_URL;
  const id = `case-${__ITER}`;
  api.uploadDefinition(base, { id });
  api.startWorkflow(base, id, id, {});
  api.pollInstance(base, id);
  api.getHistory(base, id);
  api.listInstances(base, { page: __ITER, definitionId: id });
  api.cancelInstance(base, id, 'fixture');
  api.signalWaitStep(base, id, `wait-${__ITER}`, {});
  api.completeUserTask(base, id, `task-${__ITER}`, {});
}
""")
            output = directory / "points.json"
            if native:
                command = [native, "run", "--out", "json=" + str(output),
                           "-e", "TEST_BASE_URL=" + base, str(script)]
            else:
                command = ["docker", "run", "--rm", "--user", "0:0",
                           "--add-host", "host.docker.internal:host-gateway",
                           "-v", str(directory) + ":/work",
                           "-v", str(client) + ":/work/client.js:ro",
                           "grafana/k6:1.7.0", "run", "--out", "json=/work/points.json",
                           "-e", "TEST_BASE_URL=" + base, "/work/requests.js"]
            result = subprocess.run(command, capture_output=True, text=True, timeout=60)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(len(requests), 160)
            for n in range(20):
                self.assertIn(("GET", f"/v1/instances/case-{n}"), requests)
                self.assertIn(("GET", f"/v1/instances/case-{n}/history"), requests)
                self.assertIn(("GET", f"/v1/instances?page={n}&definitionId=case-{n}"), requests)
                self.assertIn(("POST", f"/v1/instances/case-{n}/cancel"), requests)
                self.assertIn(("POST", f"/v1/instances/case-{n}/signals/wait-{n}"), requests)
                self.assertIn(("POST", f"/v1/instances/case-{n}/user-tasks/task-{n}/complete"), requests)
            groups = defaultdict(set)
            counts = Counter()
            for line in output.read_text().splitlines():
                point = json.loads(line)
                if point.get("type") != "Point" or point.get("metric") != "http_req_duration":
                    continue
                tags = point["data"]["tags"]
                self.assertEqual(tags["phase"], "steady-state")
                groups[tags["operation"]].add((tags["name"], tags["url"]))
                counts[tags["operation"]] += 1
            expected = ["definition.upload", "workflow.start", "workflow.poll", "workflow.history",
                        "workflow.list", "workflow.cancel", "workflow.signal", "workflow.task_complete"]
            self.assertEqual(dict(counts), {operation: 20 for operation in expected})
            self.assertEqual({operation: len(values) for operation, values in groups.items()},
                             {operation: 1 for operation in expected})


if __name__ == "__main__":
    unittest.main(verbosity=2)
