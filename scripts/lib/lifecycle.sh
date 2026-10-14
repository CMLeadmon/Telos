#!/usr/bin/env bash
# lifecycle.sh -- shared steps of upgrade.sh and rollback.sh. Sourced, never run.
#
# A node runs one *generation* at a time: an immutable directory /srv/telos/generations/<64 hex>/
# holding a complete release tree (its own `telos` CLI, scripts, Compose files) plus generation.json.
# `/srv/telos/current` selects the active one; the durable journal in <node>/state/operation.json
# records every step before it is taken, so an interrupted operation is recoverable from the
# journal alone (scripts/lib/generations.py decides what recovery does; this file performs it).
#
# Expected from the sourcing script: script_dir (the scripts/ directory), release_base,
# node_path, state_dir, plus log/die. Test seams: TELOS_GENERATION_OWNER_CHECK=0 (skip the
# root-ownership requirement), TELOS_LIFECYCLE_READY_SECONDS (readiness wait, default 300).

GENERATIONS_PY="$script_dir/lib/generations.py"
WRITERS=(telos-traefik telos-core telos-client telos-jellyfin telos-grimmory)
READY_SECONDS="${TELOS_LIFECYCLE_READY_SECONDS:-300}"
OWNER_FLAG=()
[ "${TELOS_GENERATION_OWNER_CHECK:-1}" = 0 ] && OWNER_FLAG=(--no-owner-check)

gen() { python3 -I "$GENERATIONS_PY" "$@" 9>&-; }
json_get() { python3 -I -c 'import json,sys; value = json.load(sys.stdin)[sys.argv[1]]; print("" if value is None else value)' "$1"; }
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
generation_dir() { printf '%s/generations/%s' "$release_base" "$1"; }

# A generation's own CLI, pointed at this node.
cli() {   # GENERATION_ID args...
  local id="$1"; shift
  TELOS_NODE_PATH="$node_path" TELOS_RELEASE_ROOT="$(generation_dir "$id")" "$(generation_dir "$id")/telos" "$@" 9>&-
}

db_schema() { cli "$1" migrate --status; }

# check_images ID -- every image the generation runs resolves from local storage. Loading another candidate that reuses a
# repository:tag moves the tag off an older generation's image, and Compose would then try to pull a localhost name; failing
# here, before anything stops, leaves the node serving. install-candidate.sh loads (and pins) the images again.
check_images() {
  local ref refs
  refs="$(gen images --root "$release_base" --id "$1" "${OWNER_FLAG[@]}" | python3 -I -c 'import json,sys; print("\n".join(json.load(sys.stdin)["images"]))')" || return 1
  while IFS= read -r ref; do
    [ -n "$ref" ] || continue
    podman image exists "$ref" 9>&- || { log "image $ref of generation ${1:0:12} is not in local storage; run install-candidate.sh for that candidate again"; return 1; }
  done <<<"$refs"
}

# Ready = every expected container healthy AND the gateway reports ok AND no open bridge has forwarding off.
node_ready() {   # GENERATION_ID
  local body
  body="$(cli "$1" status --json 2>/dev/null)" || return 1
  python3 -I -c 'import json,sys; d = json.loads(sys.argv[1]); sys.exit(0 if (d.get("gateway") or {}).get("status") == "ok" and all(n.get("forwarding") != "off" for n in d.get("network") or []) else 1)' "$body" 2>/dev/null
}

wait_ready() {   # GENERATION_ID
  local deadline=$((SECONDS + READY_SECONDS))
  until node_ready "$1"; do
    [ "$SECONDS" -lt "$deadline" ] || return 1
    sleep "${TELOS_LIFECYCLE_POLL_SECONDS:-3}"
  done
}

stop_writers() {
  local name
  for name in "${WRITERS[@]}"; do
    podman stop -t 30 "$name" >/dev/null 2>&1 9>&- || true
  done
}

journal_show() { gen journal show --node "$node_path"; }
journal_advance() { gen journal advance --node "$node_path" --state "$@" >/dev/null; }   # STATE [--snapshot ID] [--post-schema N] [--error T]

# Write the outcome for whoever invoked us. Always JSON, always atomic, never a secret.
write_result() {   # status [detail]
  local tmp="$result_out.tmp"
  python3 -I - "$result_out" "$1" "${2:-}" "$node_path" <<'PY'
import json, os, sys
out, status, detail, node = sys.argv[1:5]
try:
    journal = json.load(open(os.path.join(node, "state", "operation.json")))
except (OSError, ValueError):
    journal = {}
record = {"status": status, "detail": detail[:300] or None, "operationId": journal.get("operationId"), "kind": journal.get("kind"),
          "fromGeneration": journal.get("fromGeneration"), "toGeneration": journal.get("toGeneration"), "snapshotId": journal.get("snapshotId"),
          "preSchema": journal.get("preSchema"), "postSchema": journal.get("postSchema"), "state": journal.get("state"), "updatedAt": journal.get("updatedAt")}
tmp = out + ".tmp"
with open(os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as handle:
    json.dump(record, handle, indent=1, sort_keys=True)
os.replace(tmp, out)
PY
}

start_generation() {   # id
  cli "$1" start >/dev/null || return 1
  wait_ready "$1"
}

# Move an upgrade from "the schema reached the target's" to "the target is serving and recorded".
# Every transition is written before it is acted on, so an interruption anywhere is recoverable.
roll_forward() {   # target_id
  local to="$1" state
  state="$(json_get state <<<"$(journal_show)")"
  [ "$state" != migrating ] || { journal_advance migrated; state=migrated; }
  if [ "$state" = migrated ]; then journal_advance activated; state=activated; fi
  if [ "$state" = activated ]; then
    gen activate --root "$release_base" --id "$to" >/dev/null || return 1
    cli "$to" stop >/dev/null || log "stopping the old stack failed; starting the target anyway"
    start_generation "$to" || return 1
    journal_advance serving
  else
    gen activate --root "$release_base" --id "$to" >/dev/null || return 1
    wait_ready "$to" || return 1
  fi
  finish_operation "$to"
}

# The target cannot be made to serve. The schema is already migrated, so the only questions are
# whether the previous binaries accept it (fast rollback) or the recorded snapshot must be restored.
fail_forward() {   # reason
  local journal from to post ok
  journal="$(journal_show)"
  from="$(json_get fromGeneration <<<"$journal")"; to="$(json_get toGeneration <<<"$journal")"; post="$(json_get postSchema <<<"$journal")"
  if [ -n "$from" ] && [ -n "$post" ]; then
    ok="$(gen fast-rollback --root "$release_base" --leaving "$to" --target "$from" --schema "$post" "${OWNER_FLAG[@]}" | json_get ok)" || ok=False
  else
    ok=False
  fi
  if [ "$ok" = True ]; then apply_plan rollback "$1"; else apply_plan restore "$1"; fi
}

# Carry out what plan-recovery decided.
#   abort     nothing durable changed: make FROM active again, start it
#   continue  the schema reached the target's: activate TARGET and start it
#   rollback  the target is unhealthy but the previous binaries accept the schema
#   restore   the database is in a state the previous binaries cannot run on
apply_plan() {   # action reason
  local action="$1" reason="$2" journal from to snapshot opid
  journal="$(journal_show)"
  from="$(json_get fromGeneration <<<"$journal")"; to="$(json_get toGeneration <<<"$journal")"
  snapshot="$(json_get snapshotId <<<"$journal")"; opid="$(json_get operationId <<<"$journal")"
  case "$action" in
    none) return 0 ;;
    abort)
      [ -n "$from" ] || { journal_advance failed --error "no generation to return to"; return 1; }
      gen activate --root "$release_base" --id "$from" >/dev/null || { journal_advance failed --error "could not reactivate the previous generation"; return 1; }
      if start_generation "$from"; then journal_advance aborted --error "$reason"; else journal_advance failed --error "the previous generation did not come back after an abort"; return 1; fi ;;
    continue)
      roll_forward "$to" || fail_forward "the migrated target did not start ($reason)"
      return $? ;;
    rollback)
      gen activate --root "$release_base" --id "$from" >/dev/null && { cli "$to" stop >/dev/null || true; } && start_generation "$from" \
        || { journal_advance restore-required --error "fast rollback did not bring the previous generation up"; return 1; }
      journal_advance rolled-back --error "$reason"
      gen receipt --root "$release_base" --node "$node_path" --id "$from" "${OWNER_FLAG[@]}" >/dev/null || true ;;
    restore)
      journal_advance restore-required --error "$reason"
      log "the database cannot be run by the previous generation; restore the recorded snapshot ${snapshot:0:8}:"
      log "  scripts/restore.sh --mode rollback --operation-id $opid --confirm RESTORE-RECORDED-ROLLBACK"
      return 1 ;;
    *) die "unknown recovery action '$action'" ;;
  esac
}

# Recover an unfinished operation, if any. Returns 0 when there is nothing (left) to recover,
# 1 when recovery ended in a state the operator must act on, and sets RECOVERED_STATE.
RECOVERED_STATE=""
recover_unfinished() {
  local journal state active schema plan action reason ready=()
  journal="$(journal_show)"
  [ -n "$(json_get operationId <<<"$journal" 2>/dev/null)" ] || return 0
  state="$(json_get state <<<"$journal")"
  case "$state" in complete|aborted|rolled-back|restore-required|failed) return 0 ;; esac
  log "an earlier $(json_get kind <<<"$journal") ($(json_get operationId <<<"$journal" | cut -c1-8)) stopped in state '$state'; recovering it first"
  active="$(gen current --root "$release_base" | json_get current)"
  [ -n "$active" ] || active="$(json_get fromGeneration <<<"$journal")"
  schema="$(db_schema "$active" 2>/dev/null || true)"
  case "$schema" in ''|*[!0-9]*)
    # The database is not answering. Start it (it is never stopped by an upgrade) and ask again.
    cli "$active" start >/dev/null 2>&1 || true
    schema="$(db_schema "$active" 2>/dev/null || true)" ;;
  esac
  case "$schema" in ''|*[!0-9]*)
    journal_advance failed --error "the database schema could not be read during recovery"; RECOVERED_STATE=failed; return 1 ;;
  esac
  plan="$(gen plan-recovery --root "$release_base" --node "$node_path" --schema "$schema" "${OWNER_FLAG[@]}")"
  action="$(json_get action <<<"$plan")"; reason="$(json_get reason <<<"$plan")"
  if [ "$action" = verify ]; then
    # An interruption can have caught the start itself; starting is idempotent, so try once
    # before deciding the target is unhealthy.
    node_ready "$active" || start_generation "$active" || true
    if node_ready "$active"; then ready=(--ready true); else ready=(--ready false); fi
    plan="$(gen plan-recovery --root "$release_base" --node "$node_path" --schema "$schema" "${ready[@]}" "${OWNER_FLAG[@]}")"
    action="$(json_get action <<<"$plan")"; reason="$(json_get reason <<<"$plan")"
    if [ "$action" = none ]; then
      # Active and ready: finish the bookkeeping the interruption skipped.
      [ "$state" = activated ] && journal_advance serving
      finish_operation "$(json_get toGeneration <<<"$journal")" || return 1
      RECOVERED_STATE=complete; return 0
    fi
  fi
  apply_plan "$action" "$reason" || { RECOVERED_STATE="$(json_get state <<<"$(journal_show)")"; return 1; }
  RECOVERED_STATE="$(json_get state <<<"$(journal_show)")"
  return 0
}

# Final bookkeeping of a successful operation: the receipt names the generation now installed.
finish_operation() {   # active_generation_id
  gen receipt --root "$release_base" --node "$node_path" --id "$1" "${OWNER_FLAG[@]}" >/dev/null || return 1
  journal_advance complete
}
