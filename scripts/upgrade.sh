#!/usr/bin/env bash
# upgrade.sh -- move this node from the active release generation to another, with a verified
# encrypted snapshot first and a durable journal that makes every interruption recoverable.
#
#   upgrade.sh --generation /srv/telos/generations/<hex> --candidate-lock PATH \
#              --current-candidate-lock PATH --result-out PATH --confirm UPGRADE-THIS-NODE
#
# In order: take the host-wide maintenance lock (contention exits 75); recover any operation an
# earlier run left unfinished BEFORE doing anything new; verify the target and the installed
# generation (digests, ownership, receipt), that the target names the installed one as a
# supported predecessor and accepts the database schema as it stands; journal `started`; take
# and verify an encrypted snapshot with the CURRENT generation's backup.sh; stop the writers
# (databases stay up); run the target's migrations; atomically activate; restart the whole stack on
# the target's images; wait for readiness; record the new installed receipt. Each state is written
# to <node>/state/operation.json before it is acted on.
#
# A failure before the first migration returns to the previous generation. After a migration the
# declared compatibility decides: previous binaries accept the schema -> fast rollback; they do not
# -> state `restore-required` (restore the snapshot named in the journal with
# `restore.sh --mode rollback`). Nothing deletes a generation or a journal; the failed generation and
# the journal are kept for investigation.
#
# Exit codes: 0 upgraded, 1 refused/aborted/rolled back/restore required (see the result file), 2 usage,
# 75 another maintenance operation is running.
# Test seams: TELOS_RELEASE_BASE, TELOS_NODE_PATH, TELOS_MACHINE_ID_FILE, TELOS_LIFECYCLE_REQUIRE_ROOT=0,
# TELOS_GENERATION_OWNER_CHECK=0, TELOS_LIFECYCLE_READY_SECONDS.
set -euo pipefail
umask 077

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/maintenance.sh
. "$script_dir/lib/maintenance.sh"

CONFIRMATION="UPGRADE-THIS-NODE"
release_base="${TELOS_RELEASE_BASE:-/srv/telos}"
node_path="${TELOS_NODE_PATH:-/var/lib/telos}"
state_dir="$node_path/state"
machine_id_file="${TELOS_MACHINE_ID_FILE:-/etc/machine-id}"
export TELOS_MAINTENANCE_DIR="$state_dir"

log() { printf 'upgrade: %s\n' "$*" >&2; }
die() { log "$*"; exit "${2:-1}"; }
# shellcheck source=lib/lifecycle.sh
. "$script_dir/lib/lifecycle.sh"

generation="" candidate_lock="" current_lock="" result_out="" confirm=""
usage() {
  echo "usage: $0 --generation PATH --candidate-lock PATH --current-candidate-lock PATH --result-out PATH --confirm $CONFIRMATION" >&2
  exit 2
}
while [ $# -gt 0 ]; do
  case "$1" in
    --generation)             [ $# -ge 2 ] && [ -z "$generation" ] || usage; generation="$2"; shift 2 ;;
    --candidate-lock)         [ $# -ge 2 ] && [ -z "$candidate_lock" ] || usage; candidate_lock="$2"; shift 2 ;;
    --current-candidate-lock) [ $# -ge 2 ] && [ -z "$current_lock" ] || usage; current_lock="$2"; shift 2 ;;
    --result-out)             [ $# -ge 2 ] && [ -z "$result_out" ] || usage; result_out="$2"; shift 2 ;;
    --confirm)                [ $# -ge 2 ] && [ -z "$confirm" ] || usage; confirm="$2"; shift 2 ;;
    *) usage ;;
  esac
done
[ -n "$generation" ] && [ -n "$candidate_lock" ] && [ -n "$current_lock" ] && [ -n "$result_out" ] || usage
if [ "$confirm" != "$CONFIRMATION" ]; then
  echo "an upgrade stops the writers, migrates the database and restarts the stack. Re-run with --confirm $CONFIRMATION to proceed." >&2
  exit 2
fi

upgrade_failed=0
run_upgrade() {
  [ "${TELOS_LIFECYCLE_REQUIRE_ROOT:-1}" = 0 ] || [ "$(id -u)" -eq 0 ] || die "run as root (rootful Podman)"
  [ -f "$candidate_lock" ] && [ -f "$current_lock" ] || die "a candidate lock was not found"
  mkdir -p "$state_dir"

  local to from digest_to digest_from
  digest_to="$(sha256sum "$candidate_lock" | cut -d' ' -f1)"; digest_from="$(sha256sum "$current_lock" | cut -d' ' -f1)"
  to="$digest_to"; from="$digest_from"
  [ "$(realpath -m "$generation")" = "$(generation_dir "$to")" ] || die "--generation must be $(generation_dir "$to") (the directory named by the candidate lock's digest)"
  [ "$to" != "$from" ] || die "the candidate is already the installed generation"

  # An unfinished operation is recovered first; a new upgrade never starts on top of one.
  if ! recover_unfinished; then
    write_result "$RECOVERED_STATE" "an earlier operation needs the operator; see the journal"
    die "an earlier operation ended in state '$RECOVERED_STATE'; nothing new was started"
  fi
  case "$RECOVERED_STATE" in
    complete) write_result complete "the interrupted upgrade was completed"; log "the interrupted upgrade was completed; the node is serving"; return 0 ;;
    aborted|rolled-back) write_result "$RECOVERED_STATE" "the interrupted upgrade was undone; run the upgrade again to retry"; die "the interrupted upgrade was undone ($RECOVERED_STATE); run the upgrade again to retry" ;;
  esac

  local active
  active="$(gen current --root "$release_base" | json_get current)"
  [ "$active" = "$from" ] || die "the active generation (${active:-none}) is not the one named by --current-candidate-lock"
  gen verify --root "$release_base" --id "$to" "${OWNER_FLAG[@]}" >/dev/null || die "the target generation did not verify"
  gen check-receipt --root "$release_base" --node "$node_path" --id "$from" "${OWNER_FLAG[@]}" >/dev/null || die "the installed generation's receipt does not verify"
  local schema host
  schema="$(db_schema "$from")" || die "could not read the database schema from the running node"
  gen check-upgrade --root "$release_base" --from "$from" --to "$to" --schema "$schema" "${OWNER_FLAG[@]}" >/dev/null \
    || die "this upgrade is not allowed from the installed generation at schema $schema"
  host="sha256:$(sha256sum "$machine_id_file" | cut -d' ' -f1)"

  gen journal begin --node "$node_path" --kind upgrade --from "$from" --to "$to" --schema "$schema" --host "$host" >/dev/null
  local target_schema
  target_schema="$(gen verify --root "$release_base" --id "$to" "${OWNER_FLAG[@]}" | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["migrations"]["current"])')"

  # 1. A verified snapshot, taken by the generation that is running now.
  local backup_result="$state_dir/upgrade-backup.result.json" snapshot
  rm -f "$backup_result"
  load_backup_env || die "the repository environment could not be loaded"
  if ! "$(generation_dir "$from")/scripts/backup.sh" --candidate-lock "$current_lock" --result-out "$backup_result" 9>&-; then
    apply_plan abort "the pre-upgrade backup failed" || true
    write_result "$(json_get state <<<"$(journal_show)")" "the pre-upgrade backup failed; nothing changed"
    die "the pre-upgrade backup failed; the node was left on the installed generation"
  fi
  snapshot="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["snapshot"])' "$backup_result")" || die "the backup reported no snapshot"
  journal_advance snapshot-verified --snapshot "$snapshot"

  # 2. Writers down, databases up; the target's migrations run against the live database.
  journal_advance maintenance
  stop_writers
  journal_advance migrating --post-schema "$target_schema"
  if ! cli "$to" migrate >/dev/null; then
    local after; after="$(db_schema "$from" 2>/dev/null || echo unknown)"
    if [ "$after" = "$schema" ]; then
      apply_plan abort "the migrations failed before changing the schema" || true
      write_result "$(json_get state <<<"$(journal_show)")" "migration failed; schema unchanged; returned to the installed generation"
      die "migration failed before changing the schema; returned to the installed generation"
    fi
    apply_plan restore "a migration failed part-way (schema $after, expected $schema or $target_schema)" || true
    write_result restore-required "a migration failed part-way"
    exit 1
  fi
  local after; after="$(db_schema "$to")" || after=unknown
  if [ "$after" != "$target_schema" ]; then
    if [ "$after" = "$schema" ]; then
      apply_plan abort "the migration service exited cleanly but the schema did not move" || true
      write_result aborted "the schema did not move"; die "the schema did not move; returned to the installed generation"
    fi
    apply_plan restore "after migration the schema is $after, not $target_schema" || true
    write_result restore-required "unexpected schema after migration"; exit 1
  fi

  # 3. Activate, restart on the target's images, wait for readiness.
  if ! roll_forward "$to"; then
    fail_forward "the target generation did not become ready" || true
    local ended; ended="$(json_get state <<<"$(journal_show)")"
    write_result "$ended" "the target generation did not become ready"
    die "the target did not become ready; the node ended in state '$ended'"
  fi
  write_result complete "upgraded from ${from:0:12} to ${to:0:12}; snapshot ${snapshot:0:8}"
  log "upgraded to ${to:0:12} (schema $schema -> $after); snapshot ${snapshot:0:8} kept"
}

with_maintenance_lock run_upgrade
