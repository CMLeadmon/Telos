#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT

printf '{"schemaVersion":1,"sourceCommit":"%040d"}\n' 0 >"$fixture_dir/candidate.json"
candidate_digest="sha256:$(sha256sum "$fixture_dir/candidate.json" | awk '{print $1}')"

python3 - "$repo_root/scripts/lib/evidence.py" <<'PY'
import builtins
import hashlib
import importlib.util
import sys

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
  --source-commit "$(printf '%040d' 0)" --candidate-lock-digest "$candidate_digest" \
  --started-at 2026-08-20T10:00:00Z --finished-at 2026-08-20T10:00:01Z \
  --exit-code 0 --output-digest "sha256:$(printf passed | sha256sum | awk '{print $1}')" \
  --command-json '["printf","passed"]' \
  --tool-versions-json '{"python":"3"}' \
  --subject-json '{"kind":"fixture","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}'

[[ "$(stat -c '%a' "$fixture_dir/passed.json")" == "600" ]] || {
  echo "evidence writer did not create a mode-0600 envelope" >&2
  exit 1
}

python3 "$repo_root/scripts/lib/evidence.py" verify \
  --schema "$repo_root/release/evidence-envelope.schema.json" \
  --candidate-lock "$fixture_dir/candidate.json" "$fixture_dir/passed.json"

for mutation in missing-gate unknown-status mismatched-candidate string-command negative-exit reversed-time bad-digest secret-field; do
  python3 "$repo_root/tests/helpers/mutate_evidence.py" \
    "$mutation" "$fixture_dir/passed.json" "$fixture_dir/$mutation.json"
  if python3 "$repo_root/scripts/lib/evidence.py" verify \
      --schema "$repo_root/release/evidence-envelope.schema.json" \
      --candidate-lock "$fixture_dir/candidate.json" "$fixture_dir/$mutation.json"; then
    echo "accepted invalid envelope: $mutation" >&2
    exit 1
  fi
done
