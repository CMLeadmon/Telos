#!/usr/bin/env bash
# upgrade-rollback.sh -- the `upgrade-rollback` gate: upgrade a real node to the candidate, undo it the way the candidate's
# schema allows (binary rollback, or a restore from the recorded snapshot), and upgrade again.
#
#   scripts/drills/upgrade-rollback.sh --target-file PATH
#       (--rehearsal --source-commit SHA --rehearsal-lock PATH | --candidate-lock PATH) --result-out PATH
#
# Run it from a machine that is NOT the node. The node must already serve the predecessor generation (the descriptor's
# predecessorGeneration) with the candidate installed beside it, and the descriptor must carry the confirmation
# UPGRADE-AND-ROLL-BACK-THIS-NODE. The drill sends the fixed node-side program scripts/lib/upgrade_node.py, runs the
# candidate's own upgrade.sh / rollback.sh / restore.sh, and judges the journal, snapshot, receipt, markers, member
# journeys and the final runtime (see scripts/lib/upgrade_drill.py). Without a target it reports not_run (exit 3) and
# never a pass; fixtures and the hermetic lifecycle suites can never satisfy this gate.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
scripts_dir="$(cd "$script_dir/.." && pwd)"
original_argv=("$0" "$@")
for argument in "$@"; do
  if [[ "$argument" == --target-file ]]; then
    exec python3 -I "$scripts_dir/lib/upgrade_drill.py" "$@"
  fi
done

candidate_lock=""
evidence_out=""
result_out=""
usage() { echo "usage: $0 --target-file PATH (--rehearsal --source-commit SHA --rehearsal-lock PATH | --candidate-lock PATH) --result-out PATH" >&2; exit 2; }
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
reason="no node was supplied: the upgrade-rollback gate upgrades, rolls back and re-upgrades a real node from another machine and needs --target-file (a protected drill-target descriptor naming the predecessor generation and the confirmation)"
arguments=(--gate-id "${TELOS_GATE_ID:-upgrade-rollback}" --reason "$reason")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
exec python3 "$scripts_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
