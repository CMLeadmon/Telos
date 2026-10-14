#!/usr/bin/env bash
# backup.sh -- one consistent, encrypted, verified, off-node backup of a Telos node.
#
#   backup.sh [--candidate-lock PATH] [--result-out PATH]
#
# What it does, in order:
#   1. takes the host-wide maintenance lock (contention exits 75 before anything changes)
#      and removes any staging directory an earlier, killed run journaled;
#   2. validates the restic configuration: the repository must be OFF this node, already
#      initialized (never created here), with a password file only root can read;
#   3. stops the writers (Traefik, gateway, Jellyfin, Grimmory), keeping both databases
#      and the static client up, and records that moment as the consistency point;
#   4. collects PostgreSQL (custom format) and MariaDB dumps, the node's configuration and
#      keys, the Traefik/Jellyfin/Grimmory volumes, shared storage, the release capsule and
#      the migration manifest into a 0700 staging directory;
#   5. restarts exactly the writers it stopped, BEFORE the long upload;
#   6. writes a strict manifest (sizes and SHA-256 of every component) and uploads the stage
#      as one restic snapshot tagged scheduled / telos / release / candidate;
#   7. proves the snapshot: its tags, its listing, the manifest read BACK from the repository
#      equals the staged one, and a 5% data read check passes;
#   8. applies 14 daily / 8 weekly retention, and records last-success / last-result for the
#      internal collector atomically.
#
# On every exit path -- success, failure, SIGINT, SIGTERM -- the staging directory (which
# holds plaintext secrets) is removed and any service this run stopped is started again;
# the original exit status is preserved unless the restart itself failed. A failure after
# the snapshot exists names the snapshot; it is never reported as verified.
#
# Environment (usually /etc/telos/backup.env, mode 0600, root-owned):
#   RESTIC_REPOSITORY        required; sftp:, s3:, b2:, rest:, rclone:, azure: or gs: only
#   RESTIC_PASSWORD_FILE     required; absolute path, mode 0600, owned by the caller
#   RESTIC_CREDENTIALS_FILE  optional KEY=VALUE backend credentials, same ownership/mode
#   TELOS_NODE_PATH          node directory (default /var/lib/telos)
#   TELOS_BACKUP_TMP_ROOT    staging root (default /var/tmp); needs room for the stage
#   TELOS_RELEASE_ROOT       installed release tree (default /opt/telos)
# Test seams (tests/operations/backup_restore_test.sh): TELOS_BACKUP_REQUIRE_ROOT=0.

set -euo pipefail
umask 077

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/maintenance.sh
. "$script_dir/lib/maintenance.sh"

EXIT_BUSY=75
WRITERS_STOP_ORDER=(telos-traefik telos-core telos-jellyfin telos-grimmory)
WRITERS_START_ORDER=(telos-grimmory telos-jellyfin telos-core telos-traefik)
REQUIRED_CONTAINERS=(telos-traefik telos-core telos-client telos-postgres telos-grimmory-db telos-jellyfin telos-grimmory)
RETENTION_DAILY=14
RETENTION_WEEKLY=8

node_path="${TELOS_NODE_PATH:-/var/lib/telos}"
state_dir="$node_path/state"
ops_dir="$node_path/ops"
tmp_root="${TELOS_BACKUP_TMP_ROOT:-/var/tmp}"
release_root="${TELOS_RELEASE_ROOT:-/opt/telos}"
export TELOS_MAINTENANCE_DIR="$state_dir"
journal="$state_dir/backup-stage.journal"

candidate_lock=""
result_out=""
stage=""
data=""
work=""
step="starting"
started_at=""
stopped=()          # writers this run stopped and has not yet restarted
snapshot_id=""
backup_done=0

# External commands must not inherit the maintenance lock descriptor: a container started
# from here would otherwise keep the lock held for as long as it runs.
#
# Bash defers a trap until the foreground command finishes, so a long dump or upload would
# make SIGTERM wait for it. Every external command therefore runs in the background and is
# waited on: `wait` returns the moment a trapped signal arrives, the handler stops the child
# and the EXIT cleanup runs.
child_pid=""
run_interruptible() {
  # An asynchronous command in a non-interactive shell reads /dev/null unless stdin is
  # redirected explicitly, which would silently discard a here-document or pipe meant for it.
  "$@" 9>&- <&0 &
  child_pid=$!
  local status=0
  wait "$child_pid" || status=$?
  child_pid=""
  return "$status"
}
podman()  { run_interruptible command podman "$@"; }
restic()  { run_interruptible command restic "$@"; }
tar()     { run_interruptible command tar "$@"; }

# Starting a container from inside a systemd service leaves its conmon in the SERVICE's
# cgroup (Podman splits cgroups when it runs under a unit), so when the one-shot backup
# unit finishes, systemd would kill conmon and with it the gateway, Traefik and the media
# servers this very run just restarted. A transient scope of its own keeps them alive.
start_container() {
  local scoped=0
  if [ "${TELOS_START_IN_SCOPE:-auto}" = 1 ]; then scoped=1
  elif [ "${TELOS_START_IN_SCOPE:-auto}" = auto ] && [ "$(id -u)" -eq 0 ] && [ -d /run/systemd/system ] && command -v systemd-run >/dev/null 2>&1; then scoped=1
  fi
  if [ "$scoped" -eq 1 ]; then
    run_interruptible systemd-run --scope --quiet --collect "$(type -P podman)" start "$1" >/dev/null
  else
    podman start "$1" >/dev/null
  fi
}

log() { printf 'backup: %s\n' "$*" >&2; }
die() { log "$*"; exit "${2:-1}"; }
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

usage() { echo "usage: $0 [--candidate-lock PATH] [--result-out PATH]" >&2; exit 2; }
while [ $# -gt 0 ]; do
  case "$1" in
    --candidate-lock) [ $# -ge 2 ] && [ -z "$candidate_lock" ] || usage; candidate_lock="$2"; shift 2 ;;
    --result-out)     [ $# -ge 2 ] && [ -z "$result_out" ] || usage; result_out="$2"; shift 2 ;;
    *) usage ;;
  esac
done

# ---- safety helpers -------------------------------------------------------------------

# A staging path may be removed only if it is exactly a directory this script made: under
# the validated temp root, named telos-backup.*, owned by the caller, never a symlink.
safe_stage_path() {
  local path="$1" parent base resolved_root
  [ -n "$path" ] || return 1
  case "$path" in /|"$HOME"|"$HOME"/|"$release_root"|"$node_path"|"${STORAGE_PATH:-/nonexistent}") return 1 ;; esac
  [ -d "$path" ] && [ ! -L "$path" ] || return 1
  parent="$(cd "$(dirname "$path")" 2>/dev/null && pwd -P)" || return 1
  resolved_root="$(cd "$tmp_root" 2>/dev/null && pwd -P)" || return 1
  base="$(basename "$path")"
  [ "$parent" = "$resolved_root" ] || return 1
  case "$base" in telos-backup.??????*) ;; *) return 1 ;; esac
  [ "$(stat -c %u "$path")" = "$(id -u)" ] || return 1
  return 0
}

remove_stage() {
  local path="$1"
  if safe_stage_path "$path"; then
    rm -rf --one-file-system -- "$path"
  elif [ -e "$path" ] || [ -L "$path" ]; then
    log "refusing to remove $path: it is not a staging directory made by this script"
    return 1
  fi
}

# A run killed with SIGKILL cannot clean up, so it journals its stage first; the next run,
# holding the lock (so no other run is alive), removes exactly that path.
recover_orphan_stage() {
  [ -f "$journal" ] || return 0
  local orphan names name writer; orphan="$(head -n1 "$journal" 2>/dev/null || true)"
  # Writers the killed run had stopped stay down otherwise: start any still stopped.
  names="$(sed -n 's/^stopped=//p' "$journal" | head -n1)"
  for name in ${names//,/ }; do
    for writer in "${WRITERS_START_ORDER[@]}"; do
      if [ "$name" = "$writer" ] && [ "$(podman inspect --type container --format '{{.State.Running}}' "$writer" 2>/dev/null || echo false)" != true ]; then
        start_container "$writer" 2>/dev/null && log "restarted $writer, which an earlier run left stopped" || log "could not restart $writer"
      fi
    done
  done
  if [ -n "$orphan" ]; then
    if remove_stage "$orphan"; then
      log "removed the staging directory an earlier run left behind"
    else
      log "could not remove the journaled staging path; leaving the journal for the operator"
      return 1
    fi
  fi
  rm -f "$journal"
}

file_mode_ok() {   # path -> owned by caller, no group/other access
  [ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -c %u "$1")" = "$(id -u)" ] && [ $(( 0$(stat -c %a "$1") & 077 )) -eq 0 ]
}

# ---- cleanup --------------------------------------------------------------------------

unmark_stopped() {   # a service started again is no longer ours to restart
  local keep=() c
  for c in "${stopped[@]:-}"; do [ -z "$c" ] || [ "$c" = "$1" ] || keep+=("$c"); done
  stopped=("${keep[@]:-}")
  [ -n "${stopped[0]:-}" ] || stopped=()
}

write_status() {   # result(1|0) -> atomic textfile for the internal collector
  local result="$1" last_success=0 file="$ops_dir/last-backup.prom" tmp
  [ -d "$ops_dir" ] || return 0
  if [ "$result" = 1 ]; then
    last_success="$(date -u +%s)"
  elif [ -f "$file" ]; then
    last_success="$(sed -n 's/^telos_backup_last_success_timestamp_seconds \([0-9][0-9]*\)$/\1/p' "$file" | head -n1)"
    last_success="${last_success:-0}"
  fi
  tmp="$(mktemp "$ops_dir/.last-backup.XXXXXX")" || return 1
  {
    printf '# written by backup.sh; read by the gateway as exactly these two series\n'
    printf 'telos_backup_last_success_timestamp_seconds %s\n' "$last_success"
    printf 'telos_backup_last_result %s\n' "$result"
  } >"$tmp"
  chmod 0640 "$tmp"
  { chgrp 10001 "$tmp" 2>/dev/null || true; }
  mv -f "$tmp" "$file"
}

cleanup() {
  local status=$? restart_failed=0 name
  trap - EXIT
  set +e
  # 1. Restart what we stopped, each independently, whatever else fails.
  for name in "${WRITERS_START_ORDER[@]}"; do
    if printf '%s\n' "${stopped[@]:-}" | grep -qx -- "$name"; then
      if start_container "$name" 2>/dev/null; then
        :
      else
        restart_failed=1
        log "could not restart $name -- start it by hand: podman start $name"
      fi
    fi
  done
  # 2. Remove the plaintext stage, independently of step 1.
  if [ -n "$stage" ]; then
    if remove_stage "$stage"; then
      rm -f "$journal"
    else
      status=${status:-1}; [ "$status" -eq 0 ] && status=1
    fi
  fi
  # 3. Record the outcome for the collector (failure keeps the last-success time).
  if [ "$backup_done" -eq 1 ] && [ "$status" -eq 0 ] && [ "$restart_failed" -eq 0 ]; then
    :  # success was recorded when the snapshot was proven
  elif [ "$status" -ne 0 ] || [ "$restart_failed" -ne 0 ]; then
    write_status 0
  fi
  if [ "$status" -ne 0 ]; then
    if [ -n "$snapshot_id" ]; then
      log "FAILED at step '$step' -- snapshot ${snapshot_id:0:8} exists in the repository but is NOT verified"
    else
      log "FAILED at step '$step'"
    fi
  fi
  [ "$restart_failed" -eq 1 ] && [ "$status" -eq 0 ] && status=1
  exit "$status"
}

on_signal() {
  log "interrupted ($1)"
  if [ -n "$child_pid" ]; then
    kill -TERM "$child_pid" 2>/dev/null || true
    wait "$child_pid" 2>/dev/null || true
    child_pid=""
  fi
  exit "$2"
}
trap cleanup EXIT
trap 'on_signal SIGINT 130' INT
trap 'on_signal SIGTERM 143' TERM
trap 'on_signal SIGHUP 129' HUP

# ---- the work, under the maintenance lock ---------------------------------------------

run_backup() {
  started_at="$(now)"
  step="recover"
  recover_orphan_stage || exit 1

  step="configuration"
  [ "${TELOS_BACKUP_REQUIRE_ROOT:-1}" = 0 ] || [ "$(id -u)" -eq 0 ] || die "run as root (rootful Podman owns the containers)"
  : "${RESTIC_REPOSITORY:?RESTIC_REPOSITORY must name an off-node repository}"
  : "${RESTIC_PASSWORD_FILE:?RESTIC_PASSWORD_FILE must name a mode-0600 password file}"
  case "$RESTIC_REPOSITORY" in
    sftp:*|s3:*|b2:*|rest:*|rclone:*|azure:*|gs:*|swift:*) ;;
    *) die "RESTIC_REPOSITORY must be an off-node backend (sftp, s3, b2, rest, rclone, azure, gs, swift), not a local path" ;;
  esac
  file_mode_ok "$RESTIC_PASSWORD_FILE" || die "the restic password file must be a regular file you own with mode 0600"
  if [ -n "${RESTIC_CREDENTIALS_FILE:-}" ]; then
    file_mode_ok "$RESTIC_CREDENTIALS_FILE" || die "the restic credentials file must be a regular file you own with mode 0600"
    set -a; . "$RESTIC_CREDENTIALS_FILE"; set +a
  fi
  export RESTIC_REPOSITORY RESTIC_PASSWORD_FILE
  export RESTIC_CACHE_DIR="${RESTIC_CACHE_DIR:-/var/cache/telos-restic}"
  [ -z "$candidate_lock" ] || [ -f "$candidate_lock" ] || die "candidate lock not found: $candidate_lock"
  [ -f "$node_path/node.env" ] || die "$node_path/node.env not found"
  mkdir -p "$state_dir" "$ops_dir" "$tmp_root"
  tmp_root="$(cd "$tmp_root" && pwd -P)"
  case "$tmp_root" in /) die "TELOS_BACKUP_TMP_ROOT must not be /" ;; esac

  step="container validation"
  local container
  for container in "${REQUIRED_CONTAINERS[@]}"; do
    podman container exists "$container" || die "required container does not exist: $container"
  done

  step="repository check"
  restic cat config >/dev/null 2>&1 \
    || die "the repository is unreachable or not initialized; initialize it once, deliberately, with: restic init"

  step="staging"
  stage="$(mktemp -d "$tmp_root/telos-backup.XXXXXX")"
  chmod 0700 "$stage"
  printf '%s\n' "$stage" >"$journal"
  # data/ is what is uploaded; work/ holds scratch files (restic output, listings) that
  # must be removed with the stage but never enter the snapshot.
  data="$stage/data"; work="$stage/work"
  mkdir -m 0700 "$data" "$work"

  step="quiesce"
  local was_running
  for container in "${WRITERS_STOP_ORDER[@]}"; do
    was_running="$(podman inspect --type container --format '{{.State.Running}}' "$container" 2>/dev/null || echo false)"
    if [ "$was_running" = true ]; then
      stopped+=("$container")
    fi
  done
  # Journal the services BEFORE stopping them so a SIGKILL cannot leave them down unnoticed.
  printf 'stopped=%s\n' "$(IFS=,; echo "${stopped[*]:-}")" >>"$journal"
  for container in "${stopped[@]:-}"; do
    [ -n "$container" ] || continue
    podman stop -t 60 "$container" >/dev/null || die "could not stop $container"
  done
  local consistency_point; consistency_point="$(now)"

  step="postgres dump"
  podman exec telos-postgres sh -c 'exec pg_dump --format=custom --no-owner --username="$POSTGRES_USER" "$POSTGRES_DB"' >"$data/postgres.dump"
  step="migration manifest"
  podman exec -i telos-postgres sh -c 'exec psql --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" --no-psqlrc --tuples-only --no-align' >"$data/migrations.json" <<'SQL'
SELECT coalesce(json_agg(json_build_object('version', version, 'name', name, 'checksum', checksum) ORDER BY version), '[]') FROM schema_migrations;
SQL
  step="mariadb dump"
  podman exec telos-grimmory-db sh -c 'exec mariadb-dump --user=root --password="$MYSQL_ROOT_PASSWORD" --single-transaction --routines --events --databases "$MYSQL_DATABASE"' >"$data/grimmory.sql"
  step="configuration copy"
  cp -p -- "$node_path/node.env" "$data/node.env"
  tar -C "$node_path" -cf "$data/node-config.tar" config
  step="volume export"
  podman volume export telos_traefik_acme --output "$data/traefik-acme.tar"
  podman volume export telos_jellyfin_config --output "$data/jellyfin-config.tar"
  podman volume export telos_grimmory_config --output "$data/grimmory-config.tar"
  step="storage archive"
  local storage_path
  storage_path="$(sed -n "s/^STORAGE_PATH=['\"]\{0,1\}\([^'\"]*\)['\"]\{0,1\}$/\1/p" "$node_path/node.env" | head -n1)"
  storage_path="${storage_path:-/mnt/storage/shared}"
  [ -d "$storage_path" ] || die "shared storage does not exist: $storage_path"
  tar -C "$storage_path" -cf "$data/shared-storage.tar" --exclude=./staging/tmp .
  step="release capsule"
  [ -d "$release_root" ] || die "the installed release was not found at $release_root"
  tar -C "$release_root" -cf "$data/release-capsule.tar" --exclude=.git --exclude=node_modules --exclude=telos-review .

  step="restart writers"
  for container in "${WRITERS_START_ORDER[@]}"; do
    if printf '%s\n' "${stopped[@]:-}" | grep -qx -- "$container"; then
      start_container "$container" || die "could not restart $container"
      unmark_stopped "$container"
    fi
  done

  step="manifest"
  local candidate_digest="" release_id="unreleased" source_commit="" host_id tags=() manifest="$data/manifest.json"
  if [ -n "$candidate_lock" ]; then
    candidate_digest="sha256:$(sha256sum "$candidate_lock" | cut -d' ' -f1)"
    source_commit="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1])).get("sourceCommit",""))' "$candidate_lock" 2>/dev/null || true)"
    release_id="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1])).get("version","unreleased"))' "$candidate_lock" 2>/dev/null || echo unreleased)"
  elif [ -f "$state_dir/install-receipt.json" ]; then
    release_id="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1])).get("release","unreleased"))' "$state_dir/install-receipt.json" 2>/dev/null || echo unreleased)"
  fi
  host_id="sha256:$(sha256sum /etc/machine-id | cut -d' ' -f1)"
  tags=(scheduled telos "release:$release_id" "candidate:${candidate_digest:-none}")
  python3 -I "$script_dir/lib/backup_manifest.py" create --stage "$data" --out "$manifest" --started "$started_at" \
    --consistency-point "$consistency_point" --host-id-sha256 "$host_id" --release "$release_id" \
    --source-commit "$source_commit" --candidate-lock-digest "$candidate_digest" \
    $(printf -- '--tag %s ' "${tags[@]}") --migrations "$data/migrations.json"

  step="upload"
  local tag_args=() tag
  for tag in "${tags[@]}"; do tag_args+=(--tag "$tag"); done
  local upload_json="$work/upload.json"
  restic backup "$data" "${tag_args[@]}" --host telos-node --json >"$upload_json"
  snapshot_id="$(python3 -I "$script_dir/lib/backup_manifest.py" snapshot-id <"$upload_json")"
  rm -f "$upload_json"

  step="snapshot verification"
  local listing="$work/snapshots.json" fetched="$work/manifest.json"
  restic snapshots --json "$snapshot_id" >"$listing"
  python3 -I "$script_dir/lib/backup_manifest.py" has-tags --snapshots "$listing" --id "$snapshot_id" "${tag_args[@]}"
  local contents; contents="$(restic ls --json "$snapshot_id" "$data")"
  grep -q "\"path\":\"$data/manifest.json\"" <<<"$contents" || die "the snapshot does not list the manifest"
  restic dump "$snapshot_id" "$data/manifest.json" >"$fetched"
  python3 -I "$script_dir/lib/backup_manifest.py" compare --expected "$manifest" --actual "$fetched"
  rm -f "$listing" "$fetched"

  step="repository check (5% of data)"
  restic check --read-data-subset=5% >/dev/null

  # Verified. Record it before retention so a prune problem cannot erase the success.
  step="status"
  write_status 1
  backup_done=1

  step="retention"
  restic forget --tag telos --group-by host --keep-daily "$RETENTION_DAILY" --keep-weekly "$RETENTION_WEEKLY" --prune >/dev/null

  step="done"
  if [ -n "$result_out" ]; then
    printf '{"status":"passed","snapshot":"%s","manifest":"%s","at":"%s"}\n' "$snapshot_id" "$(sha256sum "$manifest" | cut -d' ' -f1)" "$(now)" >"$result_out.tmp" \
      && mv -f "$result_out.tmp" "$result_out"
  fi
  log "verified snapshot ${snapshot_id:0:8} (${#tags[@]} tags); retention ${RETENTION_DAILY}d/${RETENTION_WEEKLY}w applied"
}

with_maintenance_lock run_backup
