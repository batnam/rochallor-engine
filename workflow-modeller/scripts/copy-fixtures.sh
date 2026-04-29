#!/usr/bin/env bash
# Copy workflow JSON fixtures from the Go engine's testdata corpus into the
# editor's fixture tree, so the TS validator and the Go validator agree on the
# same set of inputs (drift guard — see research.md R-010, quickstart.md §5).
#
# The script is idempotent and safe to re-run. If the source directory does
# not exist yet, the script exits with a warning but does NOT fail — the
# editor also ships a small set of hand-authored fixtures under
# tests/fixtures/valid/ that are enough to get the test suite running.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$here/../.." && pwd)

engine_src_candidates=(
  "$repo_root/workflow-engine/internal/definition/testdata"
  "$repo_root/workflow-engine/test/fixtures"
  "$repo_root/workflow-engine/pkg/definition/testdata"
)

dest_valid="$here/../tests/fixtures/valid"
dest_invalid="$here/../tests/fixtures/invalid"

mkdir -p "$dest_valid" "$dest_invalid"

source_found=0
for src in "${engine_src_candidates[@]}"; do
  if [[ -d "$src" ]]; then
    source_found=1
    echo "Copying engine fixtures from: $src"
    # Positive fixtures: anything whose filename does NOT start with "invalid-".
    find "$src" -type f -name '*.json' ! -name 'invalid-*.json' -print0 |
      xargs -0 -I{} cp -f {} "$dest_valid/"
    # Negative fixtures: filenames starting with "invalid-".
    find "$src" -type f -name 'invalid-*.json' -print0 |
      xargs -0 -I{} cp -f {} "$dest_invalid/"
    break
  fi
done

if [[ "$source_found" -eq 0 ]]; then
  echo "warning: no engine fixture directory found under any of:"
  for src in "${engine_src_candidates[@]}"; do
    echo "  - $src"
  done
  echo "         Continuing with the editor's hand-authored fixtures only."
  echo "         Update this script when the engine corpus lands."
fi

echo "Fixture layout:"
echo "  valid:   $(find "$dest_valid" -maxdepth 1 -type f -name '*.json' | wc -l | tr -d ' ') file(s)"
echo "  invalid: $(find "$dest_invalid" -maxdepth 1 -type f -name '*.json' | wc -l | tr -d ' ') file(s)"
