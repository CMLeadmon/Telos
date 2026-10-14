#!/usr/bin/env bash
# audit-host-exposure.sh -- what a Telos node lets the network reach (the `host-exposure` gate).
#
#   scripts/audit-host-exposure.sh --source [--policy config/exposure-policy.json] [--compose FILE ...]
#       Static: the policy is well formed and Compose publishes only Traefik on 80/443.
#   scripts/audit-host-exposure.sh --target-file PATH [--policy PATH]
#       (--rehearsal --source-commit SHA | --candidate-lock PATH) --result-out PATH
#       Live, from a machine that is not the node: scan every TCP port and a fixed UDP list, compare with
#       the node's own report of published ports, listeners and networks, and judge against the policy.
#
# Without a source or live request there is nothing to observe, so it reports not_run (exit 3) and never a pass.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
original_argv=("$0" "$@")
if [[ "${1:-}" == --source ]]; then
  shift
  exec python3 -I "$script_dir/lib/exposure_audit.py" source "$@"
fi
for argument in "$@"; do
  if [[ "$argument" == --target-file ]]; then
    exec python3 -I "$script_dir/lib/exposure_audit.py" live "$@"
  fi
done

candidate_lock=""
evidence_out=""
result_out=""
usage() { echo "usage: $0 --source [--policy PATH] | --target-file PATH (--rehearsal --source-commit SHA | --candidate-lock PATH) --result-out PATH" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-lock) [[ $# -ge 2 && -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage; candidate_lock="$2"; shift 2 ;;
    --evidence-out) [[ $# -ge 2 && -z "$evidence_out" && -n "$2" && "$2" != --* ]] || usage; evidence_out="$2"; shift 2 ;;
    --result-out) [[ $# -ge 2 && -z "$result_out" && -n "$2" && "$2" != --* ]] || usage; result_out="$2"; shift 2 ;;
    *) usage ;;
  esac
done
arguments=(--gate-id "${TELOS_GATE_ID:-host-exposure}" --reason "no audited host was supplied: the host-exposure gate scans a real node from another machine and needs --target-file (and an external scanner)")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
exec python3 "$script_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
