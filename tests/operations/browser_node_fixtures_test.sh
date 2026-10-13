#!/usr/bin/env bash
set -euo pipefail

# The browser-node fixture suite: the HTTPS journey probe, the reference-host outage
# controller, the clean-install drill's judgement and the static client's routing/caching
# contract. All run against ephemeral fixtures; none contributes release evidence. The static-config part exits
# 3 (not_run) when no nginx is installed, and that status is preserved.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
bash tests/operations/browser_node_smoke_test.sh
bash tests/operations/browser_node_rehearsal_test.sh
python3 -I tests/operations/clean_install_drill_test.py
status=0
bash tests/operations/static_client_config_test.sh || status=$?
if [[ $status -ne 0 ]]; then
  echo "browser-node fixtures: the nginx contract did not pass (exit $status)" >&2
  exit "$status"
fi
echo "browser-node fixtures: probe, controller, install drill and static client contract verified"
