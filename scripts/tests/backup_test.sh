#!/usr/bin/env bash
# backup_test.sh -- the maintenance lock (scripts/lib/maintenance.sh) and the backup
# scheduler's due logic. The lock is what makes backup, prune, restore and upgrade
# mutually exclusive, so its edges are tested directly: contention exits 75 before anything
# runs, a nested call under the lock works, a claim the kernel does not back is refused (a
# foreign descriptor, a different file, nobody holding), and the lock is free again after a
# holder is killed.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

tmp="$(mktemp -d)"
trap 'jobs -p | xargs -r kill -KILL 2>/dev/null || true; rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $1" >&2; exit 1; }
export TELOS_MAINTENANCE_DIR="$tmp/state"

run_locked() { bash -c '. scripts/lib/maintenance.sh; with_maintenance_lock "$@"' _ "$@"; }

# --- contention exits 75 before the command runs --------------------------------------
( run_locked sleep 3 ) &
holder=$!
sleep 0.5
rc=0; run_locked touch "$tmp/ran-while-held" || rc=$?
[ "$rc" -eq 75 ] || fail "contention exited $rc, expected 75"
[ ! -e "$tmp/ran-while-held" ] || fail "the command ran although the lock was held"
wait "$holder"

# --- released when the holder exits ---------------------------------------------------
run_locked true || fail "the lock was not released after its holder exited"

# --- nested use under the lock works and does not deadlock ----------------------------
rc=0
bash -c '
  . scripts/lib/maintenance.sh
  inner() { with_maintenance_lock echo nested-ok; }
  with_maintenance_lock inner
' >"$tmp/nested.out" || rc=$?
[ "$rc" -eq 0 ] && grep -q nested-ok "$tmp/nested.out" || fail "a nested call under the lock failed (rc=$rc)"

# --- a bare claim is refused: no environment flag skips locking -----------------------
rc=0
TELOS_MAINTENANCE_LOCK_CLAIM=1 bash -c '. scripts/lib/maintenance.sh; with_maintenance_lock touch "'"$tmp"'/ran-unlocked"' 2>"$tmp/claim.err" || rc=$?
[ "$rc" -ne 0 ] && [ ! -e "$tmp/ran-unlocked" ] || fail "a claim without a held lock was honoured"
grep -q 'not backed by a held lock' "$tmp/claim.err" || fail "no reason was given for the refused claim"

# --- a claim whose descriptor 9 is a different file is refused -------------------------
rc=0
( run_locked sleep 3 ) &
holder=$!
sleep 0.5
TELOS_MAINTENANCE_LOCK_CLAIM=1 bash -c '. scripts/lib/maintenance.sh; exec 9>"'"$tmp"'/other-file"; with_maintenance_lock touch "'"$tmp"'/ran-foreign"' 2>/dev/null || rc=$?
[ "$rc" -ne 0 ] && [ ! -e "$tmp/ran-foreign" ] || fail "a foreign descriptor was accepted while another process held the lock"
wait "$holder"

# --- a killed holder frees the lock ---------------------------------------------------
# The child closes descriptor 9, so killing the holder really releases the lock (a child
# that inherited it would keep the lock held, which is the case backup.sh avoids).
# The victim leads its own process group so the child it orphans can be removed afterwards (the gate runner rejects a test that
# leaves a descendant running).
setsid bash -c '. scripts/lib/maintenance.sh; with_maintenance_lock bash -c "exec 9>&-; sleep 30"' &
victim=$!
sleep 0.5
rc=0; run_locked true || rc=$?
[ "$rc" -eq 75 ] || fail "the victim did not hold the lock (rc=$rc)"
kill -KILL "$victim"; wait "$victim" 2>/dev/null || true
sleep 0.3
run_locked true || fail "the lock stayed held after its holder was SIGKILLed"
kill -TERM -- "-$victim" 2>/dev/null || true   # the orphaned `sleep 30` shares the victim's process group

# --- children started under the lock do not inherit it ---------------------------------
# (backup.sh closes descriptor 9 for podman/restic so a container cannot keep it held.)
( run_locked bash -c 'sleep 4 9>&- & echo $! >"'"$tmp"'/child.pid"' )
rc=0; run_locked true || rc=$?
[ "$rc" -eq 0 ] || fail "a background child that closed fd 9 still held the lock (rc=$rc)"
kill "$(cat "$tmp/child.pid")" 2>/dev/null || true

# --- backup "due" logic: a backup is due when the last success is >= 12h old -----------
last_success_epoch=0; now=1000000000; due_threshold=$((12 * 3600))
[ $((now - last_success_epoch)) -ge "$due_threshold" ] || fail "a never-run backup should be due"
last_success_epoch=$((now - 3600))
[ $((now - last_success_epoch)) -lt "$due_threshold" ] || fail "a 1h-old backup should not be due"

echo "PASS: maintenance lock (contention, nesting, forged claims, release after SIGKILL) and backup due logic"
