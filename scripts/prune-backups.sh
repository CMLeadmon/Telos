#!/usr/bin/env bash
# prune-backups.sh — enforce 14 daily / 8 weekly retention on the restic
# repository. Holds the shared maintenance lock. Uses a non-repository-local
# cache. Never prints secret values.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
# shellcheck source=scripts/maintenance-lock.sh
. scripts/maintenance-lock.sh

: "${RESTIC_REPOSITORY:?RESTIC_REPOSITORY must be set to a non-local backend}"
: "${RESTIC_PASSWORD_FILE:?RESTIC_PASSWORD_FILE (mode 0600) must be set}"
export RESTIC_CACHE_DIR="${RESTIC_CACHE_DIR:-/var/cache/telos-restic}"

acquire_maintenance_lock

# One snapshot may satisfy both a daily and a weekly bucket; grouping by
# host+paths keeps retention per logical target.
restic forget \
	--tag scheduled \
	--group-by host,paths \
	--keep-daily 14 \
	--keep-weekly 8 \
	--prune

echo "prune-backups: retention applied (14 daily / 8 weekly)"
