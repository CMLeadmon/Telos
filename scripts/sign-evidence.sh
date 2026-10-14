#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# With --bundle-dir this signs an imported evidence bundle (TELOS_EVIDENCE_SIGNING_KEY, TELOS_EVIDENCE_SIGNING_PRINCIPAL):
#   sign-evidence.sh --candidate-lock PATH --catalog PATH --bundle-dir DIR [--signers FILE]
# Without them it is the gate-runner placeholder and reports not_run.
for argument in "$@"; do
  case "$argument" in
    --bundle-dir)
      exec python3 -I "$script_dir/lib/evidence_bundle.py" sign "$@" ;;
  esac
done
original_argv=("$0" "$@")
candidate_lock=""
evidence_out=""
result_out=""
usage() { echo "usage: $0 [--candidate-lock PATH] [--evidence-out PATH] [--result-out PATH]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-lock) [[ $# -ge 2 && -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage; candidate_lock="$2"; shift 2 ;;
    --evidence-out) [[ $# -ge 2 && -z "$evidence_out" && -n "$2" && "$2" != --* ]] || usage; evidence_out="$2"; shift 2 ;;
    --result-out) [[ $# -ge 2 && -z "$result_out" && -n "$2" && "$2" != --* ]] || usage; result_out="$2"; shift 2 ;;
    *) usage ;;
  esac
done
arguments=(--gate-id "${TELOS_GATE_ID:-evidence-signing}" --reason "operator signing is implemented in Phase 5")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
exec python3 "$script_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
