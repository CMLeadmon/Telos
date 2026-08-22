#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
original_argv=("$0" "$@")
candidate_lock=""
evidence_out=""
usage() { echo "usage: $0 [--candidate-lock PATH] [--evidence-out PATH]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-lock) [[ $# -ge 2 && -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage; candidate_lock="$2"; shift 2 ;;
    --evidence-out) [[ $# -ge 2 && -z "$evidence_out" && -n "$2" && "$2" != --* ]] || usage; evidence_out="$2"; shift 2 ;;
    *) usage ;;
  esac
done
arguments=(--gate-id upgrade-rollback --reason "candidate and predecessor generations plus Phase 4 implementation are required")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
exec python3 "$repo_root/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
