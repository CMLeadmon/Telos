#!/usr/bin/env bash
set -euo pipefail

# The frontend-types gate must run the locked TypeScript compiler against
# frontend/tsconfig.json no matter where the runner launches it. A bare
# `npx --prefix frontend tsc` run from the repository root finds no tsconfig,
# prints compiler help and exits 0 without checking a single file. This test
# loads the catalog's own argv, swaps only the --project value for a temporary
# project, and proves the compiler really reports a type error. It never
# installs packages: a missing compiler is a failed prerequisite.

repo_root="$(git rev-parse --show-toplevel)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT
cd "$repo_root"

command -v node >/dev/null || {
  echo "frontend-types prerequisite missing: node (expected the version in frontend/.nvmrc)" >&2
  exit 1
}
[[ -f frontend/node_modules/typescript/bin/tsc ]] || {
  echo "frontend-types prerequisite missing: frontend/node_modules/typescript (run npm ci in frontend/)" >&2
  exit 1
}

python3 - "$repo_root/ci/phase-gates.json" "$fixture_dir/argv.json" <<'PY'
import json
import sys

catalog_path, output_path = sys.argv[1:]
with open(catalog_path, encoding="utf-8") as catalog_file:
    catalog = json.load(catalog_file)
matches = [gate for gate in catalog["gates"] if gate["id"] == "frontend-types"]
assert len(matches) == 1, "catalog must define frontend-types exactly once"
gate = matches[0]
command = gate["command"]
assert command == [
    "node", "frontend/node_modules/typescript/bin/tsc",
    "--project", "frontend/tsconfig.json", "--noEmit",
], f"frontend-types must run the locked compiler against frontend/tsconfig.json: {command}"
assert gate["subjectKind"] == "source", gate
with open(output_path, "w", encoding="utf-8") as output_file:
    json.dump(command, output_file)
PY

write_project() {
  local name="$1"
  local source_text="$2"
  local project_dir="$fixture_dir/$name"
  mkdir -p "$project_dir"
  printf '%s\n' "$source_text" >"$project_dir/subject.ts"
  cat >"$project_dir/tsconfig.json" <<JSON
{
  "compilerOptions": {"types": [], "skipLibCheck": true, "noEmit": true, "strict": true},
  "files": ["subject.ts"]
}
JSON
  printf '%s\n' "$project_dir/tsconfig.json"
}

run_catalog_compiler() {
  local project="$1"
  python3 - "$fixture_dir/argv.json" "$project" <<'PY' | xargs -0 -- env
import json
import sys

command = json.load(open(sys.argv[1], encoding="utf-8"))
project = sys.argv[2]
index = command.index("--project")
command = command[:index + 1] + [project] + command[index + 2:]
sys.stdout.write("\0".join(command) + "\0")
PY
}

valid_project="$(write_project valid 'export const value: number = 1;')"
set +e
valid_output="$(run_catalog_compiler "$valid_project" 2>&1)"
valid_status=$?
set -e
[[ $valid_status -eq 0 ]] || {
  echo "catalog compiler rejected a valid project (exit $valid_status):" >&2
  echo "$valid_output" >&2
  exit 1
}

invalid_project="$(write_project invalid 'export const value: number = "wrong";')"
set +e
invalid_output="$(run_catalog_compiler "$invalid_project" 2>&1)"
invalid_status=$?
set -e
[[ $invalid_status -ne 0 ]] || {
  echo "catalog compiler accepted a type error; the gate would not catch regressions" >&2
  echo "$invalid_output" >&2
  exit 1
}
grep -Fq 'error TS2322' <<<"$invalid_output" || {
  echo "catalog compiler failed without reporting the type error:" >&2
  echo "$invalid_output" >&2
  exit 1
}

echo "frontend-types gate: catalog compiler accepts valid and rejects invalid TypeScript"
