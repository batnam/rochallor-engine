#!/usr/bin/env python3
"""Sample the actual k6 process/container and this run's Compose containers."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import threading
import time

directory, pid = Path(sys.argv[1]), int(sys.argv[2])
interval = float(sys.argv[3]) if len(sys.argv) > 3 else 2
stop = threading.Event()
signal.signal(signal.SIGTERM, lambda *_: stop.set())
signal.signal(signal.SIGINT, lambda *_: stop.set())
samples, errors = [], []
started = time.monotonic()
generator = os.environ.get("PERF_K6_CONTAINER", "")
project = os.environ["COMPOSE_PROJECT_NAME"]


def command(*args):
    return subprocess.check_output(args, text=True, timeout=15).strip()


try:
    cores = int(command("docker", "info", "--format", "{{.NCPU}}")) if generator else os.cpu_count()
    if not cores or cores < 1:
        raise ValueError("cannot determine generator CPU capacity")
    if generator:
        # docker run may still be pulling the image. Do not sample a container
        # before it exists, and do not mistake the Docker CLI's CPU for k6.
        deadline = time.monotonic() + 120
        while not stop.is_set():
            result = subprocess.run(["docker", "inspect", "--format", "{{.State.Running}}", generator],
                                    capture_output=True, text=True, timeout=10)
            if result.returncode == 0 and result.stdout.strip() == "true":
                break
            os.kill(pid, 0)
            if time.monotonic() >= deadline:
                raise TimeoutError("k6 container did not start within 120s")
            stop.wait(0.2)
    while not stop.is_set():
        try:
            names = command("docker", "ps", "--filter", f"label=com.docker.compose.project={project}",
                            "--format", "{{.Names}}").splitlines()
            if not names:
                raise RuntimeError("Compose containers missing")
            if generator:
                names.append(generator)
            stats = [json.loads(line) for line in command("docker", "stats", "--no-stream", "--format", "{{json .}}", *names).splitlines()]
            by_name = {item["Name"]: item for item in stats}
            if generator:
                cpu = float(by_name[generator]["CPUPerc"].removesuffix("%"))
                rss = None
            else:
                values = command("ps", "-o", "pcpu=,rss=", "-p", str(pid)).split()
                cpu, rss = float(values[0]), int(values[1]) / 1024
            containers = {name: {"cpuPct": float(item["CPUPerc"].removesuffix("%")), "memory": item["MemUsage"]}
                          for name, item in by_name.items() if name != generator}
            process = {"cpuPct": cpu, "capacityPct": cpu / cores, "rssMB": rss}
            if generator:
                process["containerMemoryUsage"] = by_name[generator]["MemUsage"]
            samples.append({"tSinceStartSec": round(time.monotonic() - started, 3),
                            "k6Process": process,
                            "containers": containers, "loadavg1": os.getloadavg()[0]})
        except (subprocess.CalledProcessError, ProcessLookupError, KeyError, IndexError) as exc:
            # k6 may finish between docker stats and ps. That final partial sample
            # is discarded; an error while the process remains alive is fatal.
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                break
            if stop.is_set():
                break
            raise RuntimeError(f"resource sampling failed: {exc}") from exc
        stop.wait(interval)
except Exception as exc:
    errors.append(str(exc))
finally:
    def peak(service):
        return max((sum(item["cpuPct"] for name, item in sample["containers"].items()
                        if f"-{service}-" in name) for sample in samples), default=0)
    summary = {"sampleCount": len(samples), "generatorCpuCores": locals().get("cores"),
               "k6CpuPctPeak": max((s["k6Process"]["cpuPct"] for s in samples), default=0),
               "k6CapacityPctPeak": max((s["k6Process"]["capacityPct"] for s in samples), default=0),
               "engineCpuPctPeak": peak("engine"), "postgresCpuPctPeak": peak("postgres"),
               "workersCpuPctPeak": peak("worker-go")}
    summary["generatorLikelySaturated"] = summary["k6CapacityPctPeak"] >= 90
    (directory / "host-stats.json").write_text(json.dumps({"schemaVersion": "2.0", "intervalSeconds": interval,
        "samples": samples, "summary": summary, "errors": errors}, indent=2) + "\n")
if errors or not samples:
    raise SystemExit("Host sampling failed: " + "; ".join(errors or ["no samples"]))
