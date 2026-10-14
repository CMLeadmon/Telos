#!/usr/bin/env bash
# backup.sh -- the `encrypted-backup` gate: take a real backup on the node, then judge it from the repository's side.
#
#   scripts/drills/backup.sh --target-file PATH (--rehearsal --source-commit SHA | --candidate-lock PATH) --result-out PATH
#
# Run from a machine that is NOT the node and that can read the off-node restic repository with its own copy of the
# password (credentials file keys backupRepository / backupPasswordFile). The node runs scripts/backup.sh once; this
# machine finds the snapshot, proves the repository refuses a wrong password, runs `restic check`, validates the manifest
# dumped from the repository and compares every component, and checks the node left no plaintext behind (see
# scripts/lib/backup_gate.py). Without a target it reports not_run (exit 3) and never a pass.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
scripts_dir="$(cd "$script_dir/.." && pwd)"
original_argv=("$0" "$@")
for argument in "$@"; do
  if [[ "$argument" == --target-file ]]; then
    exec python3 -I "$scripts_dir/lib/backup_gate.py" "$@"
  fi
done

candidate_lock=""
evidence_out=""
result_out=""
usage() { echo "usage: $0 --target-file PATH (--rehearsal --source-commit SHA | --candidate-lock PATH) --result-out PATH" >&2; exit 2; }
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
reason="no node was supplied: the encrypted-backup gate takes a real backup on a real node and needs --target-file (a protected drill-target descriptor)"
if [[ -z "$candidate_lock" ]]; then
  echo "backup: not_run: $reason" >&2
  exit 3
fi
arguments=(--gate-id "${TELOS_GATE_ID:-encrypted-backup}" --reason "$reason" --candidate-lock "$candidate_lock")
[[ -n "$evidence_out" ]] && arguments+=(--evidence-out "$evidence_out")
[[ -n "$result_out" ]] && arguments+=(--result-out "$result_out")
exec python3 "$scripts_dir/not-run.py" "${arguments[@]}" -- "${original_argv[@]}"
