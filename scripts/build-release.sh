#!/usr/bin/env bash
# build-release.sh -- assemble one immutable standalone candidate from a signed tag.
#
#   scripts/build-release.sh --tag vX.Y.Z-beta.N --version X.Y.Z-beta.N --desktop-dir PATH --out-dir ABSOLUTE_PATH
#       [--verify-reproducible] [--tools-dir DIR]
#   scripts/build-release.sh --rehearsal --source-commit SHA --version X.Y.Z-beta.N --out-dir ABSOLUTE_PATH
#       [--desktop-dir PATH] [--tag TAG] [--verify-reproducible] [--tools-dir DIR]
#       [--rollback-compatible-through N] [--predecessor-id sha256:HEX]
#
# The candidate form builds from the verified signed annotated tag of a clean checkout and fails closed (exit 3,
# not_run) while release/telos-tag-signers enrolls no signer. The rehearsal form builds an explicit commit and
# records itself as a rehearsal: it can never become a candidate. Details: scripts/lib/build_release.py.
#
# Called with only --candidate-lock / --evidence-out / --result-out (the gate-runner shape) it reports not_run:
# a gate has no candidate to build from, the builder is run by the release workflow, not by a gate.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for argument in "$@"; do
  case "$argument" in
    --version|--tag|--out-dir|--desktop-dir|--rehearsal|--source-commit|--verify-reproducible|--tools-dir|--rollback-compatible-through|--predecessor-id)
      exec python3 -I "$script_dir/lib/build_release.py" build "$@" ;;
  esac
done

original_argv=("$0" "$@")
candidate_lock=""
evidence_out=""
result_out=""
usage() { echo "usage: $0 --version V --out-dir DIR ... | [--candidate-lock PATH] [--evidence-out PATH] [--result-out PATH]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-lock) [[ $# -ge 2 && -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage; candidate_lock="$2"; shift 2 ;;
    --evidence-out) [[ $# -ge 2 && -z "$evidence_out" && -n "$2" && "$2" != --* ]] || usage; evidence_out="$2"; shift 2 ;;
    --result-out) [[ $# -ge 2 && -z "$result_out" && -n "$2" && "$2" != --* ]] || usage; result_out="$2"; shift 2 ;;
    *) usage ;;
  esac
done
arguments=(--gate-id "${TELOS_GATE_ID:-release-build}" --reason "the release builder is run by the candidate workflow (scripts/build-release.sh --version ... --out-dir ...), not by a gate")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
exec python3 "$script_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
