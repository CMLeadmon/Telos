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
elif mutation == "float-schema-version":
    catalog["schemaVersion"] = 1.0
elif mutation == "unknown-top-key":
    catalog["gatesPassed"] = True
elif mutation == "unknown-gate-key":
    catalog["gates"][1]["result"] = "passed"
elif mutation == "string-command":
    catalog["gates"][1]["command"] = "python3 -c pass"
elif mutation == "empty-command":
    catalog["gates"][1]["command"] = []
elif mutation == "nul-command":
    catalog["gates"][1]["command"][1] = "bad\0argument"
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
assert_catalog_rejected_before_execution float-schema-version "schemaVersion"
assert_catalog_rejected_before_execution unknown-top-key "unknown field"
assert_catalog_rejected_before_execution unknown-gate-key "unknown field"
assert_catalog_rejected_before_execution string-command "command"
assert_catalog_rejected_before_execution empty-command "command"
assert_catalog_rejected_before_execution nul-command "NUL"
assert_catalog_rejected_before_execution invalid-scope "scope"
assert_catalog_rejected_before_execution non-string-scope "scope"
assert_catalog_rejected_before_execution zero-timeout "timeoutSeconds"
assert_catalog_rejected_before_execution negative-timeout "timeoutSeconds"
assert_catalog_rejected_before_execution missing-prerequisite "unknown prerequisite"
assert_catalog_rejected_before_execution prerequisite-cycle "cycle"
assert_catalog_rejected_before_execution path-id "id"
assert_catalog_rejected_before_execution non-string-subject "subjectKind"

source_repo="$fixture_dir/source-repo"
mkdir -p "$source_repo/scripts/lib"
cp "$repo_root/scripts/run-gates.py" "$source_repo/scripts/run-gates.py"
cp "$repo_root/scripts/lib/evidence.py" "$source_repo/scripts/lib/evidence.py"
printf '__pycache__/\nignored-build/\n' >"$source_repo/.gitignore"
printf 'tracked fixture\n' >"$source_repo/tracked.txt"
git -C "$source_repo" init -q
git -C "$source_repo" config user.name "Gate Fixture"
git -C "$source_repo" config user.email "gate-fixture@example.invalid"
git -C "$source_repo" add .
git -C "$source_repo" commit -qm "source fixture"
source_runner="$source_repo/scripts/run-gates.py"
current_commit="$(git -C "$source_repo" rev-parse HEAD)"
printf '{"schemaVersion":1,"sourceCommit":"%s"}\n' "$current_commit" >"$fixture_dir/matching-candidate.json"

set +e
python3 "$source_runner" \
  --catalog "$fixture_dir/float-schema-version.json" --scope local \
  --candidate-lock "$fixture_dir/matching-candidate.json" \
  --evidence-dir "$fixture_dir/bytecode-evidence" >/dev/null 2>&1
status=$?
set -e
[[ $status -eq 2 ]]
[[ ! -e "$source_repo/scripts/lib/__pycache__" ]] || {
  echo "runner imported evidence bytecode before catalog validation" >&2
  exit 1
}

python3 - "$fixture_dir/source.json" "$fixture_dir/source-ran" <<'PY'
import json
import sys

catalog_path, marker = sys.argv[1:]
catalog = {"schemaVersion": 1, "gates": [{
    "id": "source-binding", "phase": 1, "scope": "local", "required": True,
    "timeoutSeconds": 10,
    "command": [
        "python3", "-c",
        "import pathlib,sys; pathlib.Path(sys.argv[1]).write_text('ran', encoding='utf-8')",
        marker,
    ],
    "prerequisites": [], "subjectKind": "source",
}]}
with open(catalog_path, "w", encoding="utf-8") as output_file:
    json.dump(catalog, output_file, sort_keys=True)
PY

set +e
source_mismatch_output="$(python3 "$source_runner" \
  --catalog "$fixture_dir/source.json" --scope local \
  --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$fixture_dir/source-mismatch-evidence" 2>&1)"
status=$?
set -e
[[ $status -eq 2 ]] || { echo "source mismatch returned $status" >&2; exit 1; }
grep -Fqi "sourceCommit" <<<"$source_mismatch_output"
[[ ! -e "$fixture_dir/source-ran" ]] || { echo "source gate ran for a mismatched candidate" >&2; exit 1; }
[[ ! -e "$fixture_dir/source-mismatch-evidence" ]] || { echo "source mismatch wrote evidence" >&2; exit 1; }

python3 "$source_runner" \
  --catalog "$fixture_dir/source.json" --scope local \
  --candidate-lock "$fixture_dir/matching-candidate.json" \
  --evidence-dir "$fixture_dir/source-evidence"
python3 - "$fixture_dir/source-evidence/source-binding.json" \
  "$fixture_dir/matching-candidate.json" "$current_commit" <<'PY'
import hashlib
import json
import sys

envelope_path, candidate_path, source_commit = sys.argv[1:]
with open(envelope_path, encoding="utf-8") as input_file:
    envelope = json.load(input_file)
with open(candidate_path, "rb") as input_file:
    candidate_bytes = input_file.read()
assert envelope["status"] == "passed", envelope
assert envelope["candidateLockDigest"] == "sha256:" + hashlib.sha256(candidate_bytes).hexdigest(), envelope
assert envelope["subject"] == {
    "kind": "source",
    "digest": "sha256:" + hashlib.sha256(source_commit.encode("ascii")).hexdigest(),
}, envelope
assert envelope["candidateLockDigest"] != envelope["subject"]["digest"], envelope
PY
unlink "$fixture_dir/source-ran"

mkdir "$source_repo/ignored-build"
printf 'ignored build output\n' >"$source_repo/ignored-build/artifact.txt"
python3 "$source_runner" \
  --catalog "$fixture_dir/source.json" --scope local \
  --candidate-lock "$fixture_dir/matching-candidate.json" \
  --evidence-dir "$fixture_dir/source-ignored-evidence"
unlink "$fixture_dir/source-ran"

printf 'dirty tracked change\n' >>"$source_repo/tracked.txt"
set +e
tracked_dirty_output="$(python3 "$source_runner" \
  --catalog "$fixture_dir/source.json" --scope local \
  --candidate-lock "$fixture_dir/matching-candidate.json" \
  --evidence-dir "$fixture_dir/source-tracked-dirty-evidence" 2>&1)"
status=$?
set -e
[[ $status -eq 2 ]] || { echo "dirty tracked source returned $status" >&2; exit 1; }
grep -Fqi "dirty" <<<"$tracked_dirty_output"
[[ ! -e "$fixture_dir/source-ran" ]]
[[ ! -e "$fixture_dir/source-tracked-dirty-evidence" ]]
git -C "$source_repo" restore --worktree -- tracked.txt

printf 'dirty untracked path\n' >"$source_repo/untracked.txt"
set +e
untracked_dirty_output="$(python3 "$source_runner" \
  --catalog "$fixture_dir/source.json" --scope local \
  --candidate-lock "$fixture_dir/matching-candidate.json" \
  --evidence-dir "$fixture_dir/source-untracked-dirty-evidence" 2>&1)"
status=$?
set -e
[[ $status -eq 2 ]] || { echo "dirty untracked source returned $status" >&2; exit 1; }
grep -Fqi "dirty" <<<"$untracked_dirty_output"
[[ ! -e "$fixture_dir/source-ran" ]]
[[ ! -e "$fixture_dir/source-untracked-dirty-evidence" ]]
unlink "$source_repo/untracked.txt"

python3 - "$fixture_dir/artifact.json" "$repo_root/scripts/verify-release-pins.sh" <<'PY'
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
        "id": "artifact-binding", "phase": 2, "scope": "local",
        "required": True, "timeoutSeconds": 10,
        "command": ["bash", wrapper],
        "prerequisites": ["source-prerequisite"], "subjectKind": "artifact",
    },
]}
with open(catalog_path, "w", encoding="utf-8") as output_file:
    json.dump(catalog, output_file, sort_keys=True)
PY
set +e
python3 "$source_runner" \
  --catalog "$fixture_dir/artifact.json" --scope local \
  --gate artifact-binding \
  --candidate-lock "$fixture_dir/matching-candidate.json" \
  --evidence-dir "$fixture_dir/artifact-evidence"
status=$?
set -e
[[ $status -ne 0 ]] || { echo "unavailable artifact subject returned success" >&2; exit 1; }
python3 - "$fixture_dir/artifact-evidence" "$fixture_dir/matching-candidate.json" <<'PY'
import hashlib
import json
import pathlib
import sys

evidence_dir = pathlib.Path(sys.argv[1])
candidate_path = pathlib.Path(sys.argv[2])
source = json.loads((evidence_dir / "source-prerequisite.json").read_text())
artifact = json.loads((evidence_dir / "artifact-binding.json").read_text())
assert source["status"] == "passed", source
assert artifact["status"] == "not_run", artifact
assert artifact["exitCode"] == 3, artifact
assert "unavailable" in artifact["reason"], artifact
candidate_digest = "sha256:" + hashlib.sha256(candidate_path.read_bytes()).hexdigest()
unavailable_declaration = json.dumps({
    "availability": "unavailable",
    "candidateLockDigest": candidate_digest,
    "gateId": "artifact-binding",
    "kind": "artifact",
}, sort_keys=True, separators=(",", ":")).encode()
assert artifact["subject"]["digest"] == "sha256:" + hashlib.sha256(
    unavailable_declaration
).hexdigest(), artifact
assert (evidence_dir / "source-prerequisite.log").is_file()
assert (evidence_dir / "artifact-binding.log").is_file()
PY

python3 - "$fixture_dir/artifact-zero.json" <<'PY'
import json
import sys

catalog = {"schemaVersion": 1, "gates": [{
    "id": "artifact-zero", "phase": 1, "scope": "local", "required": True,
    "timeoutSeconds": 10, "command": ["python3", "-c", "raise SystemExit(0)"],
    "prerequisites": [], "subjectKind": "artifact",
}]}
with open(sys.argv[1], "w", encoding="utf-8") as output_file:
    json.dump(catalog, output_file, sort_keys=True)
PY
set +e
python3 "$source_runner" \
  --catalog "$fixture_dir/artifact-zero.json" --scope local \
  --candidate-lock "$fixture_dir/matching-candidate.json" \
  --evidence-dir "$fixture_dir/artifact-zero-evidence"
status=$?
set -e
[[ $status -ne 0 ]] || { echo "zero-exit artifact gate returned success" >&2; exit 1; }
python3 - "$fixture_dir/artifact-zero-evidence/artifact-zero.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as input_file:
    envelope = json.load(input_file)
assert envelope["status"] == "failed", envelope
assert envelope["exitCode"] == 1, envelope
assert "unavailable" in envelope["reason"], envelope
PY

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

required_current_local = {
    "backend-headless-build", "api-contract-drift",
    "operator-cli-linux-amd64", "operator-cli-linux-arm64",
    "operator-cli-windows-amd64", "operator-cli-windows-arm64",
    "operator-cli-darwin-amd64", "operator-cli-darwin-arm64",
    "frontend-playwright", "documentation-citation-hygiene",
    "documentation-placeholder-hygiene",
}
missing = sorted(required_current_local - set(ids))
assert not missing, f"repository catalog is missing current local gates: {missing}"

gate_by_id = {gate["id"]: gate for gate in catalog["gates"]}
assert gate_by_id["backend-headless-build"]["command"][-5:] == [
    "go", "build", "-o", "/tmp/telos-core-headless", ".",
]
assert gate_by_id["api-contract-drift"]["command"][-4:] == [
    "go", "run", "./cmd/genopenapi", "--check",
]
assert gate_by_id["frontend-playwright"]["command"] == [
    "python3", "scripts/run-playwright-gate.py",
]
for platform in ("linux", "windows", "darwin"):
    for architecture in ("amd64", "arm64"):
        command = gate_by_id[f"operator-cli-{platform}-{architecture}"]["command"]
        assert f"GOOS={platform}" in command, command
        assert f"GOARCH={architecture}" in command, command
        assert command[-5:] == [
            "go", "build", "-o", "/tmp/telos-operator", "./cmd/telos",
        ], command

beta_index = ids.index("beta-certification")
required_before_beta = {
    gate["id"] for gate in catalog["gates"][:beta_index] if gate["required"]
}
assert set(gate_by_id["beta-certification"]["prerequisites"]) == required_before_beta
PY

python3 "$repo_root/scripts/check-documentation-hygiene.py" citations
python3 "$repo_root/scripts/check-documentation-hygiene.py" placeholders

python3 "$repo_root/scripts/run-playwright-gate.py" --help >/dev/null
for arguments in "--unknown" "--ready-timeout-seconds 0" "--shutdown-timeout-seconds 0"; do
  read -r -a argv <<<"$arguments"
  set +e
  python3 "$repo_root/scripts/run-playwright-gate.py" "${argv[@]}" >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 2 ]] || { echo "Playwright wrapper accepted: $arguments" >&2; exit 1; }
done

python3 - "$repo_root/scripts/run-playwright-gate.py" <<'PY'
import ast
import pathlib
import sys

tree = ast.parse(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
calls = [node for node in ast.walk(tree) if isinstance(node, ast.Call)]

def command_call(attribute, argv):
    for call in calls:
        function = call.func
        if not (
            isinstance(function, ast.Attribute)
            and isinstance(function.value, ast.Name)
            and function.value.id == "subprocess"
            and function.attr == attribute
        ):
            continue
        if not call.args or not isinstance(call.args[0], ast.List):
            continue
        values = [element.value for element in call.args[0].elts]
        if values == argv:
            return {keyword.arg: keyword.value for keyword in call.keywords}
    raise AssertionError((attribute, argv))

server = command_call("Popen", ["npm", "run", "dev"])
assert isinstance(server["cwd"], ast.Name) and server["cwd"].id == "FRONTEND_ROOT"
assert isinstance(server["shell"], ast.Constant) and server["shell"].value is False
assert isinstance(server["start_new_session"], ast.Constant)
assert server["start_new_session"].value is True
playwright = command_call("run", ["npx", "playwright", "test"])
assert isinstance(playwright["cwd"], ast.Name) and playwright["cwd"].id == "FRONTEND_ROOT"
assert isinstance(playwright["shell"], ast.Constant) and playwright["shell"].value is False
assert any(
    isinstance(call.func, ast.Attribute)
    and isinstance(call.func.value, ast.Name)
    and call.func.value.id == "os"
    and call.func.attr == "killpg"
    for call in calls
)
PY
