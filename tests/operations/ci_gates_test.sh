#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
fixture_dir="$(mktemp -d)"
sentinel="/tmp/gate-injection-sentinel"
cleanup() {
  rm -rf "$fixture_dir"
  rm -f "$sentinel"
}
trap cleanup EXIT

if [[ -e "$sentinel" ]]; then
  echo "refusing to overwrite pre-existing injection sentinel: $sentinel" >&2
  exit 1
fi
if [[ -e "$repo_root/scripts/lib/__pycache__" ]]; then
  echo "test requires a clean scripts/lib import cache" >&2
  exit 1
fi

printf '{"schemaVersion":1,"sourceCommit":"%040d"}\n' 0 >"$fixture_dir/candidate.json"

write_catalog() {
  local mutation="$1"
  local output="$2"
  local marker="$3"
  python3 - "$mutation" "$output" "$marker" <<'PY'
import copy
import json
import sys

mutation, output, marker = sys.argv[1:]
gate = {
    "id": "first-gate",
    "phase": 1,
    "scope": "local",
    "required": True,
    "timeoutSeconds": 10,
    "command": [
        "python3", "-c",
        "import pathlib,sys; pathlib.Path(sys.argv[1]).write_text('ran', encoding='utf-8')",
        marker,
    ],
    "prerequisites": [],
    "subjectKind": "fixture",
}
catalog = {"schemaVersion": 1, "gates": [gate, {
    "id": "second-gate",
    "phase": 1,
    "scope": "local",
    "required": True,
    "timeoutSeconds": 10,
    "command": ["python3", "-c", "raise SystemExit(0)"],
    "prerequisites": [],
    "subjectKind": "fixture",
}]}

if mutation == "duplicate-ids":
    catalog["gates"].append(copy.deepcopy(gate))
elif mutation == "unknown-top-key":
    catalog["gatesPassed"] = True
elif mutation == "unknown-gate-key":
    catalog["gates"][1]["result"] = "passed"
elif mutation == "string-command":
    catalog["gates"][1]["command"] = "python3 -c pass"
elif mutation == "empty-command":
    catalog["gates"][1]["command"] = []
elif mutation == "invalid-scope":
    catalog["gates"][1]["scope"] = "remote"
elif mutation == "non-string-scope":
    catalog["gates"][1]["scope"] = []
elif mutation == "zero-timeout":
    catalog["gates"][1]["timeoutSeconds"] = 0
elif mutation == "negative-timeout":
    catalog["gates"][1]["timeoutSeconds"] = -1
elif mutation == "missing-prerequisite":
    catalog["gates"][1]["prerequisites"] = ["unknown-gate"]
elif mutation == "prerequisite-cycle":
    catalog["gates"][0]["prerequisites"] = ["second-gate"]
    catalog["gates"][1]["prerequisites"] = ["first-gate"]
elif mutation == "path-id":
    catalog["gates"][1]["id"] = "../escaped"
elif mutation == "non-string-subject":
    catalog["gates"][1]["subjectKind"] = []
elif mutation != "valid":
    raise SystemExit(f"unknown mutation: {mutation}")

with open(output, "w", encoding="utf-8") as output_file:
    json.dump(catalog, output_file, sort_keys=True)
PY
}

assert_catalog_rejected_before_execution() {
  local mutation="$1"
  local expected="$2"
  local catalog="$fixture_dir/$mutation.json"
  local marker="$fixture_dir/$mutation-ran"
  local evidence_dir="$fixture_dir/$mutation-evidence"
  local output status
  write_catalog "$mutation" "$catalog" "$marker"
  set +e
  output="$(python3 "$repo_root/scripts/run-gates.py" \
    --catalog "$catalog" --scope local \
    --candidate-lock "$fixture_dir/candidate.json" \
    --evidence-dir "$evidence_dir" 2>&1)"
  status=$?
  set -e
  [[ $status -eq 2 ]] || {
    echo "$mutation returned $status instead of catalog-validation exit 2: $output" >&2
    exit 1
  }
  grep -Fqi "$expected" <<<"$output" || {
    echo "$mutation did not report $expected: $output" >&2
    exit 1
  }
  [[ ! -e "$marker" ]] || { echo "$mutation executed before catalog validation" >&2; exit 1; }
  [[ ! -e "$evidence_dir" ]] || { echo "$mutation created evidence before catalog validation" >&2; exit 1; }
}

assert_catalog_rejected_before_execution duplicate-ids "duplicate gate id"
assert_catalog_rejected_before_execution unknown-top-key "unknown field"
assert_catalog_rejected_before_execution unknown-gate-key "unknown field"
assert_catalog_rejected_before_execution string-command "command"
assert_catalog_rejected_before_execution empty-command "command"
assert_catalog_rejected_before_execution invalid-scope "scope"
assert_catalog_rejected_before_execution non-string-scope "scope"
assert_catalog_rejected_before_execution zero-timeout "timeoutSeconds"
assert_catalog_rejected_before_execution negative-timeout "timeoutSeconds"
assert_catalog_rejected_before_execution missing-prerequisite "unknown prerequisite"
assert_catalog_rejected_before_execution prerequisite-cycle "cycle"
assert_catalog_rejected_before_execution path-id "id"
assert_catalog_rejected_before_execution non-string-subject "subjectKind"

injection='$(touch /tmp/gate-injection-sentinel)'
python3 - "$fixture_dir/injection.json" "$repo_root/tests/fixtures/gates/record_argv.py" \
  "$fixture_dir/recorded.json" "$injection" <<'PY'
import json
import sys

catalog_path, fixture, record, injection = sys.argv[1:]
catalog = {"schemaVersion": 1, "gates": [{
    "id": "argv-safe",
    "phase": 1,
    "scope": "local",
    "required": True,
    "timeoutSeconds": 10,
    "command": ["python3", fixture, record, injection],
    "prerequisites": [],
    "subjectKind": "fixture",
}]}
with open(catalog_path, "w", encoding="utf-8") as output_file:
    json.dump(catalog, output_file, sort_keys=True)
PY

python3 "$repo_root/scripts/run-gates.py" \
  --catalog "$fixture_dir/injection.json" --scope local \
  --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$fixture_dir/injection-evidence"
[[ ! -e "$sentinel" ]] || { echo "catalog argv was evaluated by a shell" >&2; exit 1; }
python3 - "$fixture_dir/recorded.json" "$injection" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as input_file:
    recorded = json.load(input_file)
if recorded != [sys.argv[2]]:
    raise SystemExit(f"argv was not preserved literally: {recorded!r}")
PY
python3 - "$fixture_dir/injection-evidence/argv-safe.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as input_file:
    envelope = json.load(input_file)
assert envelope["status"] == "passed", envelope
assert envelope["exitCode"] == 0, envelope
PY
[[ "$(stat -c '%a' "$fixture_dir/injection-evidence")" == 700 ]]
[[ "$(stat -c '%a' "$fixture_dir/injection-evidence/argv-safe.log")" == 600 ]]
[[ "$(stat -c '%a' "$fixture_dir/injection-evidence/argv-safe.json")" == 600 ]]
python3 "$repo_root/scripts/lib/evidence.py" verify \
  --schema "$repo_root/release/evidence-envelope.schema.json" \
  --candidate-lock "$fixture_dir/candidate.json" \
  "$fixture_dir/injection-evidence/argv-safe.json"

python3 - "$fixture_dir/failed.json" <<'PY'
import json
import sys

catalog = {"schemaVersion": 1, "gates": [{
    "id": "required-failure", "phase": 1, "scope": "local", "required": True,
    "timeoutSeconds": 10,
    "command": ["python3", "-c", "print('assertion failed'); raise SystemExit(7)"],
    "prerequisites": [], "subjectKind": "fixture",
}]}
with open(sys.argv[1], "w", encoding="utf-8") as output_file:
    json.dump(catalog, output_file, sort_keys=True)
PY
set +e
python3 "$repo_root/scripts/run-gates.py" \
  --catalog "$fixture_dir/failed.json" --scope local \
  --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$fixture_dir/failed-evidence"
status=$?
set -e
[[ $status -ne 0 ]] || { echo "required failed gate returned success" >&2; exit 1; }
python3 - "$fixture_dir/failed-evidence/required-failure.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as input_file:
    envelope = json.load(input_file)
assert envelope["status"] == "failed", envelope
assert envelope["exitCode"] == 7, envelope
PY
grep -Fq "assertion failed" "$fixture_dir/failed-evidence/required-failure.log"

write_dependency_catalog() {
  local prerequisite_mode="$1"
  local catalog="$2"
  local dependent_marker="$3"
  python3 - "$prerequisite_mode" "$catalog" "$dependent_marker" <<'PY'
import json
import sys

mode, catalog_path, marker = sys.argv[1:]
if mode == "missing-executable":
    command = ["/definitely/missing/telos-gate-command"]
else:
    command = ["python3", "-c", "raise SystemExit(9)"]
catalog = {"schemaVersion": 1, "gates": [
    {
        "id": "prerequisite", "phase": 1, "scope": "local", "required": True,
        "timeoutSeconds": 10, "command": command, "prerequisites": [],
        "subjectKind": "fixture",
    },
    {
        "id": "dependent", "phase": 1, "scope": "local", "required": True,
        "timeoutSeconds": 10,
        "command": [
            "python3", "-c",
            "import pathlib,sys; pathlib.Path(sys.argv[1]).write_text('ran', encoding='utf-8')",
            marker,
        ],
        "prerequisites": ["prerequisite"], "subjectKind": "fixture",
    },
]}
with open(catalog_path, "w", encoding="utf-8") as output_file:
    json.dump(catalog, output_file, sort_keys=True)
PY
}

for prerequisite_mode in missing-executable failed; do
  dependency_catalog="$fixture_dir/dependency-$prerequisite_mode.json"
  dependency_evidence="$fixture_dir/dependency-$prerequisite_mode-evidence"
  dependent_marker="$fixture_dir/dependent-$prerequisite_mode-ran"
  write_dependency_catalog "$prerequisite_mode" "$dependency_catalog" "$dependent_marker"
  set +e
  python3 "$repo_root/scripts/run-gates.py" \
    --catalog "$dependency_catalog" --scope local --gate dependent \
    --candidate-lock "$fixture_dir/candidate.json" \
    --evidence-dir "$dependency_evidence"
  status=$?
  set -e
  [[ $status -ne 0 ]] || { echo "$prerequisite_mode dependency returned success" >&2; exit 1; }
  [[ ! -e "$dependent_marker" ]] || { echo "dependent ran after $prerequisite_mode prerequisite" >&2; exit 1; }
  python3 - "$dependency_evidence/prerequisite.json" \
    "$dependency_evidence/dependent.json" "$prerequisite_mode" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as input_file:
    prerequisite = json.load(input_file)
with open(sys.argv[2], encoding="utf-8") as input_file:
    dependent = json.load(input_file)
expected = "not_run" if sys.argv[3] == "missing-executable" else "failed"
assert prerequisite["status"] == expected, prerequisite
assert dependent["status"] == "not_run", dependent
assert dependent["exitCode"] == 3, dependent
PY
done

write_catalog valid "$fixture_dir/reuse.json" "$fixture_dir/reuse-ran"
mkdir "$fixture_dir/existing-evidence"
set +e
python3 "$repo_root/scripts/run-gates.py" \
  --catalog "$fixture_dir/reuse.json" --scope local \
  --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$fixture_dir/existing-evidence" >/dev/null 2>&1
status=$?
set -e
[[ $status -ne 0 ]] || { echo "runner reused an existing evidence directory" >&2; exit 1; }
[[ -z "$(find "$fixture_dir/existing-evidence" -mindepth 1 -print -quit)" ]]

mkdir "$fixture_dir/outside-evidence"
ln -s "$fixture_dir/outside-evidence" "$fixture_dir/evidence-link"
set +e
python3 "$repo_root/scripts/run-gates.py" \
  --catalog "$fixture_dir/reuse.json" --scope local \
  --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$fixture_dir/evidence-link" >/dev/null 2>&1
status=$?
set -e
[[ $status -ne 0 ]] || { echo "runner followed an evidence-directory symlink" >&2; exit 1; }
[[ -z "$(find "$fixture_dir/outside-evidence" -mindepth 1 -print -quit)" ]]

python3 - "$repo_root/ci/phase-gates.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as input_file:
    catalog = json.load(input_file)
assert catalog.get("schemaVersion") == 1, catalog
assert "gatesPassed" not in catalog, catalog
ids = [gate["id"] for gate in catalog["gates"]]
assert len(ids) == len(set(ids)), "repository catalog has duplicate IDs"
required_external = {
    "clean-install", "browser-matrix", "desktop-linux", "desktop-windows",
    "desktop-macos", "runtime-hardening", "monitoring", "encrypted-backup",
    "separate-host-restore", "upgrade-rollback", "capacity", "host-exposure",
    "controlled-egress", "candidate-supply-chain",
}
missing = sorted(required_external - set(ids))
assert not missing, f"repository catalog is missing external gates: {missing}"
PY

[[ ! -e "$repo_root/scripts/lib/__pycache__" ]] || {
  echo "gate runner wrote Python cache outside the evidence directory" >&2
  exit 1
}
