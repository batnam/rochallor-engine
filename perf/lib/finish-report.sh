#!/usr/bin/env sh
# Shared by the real command and negative command fixtures.
set -eu
REPORT_DIR="${1:?reports directory required}"
BASELINE="${2:-}"
K6_EXIT="${3:?k6 exit code required}"
LIB_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPORT_EXIT=0
node "$LIB_DIR/report.js" "$REPORT_DIR" "$BASELINE" > "$REPORT_DIR/report-patch.json" || REPORT_EXIT=$?
python3 - "$REPORT_DIR" "$REPORT_EXIT" <<'PY'
import json, pathlib, sys
directory = pathlib.Path(sys.argv[1])
summary = json.loads((directory / 'summary.json').read_text())
try:
    patch = json.loads((directory / 'report-patch.json').read_text())
except (ValueError, OSError):
    patch = {'result': 'fail', 'gateFailures': ['report evidence missing or invalid']}
summary.update(patch)
if int(sys.argv[2]):
    summary['result'] = 'fail'
(directory / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
PY
python3 "$LIB_DIR/render-report-md.py" "$REPORT_DIR"
if [ "$K6_EXIT" -ne 0 ]; then exit "$K6_EXIT"; fi
exit "$REPORT_EXIT"
