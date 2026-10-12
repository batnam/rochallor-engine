"""Disposable PostgreSQL/broker and native engine for command/contract tests.

Only containers created by this object are removed. No developer database,
Compose project, fixed port, or inherited engine configuration is used.
"""
from contextlib import ExitStack
import os
from pathlib import Path
import socket
import subprocess
import time
from urllib.request import urlopen
import uuid

ENGINE_ROOT = Path(__file__).resolve().parents[1]


def free_ports(count):
    with ExitStack() as stack:
        sockets = [stack.enter_context(socket.socket()) for _ in range(count)]
        for sock in sockets:
            sock.bind(("127.0.0.1", 0))
        return [sock.getsockname()[1] for sock in sockets]


def build_engine(output):
    output = Path(output).resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["go", "build", "-o", str(output), "./cmd/engine"],
                   cwd=ENGINE_ROOT, check=True, timeout=120)
    return output


class EngineStack:
    def __init__(self, binary, output, mode="polling"):
        self.binary = str(Path(binary).resolve())
        self.output = Path(output).resolve()
        self.output.mkdir(parents=True, exist_ok=True)
        self.mode = mode
        self.name = "contract-" + uuid.uuid4().hex[:12]
        self.containers = []
        self.process = None
        self.log = None
        self.db_port, self.rest_port, self.grpc_port, self.metrics_port, self.kafka_port = free_ports(5)
        self.dsn = f"postgres://workflow:workflow@127.0.0.1:{self.db_port}/workflow?sslmode=disable"
        self.base = f"http://127.0.0.1:{self.rest_port}"
        self.env = {k: v for k, v in os.environ.items()
                    if not k.startswith(("WE_", "DB_MAX_CONNS", "DB_MIN_CONNS"))}
        self.env.update(WE_POSTGRES_DSN=self.dsn, WE_REST_PORT=str(self.rest_port),
                        WE_GRPC_PORT=str(self.grpc_port), WE_METRICS_PORT=str(self.metrics_port),
                        WE_DISPATCH_MODE=mode, DB_MAX_CONNS="8", DB_MIN_CONNS="0",
                        WE_KAFKA_SEED_BROKERS=f"127.0.0.1:{self.kafka_port}",
                        WE_KAFKA_TRANSPORT="plaintext")

    def docker(self, *args, check=True):
        return subprocess.run(["docker", *args], capture_output=True, text=True,
                              timeout=120, check=check)

    def container(self, suffix, image, port, target, environment):
        name = self.name + "-" + suffix
        args = ["run", "-d", "--name", name, "-p", f"127.0.0.1:{port}:{target}"]
        for key, value in environment.items():
            args.extend(["-e", f"{key}={value}"])
        self.containers.append(name)
        self.docker(*args, image)
        return name

    def __enter__(self):
        try:
            self.database = self.container("db", "postgres:16-alpine", self.db_port, 5432,
                                           {"POSTGRES_DB": "workflow", "POSTGRES_USER": "workflow",
                                            "POSTGRES_PASSWORD": "workflow"})
            for _ in range(60):
                result = self.docker("exec", self.database, "pg_isready", "-h", "127.0.0.1",
                                     "-U", "workflow", "-d", "workflow", check=False)
                if result.returncode == 0:
                    break
                time.sleep(0.5)
            else:
                raise TimeoutError("test PostgreSQL did not become ready")
            if self.mode == "kafka_outbox":
                self.broker = self.container("kafka", "confluentinc/cp-kafka:7.7.8",
                    self.kafka_port, 9092, {
                        "KAFKA_HEAP_OPTS": "-Xmx512M -Xms256M", "KAFKA_NODE_ID": "0",
                        "KAFKA_PROCESS_ROLES": "broker,controller",
                        "KAFKA_CONTROLLER_QUORUM_VOTERS": "0@localhost:9093",
                        "CLUSTER_ID": "MkU3OEVBNTcwNTJENDM2Qk",
                        "KAFKA_LISTENERS": "EXTERNAL://:9092,INTERNAL://:9094,CONTROLLER://:9093",
                        "KAFKA_ADVERTISED_LISTENERS": f"EXTERNAL://127.0.0.1:{self.kafka_port},INTERNAL://localhost:9094",
                        "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP": "EXTERNAL:PLAINTEXT,INTERNAL:PLAINTEXT,CONTROLLER:PLAINTEXT",
                        "KAFKA_INTER_BROKER_LISTENER_NAME": "INTERNAL",
                        "KAFKA_CONTROLLER_LISTENER_NAMES": "CONTROLLER",
                        "KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR": "1",
                        "KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR": "1",
                        "KAFKA_TRANSACTION_STATE_LOG_MIN_ISR": "1",
                        "KAFKA_AUTO_CREATE_TOPICS_ENABLE": "true",
                    })
                deadline = time.monotonic() + 60
                while time.monotonic() < deadline:
                    try:
                        with socket.create_connection(("127.0.0.1", self.kafka_port), timeout=1):
                            break
                    except OSError:
                        time.sleep(0.5)
                else:
                    raise TimeoutError("test Kafka did not start listening")
            return self
        except BaseException:
            self.__exit__(None, None, None)
            raise

    def start(self):
        self.log = (self.output / "engine.log").open("a")
        self.process = subprocess.Popen([self.binary], env=self.env, stdout=self.log, stderr=self.log)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise RuntimeError(f"engine exited {self.process.returncode}; see {self.output / 'engine.log'}")
            try:
                with urlopen(self.base + "/healthz", timeout=1) as response:
                    if response.status == 200:
                        return
            except OSError:
                pass
            time.sleep(0.2)
        raise TimeoutError("test engine did not start")

    def sql(self, statement):
        return self.docker("exec", self.database, "psql", "-U", "workflow", "-d", "workflow",
                           "-v", "ON_ERROR_STOP=1", "-Atc", statement).stdout.strip()

    def __exit__(self, *_args):
        if self.process and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        if self.log:
            self.log.close()
        failed = []
        for name in reversed(self.containers):
            result = self.docker("logs", name, check=False)
            (self.output / f"{name.rsplit('-', 1)[1]}.log").write_text(result.stdout + result.stderr)
            result = self.docker("rm", "-fv", name, check=False)
            if result.returncode:
                failed.append(name)
        if failed:
            raise RuntimeError(f"could not remove disposable containers: {failed}")
