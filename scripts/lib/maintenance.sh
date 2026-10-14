#!/usr/bin/env bash
# maintenance.sh — the one host-wide maintenance lock, shared by backup, prune, restore,
# install, upgrade and rollback.
#
#   export TELOS_MAINTENANCE_DIR=<directory that holds maintenance.lock>
#   with_maintenance_lock COMMAND [ARGS...]
#
# The outermost call opens the lock file on descriptor 9 and takes a non-blocking
# flock(2); contention exits 75 (maintenance_in_progress) before anything is touched.
# A command that is already running under the lock may call with_maintenance_lock again
# (an upgrade running a backup, say): the nested call does not re-acquire, it *verifies*
# that descriptor 9 really is this lock file and that the lock really is held, and runs the
# command. There is no environment flag that skips locking: the variables the outer call
# exports are only claims, and the nested call refuses a claim the kernel does not back --
# a foreign descriptor, a descriptor for another file, or a lock nobody holds.

MAINTENANCE_EXIT_BUSY=75
MAINTENANCE_LOCK_FD=9

maintenance_lock_path() { printf '%s/maintenance.lock' "${TELOS_MAINTENANCE_DIR:?TELOS_MAINTENANCE_DIR must name the lock directory}"; }

# Does descriptor 9 of THIS process refer to the lock file, and is the lock held by an
# open file description other than a throwaway one? A second open + non-blocking flock
# succeeds only when nobody holds the lock.
maintenance_lock_verified() {
  local path ident
  path="$(maintenance_lock_path)"
  [ -e "/proc/self/fd/$MAINTENANCE_LOCK_FD" ] || return 1
  ident="$(stat -Lc '%d:%i' "/proc/self/fd/$MAINTENANCE_LOCK_FD" 2>/dev/null)" || return 1
  [ "$ident" = "$(stat -Lc '%d:%i' "$path" 2>/dev/null)" ] || return 1
  if ( exec 8<"$path"; flock -n 8 ); then
    return 1   # we could take it, so no one was holding it
  fi
  return 0
}

with_maintenance_lock() {
  local path; path="$(maintenance_lock_path)"
  if [ -n "${TELOS_MAINTENANCE_LOCK_CLAIM:-}" ]; then
    if ! maintenance_lock_verified; then
      echo "maintenance: refusing a lock claim that is not backed by a held lock on $path" >&2
      return 1
    fi
    "$@"
    return
  fi
  mkdir -p "$(dirname "$path")"
  exec 9>"$path" || { echo "maintenance: cannot open $path" >&2; return 1; }
  if ! flock -n 9; then
    echo "maintenance: another maintenance operation is in progress" >&2
    exit "$MAINTENANCE_EXIT_BUSY"
  fi
  export TELOS_MAINTENANCE_LOCK_CLAIM=1
  "$@"
}
