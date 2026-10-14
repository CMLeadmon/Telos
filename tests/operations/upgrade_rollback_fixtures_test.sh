#!/usr/bin/env bash
set -euo pipefail

# The upgrade/rollback fixture suite: the release-graph records the journal and receipts use, the
# generation/journal/recovery primitives, the predecessor policy, and the orchestrators against stub
# generations -- success, every refusal, failures before and after the first migration, a SIGKILL at
# each step, and compatible/incompatible rollback. Hermetic; the live cycle on a node is the
# `upgrade-rollback` gate.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
python3 -I tests/operations/release_graph_test.py
python3 -I tests/operations/generations_test.py
python3 -I tests/operations/predecessor_test.py
bash tests/operations/upgrade_rollback_test.sh
echo "upgrade/rollback fixtures: records, generations, journal, recovery planning, predecessor policy and orchestration verified"
