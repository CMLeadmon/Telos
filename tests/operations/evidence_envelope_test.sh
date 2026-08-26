#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT

printf '{"schemaVersion":1,"sourceCommit":"%040d"}\n' 0 >"$fixture_dir/candidate.json"
candidate_digest="sha256:$(sha256sum "$fixture_dir/candidate.json" | awk '{print $1}')"
source_commit="$(printf '%040d' 0)"
source_digest="sha256:$(printf '%s' "$source_commit" | sha256sum | awk '{print $1}')"

python3 - "$repo_root/scripts/lib/evidence.py" <<'PY'
import builtins
import hashlib
import importlib.util
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("evidence", sys.argv[1])
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)

payload = (b"telos-evidence-streaming-" * 100_000) + b"done"
chunk_size = 1024 * 1024

class BoundedReader:
    def __init__(self, value):
        self.value = value
        self.offset = 0

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return False

    def read(self, size=-1):
        if size < 0 or size > chunk_size:
            raise AssertionError("digest_path requested an unbounded read")
        chunk = self.value[self.offset:self.offset + size]
        self.offset += len(chunk)
        return chunk

reader = BoundedReader(payload)
original_open = builtins.open
builtins.open = lambda path, mode: reader
try:
    actual = evidence.digest_path("candidate-lock")
finally:
    builtins.open = original_open
expected = "sha256:" + hashlib.sha256(payload).hexdigest()
if actual != expected:
    raise SystemExit("digest_path did not hash every streamed byte")
PY

python3 "$repo_root/scripts/lib/evidence.py" write \
  --output "$fixture_dir/passed.json" \
  --gate-id phase-1-self-test --status passed --reason "fixture assertions passed" \
  --source-commit "$source_commit" --candidate-lock-digest "$candidate_digest" \
  --started-at 2026-08-20T10:00:00Z --finished-at 2026-08-20T10:00:01Z \
  --exit-code 0 --output-digest "sha256:$(printf passed | sha256sum | awk '{print $1}')" \
  --command-json '["printf","passed"]' \
  --tool-versions-json '{"python":"3"}' \
  --subject-json "{\"kind\":\"source\",\"digest\":\"$source_digest\"}"

[[ "$(stat -c '%a' "$fixture_dir/passed.json")" == "600" ]] || {
  echo "evidence writer did not create a mode-0600 envelope" >&2
  exit 1
}
cp "$fixture_dir/passed.json" "$fixture_dir/original-passed.json"
set +e
python3 "$repo_root/scripts/lib/evidence.py" write \
  --output "$fixture_dir/passed.json" \
  --gate-id phase-1-self-test --status passed --reason "replacement must be rejected" \
  --source-commit "$source_commit" --candidate-lock-digest "$candidate_digest" \
  --started-at 2026-08-20T10:00:00Z --finished-at 2026-08-20T10:00:02Z \
  --exit-code 0 --output-digest "sha256:$(printf replacement | sha256sum | awk '{print $1}')" \
  --command-json '["printf","replacement"]' \
  --tool-versions-json '{"python":"3"}' \
  --subject-json "{\"kind\":\"source\",\"digest\":\"$source_digest\"}" \
  >"$fixture_dir/no-clobber.stdout" 2>"$fixture_dir/no-clobber.stderr"
no_clobber_status=$?
set -e
[[ $no_clobber_status -ne 0 ]] || {
  echo "evidence writer replaced an existing envelope" >&2
  exit 1
}
cmp "$fixture_dir/original-passed.json" "$fixture_dir/passed.json" || {
  echo "evidence writer changed the first published bytes" >&2
  exit 1
}
if find "$fixture_dir" -maxdepth 1 -name '.evidence-*.tmp' -print -quit | grep -q .; then
  echo "evidence writer left temporary state after publication" >&2
  exit 1
fi

python3 "$repo_root/scripts/lib/evidence.py" verify \
  --schema "$repo_root/release/evidence-envelope.schema.json" \
  --candidate-lock "$fixture_dir/candidate.json" "$fixture_dir/passed.json"

python3 - "$repo_root/scripts/lib/evidence.py" "$repo_root/release/evidence-envelope.schema.json" \
  "$fixture_dir/candidate.json" "$fixture_dir/passed.json" <<'PY'
import argparse
import builtins
import importlib.util
import os
import sys

sys.dont_write_bytecode = True
module_path, schema_path, candidate_path, envelope_path = sys.argv[1:]
spec = importlib.util.spec_from_file_location("evidence_snapshot_test", module_path)
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
original_open = builtins.open
candidate_reads = 0


def counting_open(path, *args, **kwargs):
    global candidate_reads
    if os.fspath(path) == candidate_path:
        candidate_reads += 1
    return original_open(path, *args, **kwargs)


builtins.open = counting_open
try:
    evidence.verify_envelopes(argparse.Namespace(
        schema=schema_path,
        candidate_lock=candidate_path,
        envelopes=[envelope_path],
    ))
finally:
    builtins.open = original_open
if candidate_reads != 1:
    raise SystemExit(f"candidate lock was opened {candidate_reads} times instead of once")
PY

public_evidence="$fixture_dir/public-evidence"
mkdir "$public_evidence"
cp "$fixture_dir/passed.json" "$public_evidence/passed.json"
bash "$repo_root/scripts/verify-evidence.sh" \
  --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$public_evidence"

expect_wrapper_status() {
  local expected="$1"
  shift
  local actual=0
  bash "$repo_root/scripts/verify-evidence.sh" "$@" >/dev/null 2>&1 || actual=$?
  [[ $actual -eq $expected ]] || {
    echo "verify-evidence.sh returned $actual instead of $expected for: $*" >&2
    exit 1
  }
}

expect_wrapper_status 2 --unknown value
expect_wrapper_status 2 \
  --candidate-lock "$fixture_dir/candidate.json" \
  --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$public_evidence"
expect_wrapper_status 2 --candidate-lock
expect_wrapper_status 2 --candidate-lock --evidence-dir --evidence-dir "$public_evidence"
expect_wrapper_status 2 \
  --candidate-lock "$fixture_dir/../$(basename "$fixture_dir")/candidate.json" \
  --evidence-dir "$public_evidence"
expect_wrapper_status 2 \
  --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$fixture_dir/../$(basename "$fixture_dir")/public-evidence"

ln -s "$fixture_dir/candidate.json" "$fixture_dir/candidate-link.json"
expect_wrapper_status 1 \
  --candidate-lock "$fixture_dir/candidate-link.json" \
  --evidence-dir "$public_evidence"
ln -s "$public_evidence" "$fixture_dir/evidence-link"
expect_wrapper_status 1 \
  --candidate-lock "$fixture_dir/candidate.json" \
  --evidence-dir "$fixture_dir/evidence-link"

for mutation in empty-tool-versions non-string-tool-version empty-tool-version unknown-tool-version mismatched-source-commit mismatched-source-subject missing-gate unknown-status mismatched-candidate string-command negative-exit reversed-time bad-digest secret-field; do
  python3 "$repo_root/tests/helpers/mutate_evidence.py" \
    "$mutation" "$fixture_dir/passed.json" "$fixture_dir/$mutation.json"
  if python3 "$repo_root/scripts/lib/evidence.py" verify \
      --schema "$repo_root/release/evidence-envelope.schema.json" \
      --candidate-lock "$fixture_dir/candidate.json" "$fixture_dir/$mutation.json"; then
    echo "accepted invalid envelope: $mutation" >&2
    exit 1
  fi
done
