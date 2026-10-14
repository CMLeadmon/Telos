#!/usr/bin/env bash
set -euo pipefail

# Release graph, trust-anchor and (later phases) builder/staging contracts.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

echo "Running release test suite..."
if [[ -e release/telos-release.pub ]]; then
  echo "release/telos-release.pub is invalid trust data and must not exist" >&2
  exit 1
fi
for schema in artifact-index release-manifest candidate-lock generation-manifest installed-generation \
  artifact-identity gate-result observed-subject guided-smoke evidence-index certification release-review; do
  [[ -f "release/$schema.schema.json" ]] || { echo "release/$schema.schema.json missing" >&2; exit 1; }
done
python3 -B tests/operations/release_graph_test.py
bash tests/operations/signer_trust_test.sh
python3 -B tests/operations/gate_runner_v2_test.py
python3 -B tests/operations/frozen_artifact_test.py
python3 -B tests/operations/release_compose_test.py
python3 -B tests/operations/build_release_test.py
python3 -B tests/operations/candidate_install_test.py
python3 -B tests/operations/candidate_workflow_contract_test.py
python3 -B tests/operations/evidence_bundle_test.py
python3 -B tests/operations/stage_release_test.py
bash tests/operations/release_cli_test.sh
echo "Release test suite passed."
