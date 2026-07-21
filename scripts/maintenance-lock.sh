#!/usr/bin/env bash
# maintenance-lock.sh — the single host-wide maintenance lock shared by backup,
# prune, restore, install, upgrade, rollback, and drill scripts.
#
# Source this file, then call `acquire_maintenance_lock`. It takes an exclusive,
# non-blocking flock(2) on /run/telos/maintenance.lock and holds it for the
# lifetime of the calling shell. Contention exits 75 (maintenance_in_progress).
# No script may use a private lock name or proceed unlocked.

MAINTENANCE_LOCK_FILE="${MAINTENANCE_LOCK_FILE:-/run/telos/maintenance.lock}"
MAINTENANCE_EXIT_BUSY=75

acquire_maintenance_lock() {
	mkdir -p "$(dirname "$MAINTENANCE_LOCK_FILE")" 2>/dev/null || true
	# fd 9 stays open for the process lifetime, holding the lock.
	exec 9>"$MAINTENANCE_LOCK_FILE" || {
		echo "maintenance-lock: cannot open $MAINTENANCE_LOCK_FILE" >&2
		exit 1
	}
	if ! flock -n 9; then
		echo "maintenance-lock: another maintenance operation is in progress" >&2
		exit "$MAINTENANCE_EXIT_BUSY"
	fi
}
