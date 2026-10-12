#!/usr/bin/env python3
import json
from pathlib import Path
import sys

root, current = map(Path, sys.argv[1:])
for candidate in sorted(root.glob("*/summary.json"), reverse=True):
    if candidate.parent.resolve() == current.resolve():
        continue
    # An invalid or incompatible baseline is not silently treated as no baseline.
    summary = json.loads(candidate.read_text())
    if summary["result"] == "pass":
        print(candidate)
        break
else:
    raise SystemExit("No passing baseline found; run without comparison first")
