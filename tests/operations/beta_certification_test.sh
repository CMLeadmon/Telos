#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

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
  [[ "$(stat -c '%a' "$evidence_path")" == 600 ]] || {
    echo "$script did not write mode-0600 evidence" >&2
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
