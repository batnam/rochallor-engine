#!/usr/bin/env sh
# Compatibility entry point; run.sh invokes the same sampler directly.
exec python3 "$(dirname "$0")/host-stats.py" "$@"
