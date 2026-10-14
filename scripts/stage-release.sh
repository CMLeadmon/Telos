#!/usr/bin/env bash
# stage-release.sh -- stage a certified candidate as a GitHub DRAFT prerelease, then download and re-verify it.
#
#   scripts/stage-release.sh --tag vX.Y.Z-beta.N --candidate BUILD_OUT_DIR --evidence-bundle DIR \
#       --certification-dir DIR --review release-review.json --notes RELEASE-NOTES.md
#       [--catalog ci/phase-gates.json] [--signers release/evidence-signers] [--repo DIR]
#
# Verifies the signed tag, the candidate graph and checksum, the signed evidence bundle, the signed certification and
# a signed review by a different principal; requires the authenticated gh login to be on release/publisher-policy.json
# (an unreviewed policy is not_run, exit 3); creates the draft with --verify-tag and uploads every asset WITHOUT overwrite.
# Promotion is scripts/promote-release.sh. Called with only --candidate-lock / --evidence-out / --result-out it is the
# gate-runner placeholder and reports not_run.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for argument in "$@"; do
  case "$argument" in
    --tag|--candidate|--certification-dir|--review|--notes|--evidence-bundle)
      exec python3 -I "$script_dir/lib/stage_release.py" stage "$@" ;;
  esac
done

original_argv=("$0" "$@")
candidate_lock=""
evidence_out=""
result_out=""
usage() { echo "usage: $0 --tag TAG --candidate DIR --evidence-bundle DIR --certification-dir DIR --review FILE --notes FILE | [--candidate-lock PATH] [--evidence-out PATH] [--result-out PATH]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-lock) [[ $# -ge 2 && -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage; candidate_lock="$2"; shift 2 ;;
    --evidence-out) [[ $# -ge 2 && -z "$evidence_out" && -n "$2" && "$2" != --* ]] || usage; evidence_out="$2"; shift 2 ;;
    --result-out) [[ $# -ge 2 && -z "$result_out" && -n "$2" && "$2" != --* ]] || usage; result_out="$2"; shift 2 ;;
    *) usage ;;
  esac
done
arguments=(--gate-id "${TELOS_GATE_ID:-release-staging}" --reason "staging is an operator action on a certified candidate (scripts/stage-release.sh --tag ...), not a gate")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
exec python3 "$script_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
