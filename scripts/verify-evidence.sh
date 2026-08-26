#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 --candidate-lock PATH --evidence-dir PATH" >&2
  exit 2
}

candidate_lock=""
evidence_dir=""
while (($#)); do
  case "$1" in
    --candidate-lock)
      (($# >= 2)) || usage
      [[ -z "$candidate_lock" && -n "$2" && "$2" != --* ]] || usage
      candidate_lock="$2"
      shift 2
      ;;
    --evidence-dir)
      (($# >= 2)) || usage
      [[ -z "$evidence_dir" && -n "$2" && "$2" != --* ]] || usage
      evidence_dir="$2"
      shift 2
      ;;
    *)
      usage
      ;;
  esac
done

[[ -n "$candidate_lock" && -n "$evidence_dir" ]] || usage

has_parent_traversal() {
  local path="/$1/"
  [[ "$path" == *"/../"* ]]
}

has_parent_traversal "$candidate_lock" && usage
has_parent_traversal "$evidence_dir" && usage

repo_root="$(git rev-parse --show-toplevel)"
if [[ "$candidate_lock" != /* ]]; then
  candidate_lock="$repo_root/$candidate_lock"
fi
if [[ "$evidence_dir" != /* ]]; then
  evidence_dir="$repo_root/$evidence_dir"
fi

[[ -f "$candidate_lock" && ! -L "$candidate_lock" ]] || {
  echo "candidate lock is not a regular non-symlink file: $candidate_lock" >&2
  exit 1
}
[[ -d "$evidence_dir" && ! -L "$evidence_dir" ]] || {
  echo "evidence directory is not a non-symlink directory: $evidence_dir" >&2
  exit 1
}

mapfile -d '' envelopes < <(find "$evidence_dir" -maxdepth 1 -type f -name '*.json' -print0 | LC_ALL=C sort -z)
((${#envelopes[@]})) || { echo "no evidence envelopes found in: $evidence_dir" >&2; exit 1; }

python3 "$repo_root/scripts/lib/evidence.py" verify \
  --schema "$repo_root/release/evidence-envelope.schema.json" \
  --candidate-lock "$candidate_lock" "${envelopes[@]}"
