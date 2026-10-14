#!/usr/bin/env bash
# verify-restored-node.sh -- verify a node restored by scripts/restore.sh, from another machine.
#
#   scripts/verify-restored-node.sh --target-file PATH
#       (--rehearsal --source-commit SHA | --candidate-lock PATH) --result-out PATH
#
# The same live drill as the separate-host-restore gate (scripts/restore-drill.sh --live), recorded
# under the gate id `restored-node` unless TELOS_GATE_ID says otherwise: host/candidate/snapshot
# identities, journal and sanitizer audit, password re-authentication, old cookie/device/invite
# rejection, DNS and TLS, the member journey (Chat, Stream, Library, Files, commentary, range
# media), and RPO/RTO. It accepts no fixture statuses. Without a target it reports not_run.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for argument in "$@"; do
  if [[ "$argument" == --target-file ]]; then
    TELOS_GATE_ID="${TELOS_GATE_ID:-restored-node}" exec bash "$script_dir/restore-drill.sh" --live "$@"
  fi
done
echo "verify-restored-node: not_run: --target-file (a drill-target descriptor for the restored host) is required; a restored node cannot be verified without observing it" >&2
exit 3
