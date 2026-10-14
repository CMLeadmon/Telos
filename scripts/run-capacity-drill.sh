#!/usr/bin/env bash
# run-capacity-drill.sh -- the `capacity` gate: 100 disposable members, 25 simultaneous sessions, 30 minutes.
#
#   scripts/run-capacity-drill.sh --target-file PATH (--rehearsal --source-commit SHA | --candidate-lock PATH)
#       --result-out PATH [--duration 30m]
#
# Run from a LOAD HOST that is not the node. The locked k6 image drives tests/load/telos.js against the node's
# HTTPS origin while scripts/collect-runtime-stats.py samples the node every 5 seconds; the drill judges both
# (scripts/lib/capacity_drill.py) and removes every account it created. A run shorter than 30 minutes cannot pass.
# Without a target it reports not_run (exit 3) and never a pass.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
original_argv=("$0" "$@")
for argument in "$@"; do
  if [[ "$argument" == --target-file ]]; then
    exec python3 -I "$script_dir/lib/capacity_drill.py" "$@"
  fi
done

candidate_lock=""
evidence_out=""
result_out=""
usage() { echo "usage: $0 --target-file PATH (--rehearsal --source-commit SHA | --candidate-lock PATH) --result-out PATH [--duration 30m]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-lock) [[ $# -ge 2 && -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage; candidate_lock="$2"; shift 2 ;;
    --evidence-out) [[ $# -ge 2 && -z "$evidence_out" && -n "$2" && "$2" != --* ]] || usage; evidence_out="$2"; shift 2 ;;
    --result-out) [[ $# -ge 2 && -z "$result_out" && -n "$2" && "$2" != --* ]] || usage; result_out="$2"; shift 2 ;;
    *) usage ;;
  esac
done
arguments=(--gate-id "${TELOS_GATE_ID:-capacity}" --reason "no load target was supplied: the capacity gate loads a real disposable node from another machine and needs --target-file (a protected drill-target descriptor naming a capacity target)")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
exec python3 "$script_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
