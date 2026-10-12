#!/usr/bin/env python3
"""Fingerprint the harness files actually executed, including dirty changes."""
import hashlib
from pathlib import Path
import sys

root = Path(sys.argv[1]).resolve()
digest = hashlib.sha256()
for path in sorted(root.rglob("*")):
    relative = path.relative_to(root)
    if not path.is_file() or any(part in {"reports", "test", "__pycache__"} for part in relative.parts):
        continue
    if path.suffix not in {".js", ".py", ".sh", ".json", ".yml", ".yaml", ".go", ".mod", ".sum", ".sql"} and path.name != "Dockerfile":
        continue
    digest.update(str(relative).encode() + b"\0" + path.read_bytes() + b"\0")
print(digest.hexdigest())
