#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
original_argv=("$0" "$@")
candidate_lock=""
evidence_out=""
compose=""
env_file=""
usage() { echo "usage: $0 [--compose PATH] [--env-file PATH] [--candidate-lock PATH] [--evidence-out PATH]" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --compose) [[ $# -ge 2 && -z "$compose" ]] || usage; compose="$2"; shift 2 ;;
    --env-file) [[ $# -ge 2 && -z "$env_file" ]] || usage; env_file="$2"; shift 2 ;;
    --candidate-lock) [[ $# -ge 2 && -z "$candidate_lock" ]] || usage; candidate_lock="$2"; shift 2 ;;
    --evidence-out) [[ $# -ge 2 && -z "$evidence_out" ]] || usage; evidence_out="$2"; shift 2 ;;
    *) usage ;;
  esac
done
arguments=(--gate-id runtime-hardening --reason "effective-runtime inspection is implemented in Phase 4")
[[ -n "$candidate_lock" ]] && arguments+=(--candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
exec python3 "$script_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
