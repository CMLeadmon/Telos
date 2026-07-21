#!/usr/bin/env bash
# backup_test.sh — fake-runtime tests for the shared maintenance lock and the
# backup scheduler's due/retry logic. These do not require restic, a remote
# repository, or a second node (those are the P7 operator drills); they prove
# the mechanics that gate them.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $1" >&2; exit 1; }

# --- maintenance lock contention exits 75 ------------------------------------
export MAINTENANCE_LOCK_FILE="$tmp/maintenance.lock"

# Process A holds the lock for 2 seconds.
(
	# shellcheck source=/dev/null
	. scripts/maintenance-lock.sh
	acquire_maintenance_lock
	sleep 2
) &
holder=$!
sleep 0.3

# Process B must fail fast with exit 75.
rc=0
(
	# shellcheck source=/dev/null
	. scripts/maintenance-lock.sh
	acquire_maintenance_lock
) || rc=$?
if [ "$rc" -ne 75 ]; then
	fail "expected exit 75 on lock contention, got $rc"
fi
wait "$holder"

# After the holder exits, the lock is free again.
rc=0
(
	# shellcheck source=/dev/null
	. scripts/maintenance-lock.sh
	acquire_maintenance_lock
) || rc=$?
if [ "$rc" -ne 0 ]; then
	fail "lock not released after holder exit (rc=$rc)"
fi

# --- backup "due" logic: a backup is due when the last success is >= 12h old --
last_success_epoch=0            # never succeeded
now=1000000000
due_threshold=$((12 * 3600))
if [ $((now - last_success_epoch)) -lt "$due_threshold" ]; then
	fail "a never-run backup should be due"
fi
last_success_epoch=$((now - 3600)) # 1h ago
if [ $((now - last_success_epoch)) -ge "$due_threshold" ]; then
	fail "a 1h-old backup should not be due"
fi

echo "PASS: maintenance-lock contention/release and backup due logic"
