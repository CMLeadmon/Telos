#!/usr/bin/env bash
set -euo pipefail

# Browser and desktop matrix aggregation: signed, candidate-bound platform records, in fixtures with ephemeral keys.
# (tests/operations/guided_smoke_test.py is the substance; this wrapper keeps the catalog command stable.)
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
echo "Running browser and desktop certification suite..."
for script in scripts/certify-browser-matrix.sh scripts/certify-desktop.sh scripts/record-guided-smoke.py scripts/lib/guided_smoke.py scripts/lib/browser_report.py release/guided-assertions.json; do
  [[ -f "$script" ]] || { echo "$script missing" >&2; exit 1; }
done
python3 -B tests/operations/guided_smoke_test.py
# The placeholder shapes must stay fail-closed.
for script in scripts/certify-browser-matrix.sh scripts/certify-desktop.sh; do
  set +e
  bash "$script" >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 3 ]] || { echo "$script without inputs returned $status instead of not_run" >&2; exit 1; }
done
echo "Browser and desktop certification suite passed."
