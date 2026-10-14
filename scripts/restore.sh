#!/usr/bin/env bash
# restore.sh -- restore a Telos node from an encrypted off-node snapshot onto a DIFFERENT, empty host.
#
#   restore.sh --mode separate-host --snapshot ID (--candidate-lock PATH | --rehearsal)
#              --result-out PATH --confirm RESTORE-TO-EMPTY-REFERENCE-HOST
#
# Preconditions: the release is installed on this host (install.sh), Podman works, and
# RESTIC_REPOSITORY / RESTIC_PASSWORD_FILE name the off-node repository (see backup.sh).
#
# Nothing is started, stopped or overwritten until everything that can be checked has been:
#   1. the maintenance lock is taken (contention exits 75);
#   2. the snapshot is restored into a 0700 staging directory under the temp root;
#   3. the manifest and every component hash are verified, the host differs from the machine
#      that made the backup, the candidate matches, the schema is not newer than this release
#      and no migration checksum drifted, there is disk for it, and every archive is scanned
#      for traversal, links out of the tree, devices and FIFOs -- all before the first change;
#   4. the target must be an empty reference host (no Telos containers or volumes); prior
#      node files are moved, never deleted, into a validated rollback directory;
#   5. configuration, storage and volumes are restored; only the two databases are started;
#      PostgreSQL and MariaDB are restored; migrations run; `sanitize-restore` revokes every
#      credential the backup carried -- and only then does the rest of the stack start.
# Every transition is appended to the restore journal. On any failure the stack is brought
# down again (a node holding unsanitized credentials must not serve) and the staging
# directory, which holds plaintext secrets, is deleted.
#
# Exit codes: 0 restored, 1 refused or failed, 2 usage, 75 another maintenance operation.
# Test seams: TELOS_BACKUP_REQUIRE_ROOT=0, TELOS_MACHINE_ID_FILE.

set -euo pipefail
umask 077

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/maintenance.sh
. "$script_dir/lib/maintenance.sh"

CONFIRMATION="RESTORE-TO-EMPTY-REFERENCE-HOST"
VOLUMES=(traefik_acme jellyfin_config grimmory_config)

node_path="${TELOS_NODE_PATH:-/var/lib/telos}"
state_dir="$node_path/state"
tmp_root="${TELOS_BACKUP_TMP_ROOT:-/var/tmp}"
release_root="${TELOS_RELEASE_ROOT:-/opt/telos}"
machine_id_file="${TELOS_MACHINE_ID_FILE:-/etc/machine-id}"
journal="$state_dir/restore-journal.jsonl"
export TELOS_MAINTENANCE_DIR="$state_dir"

mode="" snapshot="" candidate_lock="" result_out="" confirm="" rehearsal=0
stage=""
step="starting"
stack_touched=0
child_pid=""

run_interruptible() {
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

log() { printf 'restore: %s\n' "$*" >&2; }
die() { log "$*"; exit "${2:-1}"; }
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
journal_event() {   # step state -- one JSON line per transition, written before the next begins
  mkdir -p "$state_dir"
  printf '{"at":"%s","step":"%s","state":"%s"}\n' "$(now)" "$1" "$2" >>"$journal"
}

usage() {
  echo "usage: $0 --mode separate-host --snapshot ID (--candidate-lock PATH | --rehearsal) --result-out PATH --confirm $CONFIRMATION" >&2
  exit 2
}
while [ $# -gt 0 ]; do
  case "$1" in
    --mode)           [ $# -ge 2 ] && [ -z "$mode" ] || usage; mode="$2"; shift 2 ;;
    --snapshot)       [ $# -ge 2 ] && [ -z "$snapshot" ] || usage; snapshot="$2"; shift 2 ;;
    --candidate-lock) [ $# -ge 2 ] && [ -z "$candidate_lock" ] || usage; candidate_lock="$2"; shift 2 ;;
    --rehearsal)      rehearsal=1; shift ;;
    --result-out)     [ $# -ge 2 ] && [ -z "$result_out" ] || usage; result_out="$2"; shift 2 ;;
    --confirm)        [ $# -ge 2 ] && [ -z "$confirm" ] || usage; confirm="$2"; shift 2 ;;
    *) usage ;;
  esac
done
[ "$mode" = separate-host ] || usage
[[ "$snapshot" =~ ^[0-9a-f]{8,64}$ ]] || usage
[ -n "$result_out" ] || usage
[ -n "$candidate_lock" ] || [ "$rehearsal" -eq 1 ] || usage
[ -z "$candidate_lock" ] || [ "$rehearsal" -eq 0 ] || usage
if [ "$confirm" != "$CONFIRMATION" ]; then
  echo "restore replaces databases, configuration, volumes and shared storage on THIS host." >&2
  echo "It is only for an empty reference host. Re-run with --confirm $CONFIRMATION to proceed." >&2
  exit 2
fi

# ---- safety helpers -------------------------------------------------------------------

safe_stage_path() {
  local path="$1" parent base resolved_root
  [ -n "$path" ] && [ -d "$path" ] && [ ! -L "$path" ] || return 1
  parent="$(cd "$(dirname "$path")" 2>/dev/null && pwd -P)" || return 1
  resolved_root="$(cd "$tmp_root" 2>/dev/null && pwd -P)" || return 1
  base="$(basename "$path")"
  [ "$parent" = "$resolved_root" ] && [ "$resolved_root" != / ] || return 1
  case "$base" in telos-restore.??????*) ;; *) return 1 ;; esac
  [ "$(stat -c %u "$path")" = "$(id -u)" ]
}

file_mode_ok() { [ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat -c %u "$1")" = "$(id -u)" ] && [ $(( 0$(stat -c %a "$1") & 077 )) -eq 0 ]; }

cleanup() {
  local status=$?
  trap - EXIT
  set +e
  if [ "$status" -ne 0 ] && [ "$stack_touched" -eq 1 ]; then
    # A node that holds credentials the sanitizer has not revoked must not serve traffic.
    log "bringing the stack down so nothing serves unsanitized state"
    stack down >/dev/null 2>&1
    journal_event "failure-stack-down" done
  fi
  if [ -n "$stage" ]; then
    if safe_stage_path "$stage"; then rm -rf --one-file-system -- "$stage"; else log "refusing to remove $stage"; status=1; fi
  fi
  if [ "$status" -ne 0 ]; then
    journal_event "$step" failed
    log "FAILED at step '$step'"
  fi
  exit "$status"
}
on_signal() {
  log "interrupted ($1)"
  if [ -n "$child_pid" ]; then kill -TERM "$child_pid" 2>/dev/null || true; wait "$child_pid" 2>/dev/null || true; child_pid=""; fi
  exit "$2"
}
trap cleanup EXIT
trap 'on_signal SIGINT 130' INT
trap 'on_signal SIGTERM 143' TERM
trap 'on_signal SIGHUP 129' HUP

# ---- the stack, through the operator CLI's own compose layer --------------------------

stack() {   # compose arguments
  REPO_ROOT="$release_root" CLI_DIR="$release_root/cli" TELOS_NODE_PATH="$node_path" \
    bash -c '. "$CLI_DIR/lib/common.sh"; . "$CLI_DIR/lib/config.sh"; compose_with -- "$@"' _ "$@" 9>&-
}
telos_cli() {
  TELOS_NODE_PATH="$node_path" "$release_root/telos" "$@" 9>&-
}
gateway_ready() {
  REPO_ROOT="$release_root" CLI_DIR="$release_root/cli" TELOS_NODE_PATH="$node_path" \
    bash -c '. "$CLI_DIR/lib/common.sh"; . "$CLI_DIR/lib/config.sh"; body="$(gateway_health)" && python3 -I -c "import json,sys; sys.exit(0 if json.loads(sys.argv[1]).get(\"status\") == \"ok\" else 1)" "$body"' 9>&-
}

# ---- the restore ----------------------------------------------------------------------

run_restore() {
  step="configuration"
  journal_event "$step" begin
  [ "${TELOS_BACKUP_REQUIRE_ROOT:-1}" = 0 ] || [ "$(id -u)" -eq 0 ] || die "run as root (rootful Podman)"
  : "${RESTIC_REPOSITORY:?RESTIC_REPOSITORY must name the off-node repository}"
  : "${RESTIC_PASSWORD_FILE:?RESTIC_PASSWORD_FILE must name a mode-0600 password file}"
  case "$RESTIC_REPOSITORY" in
    sftp:*|s3:*|b2:*|rest:*|rclone:*|azure:*|gs:*|swift:*) ;;
    *) die "RESTIC_REPOSITORY must be an off-node backend, not a local path" ;;
  esac
  file_mode_ok "$RESTIC_PASSWORD_FILE" || die "the restic password file must be a regular file you own with mode 0600"
  if [ -n "${RESTIC_CREDENTIALS_FILE:-}" ]; then
    file_mode_ok "$RESTIC_CREDENTIALS_FILE" || die "the restic credentials file must be a regular file you own with mode 0600"
    set -a; . "$RESTIC_CREDENTIALS_FILE"; set +a
  fi
  export RESTIC_REPOSITORY RESTIC_PASSWORD_FILE RESTIC_CACHE_DIR="${RESTIC_CACHE_DIR:-/var/cache/telos-restic}"
  [ -z "$candidate_lock" ] || [ -f "$candidate_lock" ] || die "candidate lock not found: $candidate_lock"
  [ -x "$release_root/telos" ] && [ -d "$release_root/cli" ] || die "the release is not installed at $release_root (run install.sh first)"
  [ -f "$release_root/docker-compose.yml" ] || die "no docker-compose.yml in $release_root"
  mkdir -p "$state_dir" "$tmp_root"; tmp_root="$(cd "$tmp_root" && pwd -P)"
  [ "$tmp_root" != / ] || die "the temp root must not be /"
  journal_event "$step" done

  step="empty-host check"
  journal_event "$step" begin
  local existing
  existing="$(podman ps -a --format '{{.Names}}' 2>/dev/null | grep -E '^telos-' || true)"
  [ -z "$existing" ] || die "this host already has Telos containers; a separate-host restore needs an empty reference host"
  local volume
  for volume in "${VOLUMES[@]}" postgres_data grimmory_db_data; do
    ! podman volume exists "telos_$volume" 2>/dev/null || die "volume telos_$volume already exists; this is not an empty reference host"
  done
  local rollback=""
  if [ -f "$node_path/node.env" ] || [ -d "$node_path/config" ]; then
    rollback="$(dirname "$node_path")/telos-rollback/$(date -u +%Y%m%dT%H%M%SZ)"
  fi
  journal_event "$step" done

  step="snapshot retrieval"
  journal_event "$step" begin
  restic cat config >/dev/null 2>&1 || die "the repository is unreachable or its password is wrong"
  stage="$(mktemp -d "$tmp_root/telos-restore.XXXXXX")"
  chmod 0700 "$stage"
  restic restore "$snapshot" --target "$stage/restored" >/dev/null || die "restic could not restore snapshot ${snapshot:0:8}"
  local manifests; manifests="$(find "$stage/restored" -type f -name manifest.json)"
  [ "$(printf '%s\n' "$manifests" | grep -c .)" -eq 1 ] || die "the snapshot must contain exactly one manifest.json"
  local data; data="$(dirname "$manifests")"
  journal_event "$step" done

  step="verification"
  journal_event "$step" begin
  local target_id candidate_digest="" free_tmp free_storage plan_args=()
  target_id="sha256:$(sha256sum "$machine_id_file" | cut -d' ' -f1)"
  [ -z "$candidate_lock" ] || candidate_digest="sha256:$(sha256sum "$candidate_lock" | cut -d' ' -f1)"
  free_tmp="$(df -PB1 "$tmp_root" | awk 'NR==2 {print $4}')"
  plan_args=(plan --data "$data" --target-machine-id-sha256 "$target_id" --migrations "$release_root/backend/db/migrations" --free "TMP=$free_tmp")
  local storage_path
  storage_path="$(sed -n "s/^STORAGE_PATH=['\"]\{0,1\}\([^'\"]*\)['\"]\{0,1\}$/\1/p" "$data/node.env" | head -n1)"
  storage_path="${storage_path:-/mnt/storage/shared}"
  case "$storage_path" in /*) ;; *) die "the backup's STORAGE_PATH is not absolute" ;; esac
  local probe="$storage_path"; while [ ! -d "$probe" ] && [ "$probe" != / ]; do probe="$(dirname "$probe")"; done
  free_storage="$(df -PB1 "$probe" | awk 'NR==2 {print $4}')"
  plan_args+=(--free "STORAGE=$free_storage")
  if [ "$rehearsal" -eq 1 ]; then plan_args+=(--rehearsal); else plan_args+=(--candidate-lock-digest "$candidate_digest"); fi
  python3 -I "$script_dir/lib/restore_check.py" "${plan_args[@]}" || exit 1
  python3 -I "$script_dir/lib/restore_check.py" tar "$data/node-config.tar" "$data/shared-storage.tar" \
    "$data/traefik-acme.tar" "$data/jellyfin-config.tar" "$data/grimmory-config.tar" "$data/release-capsule.tar" || exit 1
  journal_event "$step" done

  step="preserve prior state"
  journal_event "$step" begin
  if [ -n "$rollback" ]; then
    mkdir -p "$rollback"; chmod 0700 "$rollback"
    [ -f "$node_path/node.env" ] && mv -- "$node_path/node.env" "$rollback/node.env"
    [ -d "$node_path/config" ] && mv -- "$node_path/config" "$rollback/config"
    log "prior node files preserved in $rollback"
  fi
  if [ -d "$storage_path" ] && [ -n "$(ls -A "$storage_path" 2>/dev/null)" ]; then
    mkdir -p "${rollback:=$(dirname "$node_path")/telos-rollback/$(date -u +%Y%m%dT%H%M%SZ)}"; chmod 0700 "$rollback"
    mv -- "$storage_path" "$rollback/storage"
    log "prior shared storage preserved in $rollback/storage"
  fi
  journal_event "$step" done

  step="configuration restore"
  journal_event "$step" begin
  mkdir -p "$node_path"
  install -m 0600 -- "$data/node.env" "$node_path/node.env"
  tar -C "$node_path" -xf "$data/node-config.tar" --numeric-owner
  mkdir -p "$state_dir"; chmod 0700 "$state_dir"
  mkdir -p "$storage_path"
  tar -C "$storage_path" -xf "$data/shared-storage.tar" --numeric-owner
  journal_event "$step" done

  step="volume restore"
  journal_event "$step" begin
  stack_touched=1
  local pair name
  for pair in traefik_acme:traefik-acme.tar jellyfin_config:jellyfin-config.tar grimmory_config:grimmory-config.tar; do
    name="${pair%%:*}"
    podman volume create "telos_$name" >/dev/null
    podman volume import "telos_$name" "$data/${pair#*:}"
  done
  journal_event "$step" done

  step="start databases"
  journal_event "$step" begin
  stack up -d postgres grimmory-db >/dev/null
  local ready=0
  while [ "$ready" -lt 120 ]; do
    if podman exec telos-postgres sh -c 'pg_isready --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' >/dev/null 2>&1 \
       && podman exec telos-grimmory-db healthcheck.sh --connect --innodb_initialized >/dev/null 2>&1; then break; fi
    ready=$((ready + 1)); sleep 1
  done
  [ "$ready" -lt 120 ] || die "the databases did not become ready within 120 seconds"
  journal_event "$step" done

  step="postgres restore"
  journal_event "$step" begin
  podman exec -i telos-postgres sh -c 'exec pg_restore --clean --if-exists --no-owner --role=telos_owner --exit-on-error --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' <"$data/postgres.dump"
  journal_event "$step" done

  step="mariadb restore"
  journal_event "$step" begin
  podman exec -i telos-grimmory-db sh -c 'exec mariadb --user=root --password="$MYSQL_ROOT_PASSWORD"' <"$data/grimmory.sql"
  journal_event "$step" done

  step="migrations"
  journal_event "$step" begin
  # Images the host already holds (loaded from the release's artifacts) are used as they
  # are; only a missing one is built from the installed release.
  local missing_build=() service
  for service in telos-migrate telos-sanitize-restore; do
    podman image exists "localhost/telos_$service" 2>/dev/null || missing_build+=("$service")
  done
  [ "${#missing_build[@]}" -eq 0 ] || stack --profile restore build "${missing_build[@]}" >/dev/null
  stack run --rm --no-deps -T telos-migrate >/dev/null
  journal_event "$step" done

  step="sanitize"
  journal_event "$step" begin
  stack --profile restore run --rm --no-deps -T telos-sanitize-restore ./telos-core sanitize-restore --snapshot "$snapshot"
  journal_event "$step" done

  step="start stack"
  journal_event "$step" begin
  local build_flag=(--build)
  local image all_present=1
  for image in telos-core telos-client telos-egress-proxy; do
    podman image exists "localhost/telos_$image" 2>/dev/null || all_present=0
  done
  [ "$all_present" -eq 0 ] || build_flag=()
  telos_cli start "${build_flag[@]}"
  local wait=0
  until gateway_ready; do
    wait=$((wait + 1)); [ "$wait" -lt 60 ] || die "the restored gateway did not report ready within 5 minutes"
    sleep 5
  done
  journal_event "$step" done

  step="result"
  local manifest_digest; manifest_digest="$(sha256sum "$data/manifest.json" | cut -d' ' -f1)"
  python3 -I - "$result_out" "$snapshot" "$manifest_digest" "$data/manifest.json" "$target_id" "$rehearsal" <<'PY'
import json, os, sys, datetime
out, snapshot, digest, manifest_path, target, rehearsal = sys.argv[1:7]
manifest = json.load(open(manifest_path))
record = {
    "status": "restored",
    "mode": "separate-host",
    "recordKind": "rehearsal" if rehearsal == "1" else "candidate",
    "snapshot": snapshot,
    "manifestSha256": digest,
    "sourceHostIdSha256": manifest["source"]["hostIdSha256"],
    "targetHostIdSha256": target,
    "candidateLockDigest": manifest["source"]["candidateLockDigest"],
    "consistencyPointAt": manifest["consistencyPointAt"],
    "restoredAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
}
tmp = out + ".tmp"
with open(os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as handle:
    json.dump(record, handle, indent=1, sort_keys=True)
os.replace(tmp, out)
PY
  journal_event "$step" done
  log "restored snapshot ${snapshot:0:8} from $(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["consistencyPointAt"])' "$data/manifest.json"); sanitized and serving"
}

with_maintenance_lock run_restore
