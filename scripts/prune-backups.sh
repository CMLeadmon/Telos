#!/usr/bin/env bash
# prune-backups.sh -- apply 14 daily / 8 weekly retention to the off-node restic repository.
# Holds the host-wide maintenance lock (a concurrent backup or restore exits 75 first).
# Snapshots are grouped by host only: every backup stages into a differently named
# directory, so grouping by path would put each snapshot in a group of its own and
# nothing would ever be pruned. Never prints a secret.
set -euo pipefail
umask 077

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/maintenance.sh
. "$script_dir/lib/maintenance.sh"
export TELOS_MAINTENANCE_DIR="${TELOS_NODE_PATH:-/var/lib/telos}/state"

: "${RESTIC_REPOSITORY:?RESTIC_REPOSITORY must name an off-node repository}"
: "${RESTIC_PASSWORD_FILE:?RESTIC_PASSWORD_FILE (mode 0600) must be set}"
export RESTIC_CACHE_DIR="${RESTIC_CACHE_DIR:-/var/cache/telos-restic}"

apply_retention() {
  restic forget --tag telos --group-by host --keep-daily 14 --keep-weekly 8 --prune 9>&-
  echo "prune-backups: retention applied (14 daily / 8 weekly)"
}

with_maintenance_lock apply_retention
