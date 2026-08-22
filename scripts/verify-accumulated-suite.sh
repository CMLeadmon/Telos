#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
candidate_lock=""
evidence_dir=""
phase=""
gates=()

usage() {
  echo "usage: $0 --candidate-lock PATH --evidence-dir PATH [--phase N] [--gate ID]" >&2
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-lock)
      [[ $# -ge 2 && -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage
      candidate_lock="$2"
      shift 2
      ;;
    --evidence-dir)
      [[ $# -ge 2 && -z "$evidence_dir" && -n "$2" && "$2" != --* ]] || usage
      evidence_dir="$2"
      shift 2
      ;;
    --phase)
      [[ $# -ge 2 && -z "$phase" && "$2" =~ ^[1-9][0-9]*$ ]] || usage
      phase="$2"
      shift 2
      ;;
    --gate)
      [[ $# -ge 2 && -n "$2" && "$2" != --* ]] || usage
      gates+=("$2")
      shift 2
      ;;
    *) usage ;;
  esac
done

[[ -n "$candidate_lock" && -n "$evidence_dir" ]] || usage

arguments=(
  "$repo_root/scripts/run-gates.py"
  --catalog "$repo_root/ci/phase-gates.json"
  --scope local
  --candidate-lock "$candidate_lock"
  --evidence-dir "$evidence_dir"
)
[[ -n "$phase" ]] && arguments+=(--phase "$phase")
for gate in "${gates[@]}"; do
  arguments+=(--gate "$gate")
done

exec python3 "${arguments[@]}"
