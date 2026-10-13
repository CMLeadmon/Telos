#!/usr/bin/env bash
# clean-node-install.sh — judge a freshly installed Telos node (the `clean-install` gate).
#
#   scripts/drills/clean-node-install.sh --target-file PATH --fixture-manifest PATH \
#     (--rehearsal --source-commit SHA [--rehearsal-lock PATH] | --candidate-lock PATH) \
#     --result-out PATH
#
# Run it from a verifier machine that is NOT the node. It only observes: install
# facts over a fixed read-only collector, public TLS, and the member journey. The
# operator installs by following documentation/operations/clean-install-evidence.md.
# Without a target descriptor nothing can be observed, so it reports not_run (3) and
# never a pass. The gate runner, not this script, writes the evidence envelope.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
scripts_dir="$(cd "$script_dir/.." && pwd)"
original_argv=("$0" "$@")

for argument in "$@"; do
  if [[ "$argument" == --target-file ]]; then
    exec python3 -I "$scripts_dir/lib/clean_install_drill.py" "$@"
  fi
done

candidate_lock=""
evidence_out=""
result_out=""
usage() { echo "usage: $0 --target-file PATH --fixture-manifest PATH (--rehearsal --source-commit SHA | --candidate-lock PATH) --result-out PATH" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-lock) [[ $# -ge 2 && -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage; candidate_lock="$2"; shift 2 ;;
    --evidence-out) [[ $# -ge 2 && -z "$evidence_out" && -n "$2" && "$2" != --* ]] || usage; evidence_out="$2"; shift 2 ;;
    --result-out) [[ $# -ge 2 && -z "$result_out" && -n "$2" && "$2" != --* ]] || usage; result_out="$2"; shift 2 ;;
    --fixture-manifest|--source-commit|--rehearsal-lock) [[ $# -ge 2 && -n "$2" && "$2" != --* ]] || usage; shift 2 ;;
    --rehearsal) shift ;;
    *) usage ;;
  esac
done
reason="no drill target was supplied: the clean-install gate observes a real node and needs --target-file (a protected drill-target descriptor) and --fixture-manifest"
arguments=(--gate-id "${TELOS_GATE_ID:-clean-install}" --reason "$reason")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
if [[ -z "$candidate_lock" ]]; then
  echo "clean-node-install: not_run: $reason" >&2
  exit 3
fi
exec python3 "$scripts_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
