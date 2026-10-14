#!/usr/bin/env bash
set -euo pipefail

# The capacity, exposure and controlled-egress fixture suite (the `capacity-exposure-fixtures` gate): the
# audit/drill judgements, the sampler, the fixture preparer's safety rules and the whole capacity drill with an
# injected load generator, the egress node program against a fake podman, the Squid policy, the k6 script's
# syntax, and the static exposure check of the repository's own Compose files. Hermetic; the live drills are the
# `capacity`, `host-exposure` and `controlled-egress` gates. The k6 syntax check exits 3 (not_run) with no Node.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
python3 -I tests/operations/edge_policy_test.py
python3 -I tests/operations/exposure_audit_test.py
python3 -I tests/operations/egress_drill_test.py
python3 -I tests/operations/capacity_drill_test.py
bash scripts/tests/egress_test.sh
bash scripts/audit-host-exposure.sh --source --policy config/exposure-policy.json
python3 -m py_compile scripts/prepare-capacity-fixture.py scripts/collect-runtime-stats.py scripts/lib/capacity_drill.py scripts/lib/egress_drill.py scripts/lib/exposure_audit.py
if ! command -v node >/dev/null 2>&1; then
  echo "capacity/exposure fixtures: not_run: node is required to check tests/load/telos.js" >&2
  exit 3
fi
node --check tests/load/telos.js
echo "capacity/exposure fixtures: judgements, sampler, fixture safety, drill orchestration, egress program, policy and load script verified"
