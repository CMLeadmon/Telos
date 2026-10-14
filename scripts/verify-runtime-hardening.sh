#!/usr/bin/env bash
# verify-runtime-hardening.sh -- static policy check of the rendered Compose model, or
# live inspection of the running containers. See verify-runtime-hardening.py.
set -euo pipefail
exec python3 -B "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/verify-runtime-hardening.py" "$@"
