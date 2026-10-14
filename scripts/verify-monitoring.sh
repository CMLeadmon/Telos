#!/usr/bin/env bash
# verify-monitoring.sh -- monitoring source checks (--source) or a live Prometheus check
# (--runtime). See verify-monitoring.py.
set -euo pipefail
exec python3 -B "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/verify-monitoring.py" "$@"
