#!/usr/bin/env bash
# scan-candidate.sh -- the `candidate-supply-chain` gate: secrets, vulnerabilities and licenses of a frozen graph.
#
#   scripts/scan-candidate.sh --candidate-lock PATH --artifact-root DIR --sbom-dir DIR --result-out PATH \
#       [--source-dir DIR] [--tools-dir DIR]
#   scripts/scan-candidate.sh --rehearsal-lock PATH --source-commit SHA --artifact-root DIR --sbom-dir DIR --result-out PATH ...
#
# Run only AFTER freeze: the SBOMs, the lock and the artifacts are verified against each other first; gitleaks scans
# the exact source tree (--source-dir), Trivy each OCI/desktop artifact against a database at most 24 hours old whose
# digest and retrieval time are recorded, licenses are judged by the policy in ci/license-policy.json and every High
# needs an exact, unexpired entry in ci/risk-acceptances.json. Any secret or Critical fails. Without a graph to scan it
# reports not_run (exit 3) and never a pass.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
original_argv=("$0" "$@")
for argument in "$@"; do
  if [[ "$argument" == --sbom-dir ]]; then
    tools="${TELOS_CI_TOOLS_DIR:-$HOME/.cache/telos-ci-tools}"
    have_tools=0; for argument2 in "$@"; do [[ "$argument2" == --tools-dir ]] && have_tools=1; done
    if [[ $have_tools -eq 1 ]]; then exec python3 -I "$script_dir/lib/supply_chain.py" scan "$@"; fi
    exec python3 -I "$script_dir/lib/supply_chain.py" scan --tools-dir "$tools" "$@"
  fi
done

candidate_lock=""
evidence_out=""
result_out=""
usage() { echo "usage: $0 --candidate-lock PATH --artifact-root DIR --sbom-dir DIR --result-out PATH [--source-dir DIR]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-lock) [[ $# -ge 2 && -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage; candidate_lock="$2"; shift 2 ;;
    --evidence-out) [[ $# -ge 2 && -z "$evidence_out" && -n "$2" && "$2" != --* ]] || usage; evidence_out="$2"; shift 2 ;;
    --result-out) [[ $# -ge 2 && -z "$result_out" && -n "$2" && "$2" != --* ]] || usage; result_out="$2"; shift 2 ;;
    *) usage ;;
  esac
done
arguments=(--gate-id "${TELOS_GATE_ID:-candidate-supply-chain}" --reason "no artifact graph or SBOM directory was supplied: the candidate supply-chain gate scans a frozen candidate's artifacts and needs --artifact-root and --sbom-dir")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
exec python3 "$script_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
