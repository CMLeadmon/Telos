#!/usr/bin/env bash
# recovery.sh -- the `separate-host-restore` gate: judge a node restored from an off-node snapshot.
#
#   scripts/drills/recovery.sh --target-file PATH
#       (--rehearsal --source-commit SHA [--rehearsal-lock PATH] | --candidate-lock PATH) --result-out PATH
#
# Run it from a THIRD machine (neither the host that made the backup nor the restored host). It
# delegates to the live drill (scripts/restore-drill.sh --live), which observes the restored host
# over the fixed read-only collector, probes old credentials and the member journey over verified
# HTTPS, and measures RPO/RTO. It never prints success by itself, and without a target descriptor it
# can only report not_run -- a fixture or a same-host restore cannot satisfy this gate.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
scripts_dir="$(cd "$script_dir/.." && pwd)"
original_argv=("$0" "$@")

for argument in "$@"; do
  if [[ "$argument" == --target-file ]]; then
    exec bash "$scripts_dir/restore-drill.sh" --live "$@"
  fi
done

candidate_lock=""
evidence_out=""
result_out=""
usage() { echo "usage: $0 --target-file PATH (--rehearsal --source-commit SHA | --candidate-lock PATH) --result-out PATH" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-lock) [[ $# -ge 2 && -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage; candidate_lock="$2"; shift 2 ;;
    --evidence-out) [[ $# -ge 2 && -z "$evidence_out" && -n "$2" && "$2" != --* ]] || usage; evidence_out="$2"; shift 2 ;;
    --result-out) [[ $# -ge 2 && -z "$result_out" && -n "$2" && "$2" != --* ]] || usage; result_out="$2"; shift 2 ;;
    --source-commit|--rehearsal-lock) [[ $# -ge 2 && -n "$2" && "$2" != --* ]] || usage; shift 2 ;;
    --rehearsal) shift ;;
    *) usage ;;
  esac
done
reason="no recovered host was supplied: the separate-host-restore gate observes a real restored machine and needs --target-file (a protected drill-target descriptor for the recovered host)"
if [[ -z "$candidate_lock" ]]; then
  echo "recovery: not_run: $reason" >&2
  exit 3
fi
arguments=(--gate-id "${TELOS_GATE_ID:-separate-host-restore}" --reason "$reason" --candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
exec python3 "$scripts_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
