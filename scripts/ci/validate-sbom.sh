#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
original_argv=("$0" "$@")
# validate-sbom.sh --candidate-lock PATH --sbom-dir DIR [--max-age-hours N]: the semantic check -- schema, coverage,
# package uniqueness, artifact/SBOM digests, creator tool and candidate binding. Without --sbom-dir there is nothing
# to validate and the gate placeholder below reports not_run; the old directory-exists "success" is gone.
for argument in "$@"; do
  if [[ "$argument" == --sbom-dir ]]; then
    exec python3 -I "$repo_root/lib/supply_chain.py" validate-sbom "$@"
  fi
done
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
arguments=(--gate-id "${TELOS_GATE_ID:-sbom-validation}" --reason "candidate SBOM validation is implemented in Phase 4")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
exec python3 "$repo_root/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
