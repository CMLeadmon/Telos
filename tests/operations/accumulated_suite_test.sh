#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT

printf '{"schemaVersion":1,"sourceCommit":"%040d"}\n' 0 >"$fixture_dir/candidate.json"

expect_usage_error() {
  local evidence_dir="$1"
  shift
  local status
  set +e
  bash "$repo_root/scripts/verify-accumulated-suite.sh" "$@" >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 2 ]] || { echo "expected usage exit 2, got $status: $*" >&2; exit 1; }
  [[ ! -e "$evidence_dir" ]] || { echo "usage error created evidence: $evidence_dir" >&2; exit 1; }
}

expect_usage_error "$fixture_dir/no-args" \
  --candidate-lock "$fixture_dir/candidate.json"
expect_usage_error "$fixture_dir/unknown" \
  --candidate-lock "$fixture_dir/candidate.json" --evidence-dir "$fixture_dir/unknown" --unknown
expect_usage_error "$fixture_dir/missing-value" \
  --candidate-lock "$fixture_dir/candidate.json" --evidence-dir
expect_usage_error "$fixture_dir/bad-phase" \
  --candidate-lock "$fixture_dir/candidate.json" --evidence-dir "$fixture_dir/bad-phase" --phase 0
expect_usage_error "$fixture_dir/duplicate" \
  --candidate-lock "$fixture_dir/candidate.json" --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$fixture_dir/duplicate"

bash "$repo_root/scripts/verify-accumulated-suite.sh" \
  --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$fixture_dir/evidence" \
  --gate evidence-envelope

python3 - "$fixture_dir/evidence/evidence-envelope.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as input_file:
    envelope = json.load(input_file)
assert envelope["gateId"] == "evidence-envelope", envelope
assert envelope["status"] == "passed", envelope
assert envelope["command"] == ["bash", "tests/operations/evidence_envelope_test.sh"], envelope
PY
python3 "$repo_root/scripts/lib/evidence.py" verify \
  --schema "$repo_root/release/evidence-envelope.schema.json" \
  --candidate-lock "$fixture_dir/candidate.json" \
  "$fixture_dir/evidence/evidence-envelope.json"
