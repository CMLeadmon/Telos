#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
tmp="$(mktemp -d)"
producer=""
cleanup() {
  if [[ -n "$producer" ]]; then
    kill "$producer" 2>/dev/null || true
    wait "$producer" 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

printf '{"schemaVersion":1,"sourceCommit":"%040d"}\n' 0 >"$tmp/candidate.json"

scripts=(
  certify-beta.sh verify-runtime-hardening.sh run-capacity-drill.sh
  audit-host-exposure.sh certify-browser-matrix.sh build-release.sh
  build-predecessor.sh stage-release.sh sign-evidence.sh verify-release-pins.sh
  verify-restored-node.sh verify-monitoring.sh upgrade.sh rollback.sh
  provision-jellyfin.sh provision-grimmory.sh ci/run-local.sh
  ci/validate-sbom.sh drills/clean-node-install.sh drills/recovery.sh
  drills/upgrade-rollback.sh
)

assert_not_run() {
  local script="$1"
  local output status evidence_path
  evidence_path="$tmp/${script//\//-}.json"

  set +e
  output="$(bash "$repo_root/scripts/$script" 2>&1)"
  status=$?
  set -e
  [[ $status -eq 3 ]] || { echo "$script without arguments returned $status" >&2; exit 1; }
  ! grep -Eqi 'certified successfully|complete\.|passed\.' <<<"$output" || {
    echo "$script claimed success without running" >&2
    exit 1
  }

  set +e
  output="$(bash "$repo_root/scripts/$script" --candidate-lock "$tmp/candidate.json" \
    --evidence-out "$evidence_path" 2>&1)"
  status=$?
  set -e
  [[ $status -eq 3 ]] || { echo "$script with evidence returned $status" >&2; exit 1; }
  ! grep -Eqi 'certified successfully|complete\.|passed\.' <<<"$output" || {
    echo "$script claimed success while writing evidence" >&2
    exit 1
  }
  python3 "$repo_root/scripts/lib/evidence.py" verify \
    --schema "$repo_root/release/evidence-envelope.schema.json" \
    --candidate-lock "$tmp/candidate.json" "$evidence_path"
  [[ "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "$evidence_path")" == not_run ]] || {
    echo "$script did not write not_run evidence" >&2
    exit 1
  }
  [[ "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["command"][0])' "$evidence_path")" != -- ]] || {
    echo "$script recorded the argv separator instead of the original command" >&2
    exit 1
  }
  python3 - "$evidence_path" "$repo_root/scripts/$script" "$tmp/candidate.json" "$evidence_path" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as evidence_file:
    recorded = json.load(evidence_file)["command"]
expected = [sys.argv[2], "--candidate-lock", sys.argv[3], "--evidence-out", sys.argv[4]]
if recorded != expected:
    raise SystemExit(f"recorded command differs from the wrapper invocation: {recorded!r}")
PY
  [[ "$(stat -c '%a' "$evidence_path")" == 600 ]] || {
    echo "$script did not write mode-0600 evidence" >&2
    exit 1
  }

  set +e
  (
    cd "$tmp"
    bash "$repo_root/scripts/$script" --candidate-lock candidate.json \
      --evidence-out "ordinary-${script//\//-}.json"
  ) >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 3 ]] || { echo "$script rejected ordinary relative paths" >&2; exit 1; }
  [[ -e "$tmp/ordinary-${script//\//-}.json" ]] || {
    echo "$script did not write evidence to an ordinary relative path" >&2
    exit 1
  }

  set +e
  bash "$repo_root/scripts/$script" --unknown --evidence-out "$tmp/unknown-${script//\//-}.json" >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 2 ]] || { echo "$script accepted an unknown argument" >&2; exit 1; }
  [[ ! -e "$tmp/unknown-${script//\//-}.json" ]] || {
    echo "$script wrote evidence for an unknown argument" >&2
    exit 1
  }

  set +e
  bash "$repo_root/scripts/$script" --candidate-lock --unknown \
    --evidence-out "$tmp/option-value-${script//\//-}.json" >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 2 ]] || { echo "$script accepted an option token as a candidate-lock path" >&2; exit 1; }
  [[ ! -e "$tmp/option-value-${script//\//-}.json" ]] || {
    echo "$script wrote evidence after an option token was used as a candidate-lock path" >&2
    exit 1
  }

  set +e
  (
    cd "$tmp"
    bash "$repo_root/scripts/$script" --candidate-lock "$tmp/candidate.json" --evidence-out --unknown
  ) >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 2 ]] || { echo "$script accepted an option token as an evidence path" >&2; exit 1; }
  [[ ! -e "$tmp/--unknown" ]] || {
    echo "$script wrote evidence to an option token path" >&2
    exit 1
  }

  set +e
  bash "$repo_root/scripts/$script" --evidence-out "$tmp/missing-candidate-${script//\//-}.json" >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 2 ]] || { echo "$script accepted evidence without a candidate lock" >&2; exit 1; }
  [[ ! -e "$tmp/missing-candidate-${script//\//-}.json" ]] || {
    echo "$script wrote evidence without a candidate lock" >&2
    exit 1
  }

  set +e
  bash "$repo_root/scripts/$script" --candidate-lock >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 2 ]] || { echo "$script accepted an incomplete argument" >&2; exit 1; }
}

for script in "${scripts[@]}"; do
  assert_not_run "$script"
done

for option in --compose --env-file; do
  set +e
  bash "$repo_root/scripts/verify-runtime-hardening.sh" "$option" --unknown >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 2 ]] || { echo "verify-runtime-hardening.sh accepted an option token as a $option path" >&2; exit 1; }

  set +e
  bash "$repo_root/scripts/verify-runtime-hardening.sh" "$option" "$tmp/ordinary-path" >/dev/null 2>&1
  status=$?
  set -e
  [[ $status -eq 3 ]] || { echo "verify-runtime-hardening.sh rejected an ordinary $option path" >&2; exit 1; }
done

python3 - "$repo_root/scripts/lib/evidence.py" <<'PY'
import hashlib
import importlib.util
import sys

spec = importlib.util.spec_from_file_location("evidence", sys.argv[1])
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)
payload = b'{"schemaVersion":1,"sourceCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}\n'
expected = "sha256:" + hashlib.sha256(payload).hexdigest()
if evidence.digest_bytes(payload) != expected:
    raise SystemExit("evidence digest_bytes did not preserve the candidate byte digest")
PY

snapshot_candidate="$tmp/candidate.fifo"
replacement_candidate="$tmp/replacement.fifo"
snapshot_evidence="$tmp/snapshot-evidence.json"
snapshot_commit="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
replacement_commit="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
snapshot_payload="{\"schemaVersion\":1,\"sourceCommit\":\"$snapshot_commit\"}"$'\n'
replacement_payload="{\"schemaVersion\":1,\"sourceCommit\":\"$replacement_commit\"}"$'\n'
mkfifo "$snapshot_candidate"
mkfifo "$replacement_candidate"
(
  printf '%s' "$snapshot_payload" >"$snapshot_candidate"
  mv -- "$replacement_candidate" "$snapshot_candidate"
  printf '%s' "$replacement_payload" >"$snapshot_candidate"
) >/dev/null 2>&1 &
producer=$!

set +e
bash "$repo_root/scripts/certify-beta.sh" --candidate-lock "$snapshot_candidate" \
  --evidence-out "$snapshot_evidence" >/dev/null 2>&1
status=$?
set -e
[[ $status -eq 3 ]] || { echo "snapshot candidate invocation returned $status" >&2; exit 1; }
python3 - "$snapshot_evidence" "$snapshot_payload" "$snapshot_commit" <<'PY'
import hashlib
import json
import sys

with open(sys.argv[1], encoding="utf-8") as evidence_file:
    envelope = json.load(evidence_file)
expected_digest = "sha256:" + hashlib.sha256(sys.argv[2].encode()).hexdigest()
if envelope["sourceCommit"] != sys.argv[3]:
    raise SystemExit("evidence sourceCommit was not read from the candidate snapshot")
if envelope["candidateLockDigest"] != expected_digest:
    raise SystemExit("evidence candidate digest was not computed from the candidate snapshot")
if envelope["subject"]["digest"] != expected_digest:
    raise SystemExit("evidence subject digest was not computed from the candidate snapshot")
PY
