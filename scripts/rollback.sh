#!/usr/bin/env bash
# rollback.sh -- return the node to the generation an upgrade replaced, WITHOUT restoring data,
# when (and only when) that generation's binaries accept the database as it now stands.
#
#   rollback.sh --operation-id ID --result-out PATH --confirm ROLLBACK-THIS-NODE
#
# ID names the upgrade being undone (from the journal). A fast rollback requires the generation being
# left to declare the current schema inside rollbackCompatibleThrough and the previous generation to
# accept it (generations.py fast-rollback). If it does not, nothing is changed and the command prints
# the restore to run instead:
#   scripts/restore.sh --mode rollback --operation-id ID --confirm RESTORE-RECORDED-ROLLBACK
# Same-host rollback evidence never satisfies the separate-host-restore gate.
#
# Both generations and the journal are kept; nothing is deleted. Exit codes: 0 rolled back, 1 refused
# or failed, 2 usage, 75 another maintenance operation is running.
set -euo pipefail
umask 077

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/maintenance.sh
. "$script_dir/lib/maintenance.sh"

CONFIRMATION="ROLLBACK-THIS-NODE"
release_base="${TELOS_RELEASE_BASE:-/srv/telos}"
node_path="${TELOS_NODE_PATH:-/var/lib/telos}"
state_dir="$node_path/state"
machine_id_file="${TELOS_MACHINE_ID_FILE:-/etc/machine-id}"
export TELOS_MAINTENANCE_DIR="$state_dir"

log() { printf 'rollback: %s\n' "$*" >&2; }
die() { log "$*"; exit "${2:-1}"; }
# shellcheck source=lib/lifecycle.sh
. "$script_dir/lib/lifecycle.sh"

operation_id="" result_out="" confirm=""
usage() { echo "usage: $0 --operation-id ID --result-out PATH --confirm $CONFIRMATION" >&2; exit 2; }
while [ $# -gt 0 ]; do
  case "$1" in
    --operation-id) [ $# -ge 2 ] && [ -z "$operation_id" ] || usage; operation_id="$2"; shift 2 ;;
    --result-out)   [ $# -ge 2 ] && [ -z "$result_out" ] || usage; result_out="$2"; shift 2 ;;
    --confirm)      [ $# -ge 2 ] && [ -z "$confirm" ] || usage; confirm="$2"; shift 2 ;;
    *) usage ;;
  esac
done
[[ "$operation_id" =~ ^[0-9a-f]{32}$ ]] && [ -n "$result_out" ] || usage
if [ "$confirm" != "$CONFIRMATION" ]; then
  echo "a rollback stops the stack and starts the previous generation. Re-run with --confirm $CONFIRMATION to proceed." >&2
  exit 2
fi

run_rollback() {
  [ "${TELOS_LIFECYCLE_REQUIRE_ROOT:-1}" = 0 ] || [ "$(id -u)" -eq 0 ] || die "run as root (rootful Podman)"
  mkdir -p "$state_dir"
  local journal state kind from to
  # An unfinished operation is recovered first; it may be exactly the one the operator means.
  if ! recover_unfinished; then
    write_result "$RECOVERED_STATE" "an earlier operation needs the operator"
    die "an earlier operation ended in state '$RECOVERED_STATE'; nothing new was started"
  fi
  journal="$(journal_show)"
  [ "$(json_get operationId <<<"$journal")" = "$operation_id" ] || die "operation $operation_id is not the node's last operation; rollback undoes only the last upgrade"
  kind="$(json_get kind <<<"$journal")"; state="$(json_get state <<<"$journal")"
  [ "$kind" = upgrade ] || die "operation $operation_id is a $kind, not an upgrade"
  case "$state" in
    complete|failed|restore-required) ;;
    rolled-back) die "operation $operation_id was already rolled back" ;;
    aborted) die "operation $operation_id was aborted before any migration; the previous generation never stopped being the one to run" ;;
    *) die "operation $operation_id is in state '$state'" ;;
  esac
  from="$(json_get fromGeneration <<<"$journal")"; to="$(json_get toGeneration <<<"$journal")"
  [ "$(gen current --root "$release_base" | json_get current)" = "$to" ] || die "the active generation is not the upgrade's target; refusing to guess"

  local schema
  schema="$(db_schema "$to" 2>/dev/null || true)"
  case "$schema" in ''|*[!0-9]*) cli "$to" start >/dev/null 2>&1 || true; schema="$(db_schema "$to" 2>/dev/null || true)" ;; esac
  case "$schema" in ''|*[!0-9]*) die "the database schema could not be read; start the database and retry" ;; esac
  if [ "$(gen fast-rollback --root "$release_base" --leaving "$to" --target "$from" --schema "$schema" "${OWNER_FLAG[@]}" | json_get ok)" != True ]; then
    write_result refused "the previous generation cannot run on schema $schema"
    log "the previous generation's binaries cannot run on schema $schema; nothing was changed. Restore the recorded snapshot instead:"
    log "  scripts/restore.sh --mode rollback --operation-id $operation_id --confirm RESTORE-RECORDED-ROLLBACK"
    return 1
  fi
  gen verify --root "$release_base" --id "$from" "${OWNER_FLAG[@]}" >/dev/null || die "the previous generation did not verify"
  check_images "$from" || die "the previous generation's images are not all loaded; nothing was changed"

  local host; host="sha256:$(sha256sum "$machine_id_file" | cut -d' ' -f1)"
  gen journal begin --node "$node_path" --kind rollback --from "$to" --to "$from" --schema "$schema" --host "$host" --supersede "$operation_id" >/dev/null
  journal_advance maintenance
  cli "$to" stop >/dev/null || log "stopping the stack failed; continuing"
  journal_advance activated
  gen activate --root "$release_base" --id "$from" >/dev/null || { journal_advance failed --error "could not activate the previous generation"; write_result failed "activation failed"; exit 1; }
  if ! start_generation "$from"; then
    journal_advance failed --error "the previous generation did not become ready"
    write_result failed "the previous generation did not become ready; the failed generation and journal are kept"
    die "the previous generation did not become ready"
  fi
  journal_advance serving
  finish_operation "$from"
  write_result complete "rolled back from ${to:0:12} to ${from:0:12}"
  log "rolled back to ${from:0:12} on schema $schema; ${to:0:12} and the journal are kept"
}

with_maintenance_lock run_rollback
