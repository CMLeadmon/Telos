#!/usr/bin/env bash
# restore-state.sh -- inspect and repair the active-generation pointer and the operation journal.
#
#   restore-state.sh current              the active generation id (empty when none)
#   restore-state.sh journal              the last upgrade/rollback journal as JSON
#   restore-state.sh recover SCHEMA       what an interrupted operation must do next (read-only)
#
# This is a thin, read-only view over scripts/lib/generations.py; it does not move the pointer. Changing
# the active generation is upgrade.sh, rollback.sh or restore.sh --mode rollback, each under the host-wide
# maintenance lock and each journaling every step first. (The earlier pointer-file/journal pair that lived here
# was replaced by the generation layout /srv/telos/{generations,current} and <node>/state/operation.json.)
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
release_base="${TELOS_RELEASE_BASE:-/srv/telos}"
node_path="${TELOS_NODE_PATH:-/var/lib/telos}"
generations() { python3 -I "$script_dir/lib/generations.py" "$@"; }

case "${1:-}" in
  current) generations current --root "$release_base" | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["current"] or "")' ;;
  journal) generations journal show --node "$node_path" ;;
  recover)
    [[ "${2:-}" =~ ^[0-9]+$ ]] || { echo "usage: restore-state.sh recover SCHEMA_VERSION" >&2; exit 2; }
    generations plan-recovery --root "$release_base" --node "$node_path" --schema "$2" ;;
  *) echo "usage: restore-state.sh {current|journal|recover SCHEMA_VERSION}" >&2; exit 2 ;;
esac
