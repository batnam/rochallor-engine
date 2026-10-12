"""Negative command tests: the executable must reject drift and network errors."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import subprocess
import sys
import threading
import unittest

from replay import Replay

ROOT = Path(__file__).resolve().parents[2]
SPEC = ROOT / "api/openapi/rest-openapi.yaml"


class ReplayTests(unittest.TestCase):
    def command(self, base):
        return subprocess.run([sys.executable, str(Path(__file__).with_name("replay.py")),
                               "--openapi", str(SPEC), "--base", base],
                              capture_output=True, text=True, timeout=15)

    def test_invalid_responses_fail_the_command(self):
        for mutation in ("status", "schema", "json", "content-type", "behavior"):
            with self.subTest(mutation=mutation):
                class Handler(BaseHTTPRequestHandler):
                    def do_GET(self):
                        # Let the readiness prelude pass so each test reaches
                        # its intended mutated definition response.
                        self.send_response(200)
                        if self.path == "/readyz":
                            self.send_header("Content-Type", "application/json")
                        self.end_headers()
                        if self.path == "/readyz":
                            self.wfile.write(b'{"status":"ready"}')

                    def do_POST(self):
                        self.rfile.read(int(self.headers["Content-Length"]))
                        self.send_response(200 if mutation == "status" else 201)
                        self.send_header("Content-Type", "text/plain" if mutation == "content-type"
                                         else "application/json")
                        self.end_headers()
                        body = {"id": "wrong-id", "name": "wrong", "version": 1}
                        if mutation == "schema":
                            body["version"] = "one"
                        self.wfile.write(b"{" if mutation == "json" else json.dumps(body).encode())

                    def log_message(self, *_args):
                        pass

                with ThreadingHTTPServer(("127.0.0.1", 0), Handler) as server:
                    thread = threading.Thread(target=server.serve_forever, daemon=True)
                    thread.start()
                    result = self.command(f"http://127.0.0.1:{server.server_port}")
                    server.shutdown()
                    thread.join()
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn("FAILED", result.stderr)
                self.assertIn("PASS GET /readyz", result.stdout)
                expected = {"status": "expected 201", "schema": "ValidationError",
                            "json": "JSONDecodeError", "content-type": "expected application/json",
                            "behavior": "AssertionError"}[mutation]
                self.assertIn(expected, result.stderr)

    def test_unreachable_server_fails(self):
        with ThreadingHTTPServer(("127.0.0.1", 0), BaseHTTPRequestHandler) as server:
            address = f"http://127.0.0.1:{server.server_port}"
        self.assertNotEqual(self.command(address).returncode, 0)

    def test_nested_required_fields_and_formats_are_checked(self):
        replay = Replay(SPEC, "http://unused")
        for value in ({"jobs": [{}]}, {"jobs": "wrong"}):
            with self.subTest(value=value), self.assertRaises(Exception):
                replay.validate({"$ref": "#/components/schemas/JobPollResponse"}, value)
        with self.assertRaises(Exception):
            replay.validate({"type": "string", "format": "date-time"}, "not-a-date")


if __name__ == "__main__":
    unittest.main()
