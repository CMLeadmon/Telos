#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT

printf '{"schemaVersion":1,"sourceCommit":"%s"}\n' \
  "$(git -C "$repo_root" rev-parse HEAD)" >"$fixture_dir/candidate.json"

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

accumulated_repo="$fixture_dir/accumulated-repo"
mkdir -p "$accumulated_repo/scripts/lib" "$accumulated_repo/ci"
cp "$repo_root/scripts/verify-accumulated-suite.sh" "$accumulated_repo/scripts/verify-accumulated-suite.sh"
cp "$repo_root/scripts/run-gates.py" "$accumulated_repo/scripts/run-gates.py"
cp "$repo_root/scripts/lib/evidence.py" "$accumulated_repo/scripts/lib/evidence.py"
printf '__pycache__/\n' >"$accumulated_repo/.gitignore"
python3 - "$accumulated_repo/ci/phase-gates.json" "$repo_root/scripts/verify-release-pins.sh" <<'PY'
import json
import sys

catalog_path, wrapper = sys.argv[1:]
catalog = {"schemaVersion": 1, "gates": [
    {
        "id": "source-prerequisite", "phase": 1, "scope": "local",
        "required": True, "timeoutSeconds": 10,
        "command": ["python3", "-c", "print('source prerequisite ran')"],
        "prerequisites": [], "subjectKind": "source",
    },
    {
        "id": "artifact-later-phase", "phase": 2, "scope": "local",
        "required": True, "timeoutSeconds": 10,
        "command": ["bash", wrapper],
        "prerequisites": ["source-prerequisite"], "subjectKind": "artifact",
    },
]}
with open(catalog_path, "w", encoding="utf-8") as output_file:
    json.dump(catalog, output_file, sort_keys=True)
PY
git -C "$accumulated_repo" init -q
git -C "$accumulated_repo" config user.name "Accumulated Fixture"
git -C "$accumulated_repo" config user.email "accumulated-fixture@example.invalid"
git -C "$accumulated_repo" add .
git -C "$accumulated_repo" commit -qm "accumulated fixture"
printf '{"schemaVersion":1,"sourceCommit":"%s"}\n' \
  "$(git -C "$accumulated_repo" rev-parse HEAD)" >"$fixture_dir/accumulated-candidate.json"

set +e
bash "$accumulated_repo/scripts/verify-accumulated-suite.sh" \
  --candidate-lock "$fixture_dir/accumulated-candidate.json" \
  --evidence-dir "$fixture_dir/evidence"
status=$?
set -e
[[ $status -ne 0 ]] || { echo "default all-local wrapper returned success" >&2; exit 1; }

python3 - "$fixture_dir/evidence" <<'PY'
import json
import pathlib
import sys

evidence_dir = pathlib.Path(sys.argv[1])
source = json.loads((evidence_dir / "source-prerequisite.json").read_text())
artifact = json.loads((evidence_dir / "artifact-later-phase.json").read_text())
assert source["status"] == "passed", source
assert artifact["status"] == "not_run", artifact
assert artifact["exitCode"] == 3, artifact
assert "unavailable" in artifact["reason"], artifact
assert (evidence_dir / "source-prerequisite.log").is_file()
assert (evidence_dir / "artifact-later-phase.log").is_file()
PY
python3 "$repo_root/scripts/lib/evidence.py" verify \
  --schema "$repo_root/release/evidence-envelope.schema.json" \
  --candidate-lock "$fixture_dir/accumulated-candidate.json" \
  "$fixture_dir/evidence/source-prerequisite.json" \
  "$fixture_dir/evidence/artifact-later-phase.json"
