#!/usr/bin/env bash
set -euo pipefail

# Monitoring contract (Phase 4 Task 3): the configuration is valid and private, the
# metrics port is never published or routed, and the runtime verifier fails on a down
# target, a stale scrape, a firing critical alert or a missing series.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
echo "Running monitoring test suite..."

python3 -B tests/operations/monitoring_check_test.py

# promtool: the locked Prometheus image when Podman is present, or $PROMTOOL (a binary
# extracted from that same image). Without either, the repository checks above still ran
# and this step reports not_run instead of pretending the configuration was validated.
set +e
bash scripts/verify-monitoring.sh --source
status=$?
set -e
case "$status" in
  0) ;;
  3) echo "monitoring: promtool unavailable (set PROMTOOL or install Podman); configuration validation not run" >&2; exit 3 ;;
  *) echo "verify-monitoring.sh --source failed ($status)" >&2; exit 1 ;;
esac

# Usage errors are 2, and a mode is required.
for bad in "" "--source --runtime" "--bogus"; do
  set +e; bash scripts/verify-monitoring.sh $bad >/dev/null 2>&1; code=$?; set -e
  [[ $code -eq 2 ]] || { echo "verify-monitoring.sh $bad exited $code, expected 2" >&2; exit 1; }
done

# The runtime mode without a container engine is not_run, never a pass.
set +e
env PATH="/usr/bin:/bin" bash scripts/verify-monitoring.sh --runtime >/dev/null 2>&1; code=$?
set -e
if ! command -v podman >/dev/null 2>&1 || [[ -z "$(PATH=/usr/bin:/bin command -v podman || true)" ]]; then
  [[ $code -eq 3 ]] || { echo "--runtime without Podman exited $code, expected 3 (not_run)" >&2; exit 1; }
fi
echo "Monitoring test suite passed."
