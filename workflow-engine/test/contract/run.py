#!/usr/bin/env python3
"""Run contract replay against a fresh disposable engine/DB for each mode."""
import argparse
import os
from pathlib import Path
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from support import ENGINE_ROOT, EngineStack, build_engine


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=["polling", "kafka_outbox", "all"], default="all")
    parser.add_argument("--output", default="../testresults/t01-contract")
    parser.add_argument("--engine", help="Already-built engine binary (otherwise build current source)")
    args = parser.parse_args()
    output = Path(args.output).resolve()
    binary = args.engine or build_engine(output / "engine")
    for mode in (["polling", "kafka_outbox"] if args.mode == "all" else [args.mode]):
        with EngineStack(binary, output / mode, mode) as stack:
            stack.start()
            env = dict(stack.env, PATH=str(Path(sys.executable).parent) + os.pathsep + os.environ["PATH"])
            result = subprocess.run(["make", "contract-replay"], cwd=ENGINE_ROOT, env=env,
                                    capture_output=True, text=True, timeout=120)
            (output / mode / "replay.log").write_text(result.stdout + result.stderr)
            print(result.stdout + result.stderr, end="", flush=True)
            if result.returncode:
                raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
