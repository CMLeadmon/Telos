#!/usr/bin/env bash
set -euo pipefail

# Encrypted off-node backup, driven through fake podman and restic so every failure point
# can be injected without a node: a failure at ANY step must exit nonzero at that step,
# leave no plaintext staging directory, restart every service the run stopped, run no
# later restic/prune step, and print no secret; a success must leave a manifest in the
# "repository" that equals the staged one. The signal cases use a stub that blocks.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

work="$(mktemp -d)"
cleanup_work() { jobs -p | xargs -r kill -KILL 2>/dev/null || true; rm -rf "$work"; }
trap cleanup_work EXIT
fail() { echo "backup_restore_test: $*" >&2; exit 1; }

bin="$work/bin"; mkdir -p "$bin"
CANARY_PASSWORD="canary-password-$$-do-not-print"
CANARY_SECRET="canary-credential-$$-do-not-print"

# ------------------------------------------------------------------ fake podman
cat >"$bin/podman" <<'SH'
#!/usr/bin/env bash
# Records argv; models container state in $STUB_STATE/running/<name>.
set -u
state="$STUB_STATE"; log="$state/podman.log"
printf '%s\n' "$*" >>"$log"
fail_at="${FAIL_AT:-}"
case "$1" in
  container)  # container exists NAME
    [[ "$fail_at" == validation && "$3" == telos-grimmory ]] && exit 1
    exit 0 ;;
  inspect)    # inspect --type container --format ... NAME
    name="${!#}"; [[ -e "$state/running/$name" ]] && echo true || echo false; exit 0 ;;
  stop)
    [[ "$fail_at" == quiesce ]] && exit 1
    rm -f "$state/running/${!#}"; exit 0 ;;
  start)
    [[ "$fail_at" == restart ]] && exit 1
    touch "$state/running/${!#}"; exit 0 ;;
  exec)
    shift; input=""; [[ "$1" == -i ]] && { shift; input="$(cat)"; }
    name="$1"; shift; joined="$*"
    if [[ "$joined" == *pg_dump* ]]; then [[ "$fail_at" == pgdump ]] && exit 1; printf 'PGDMP-binary-dump'; exit 0; fi
    if [[ "$joined" == *psql* ]]; then
      # The query arrives on stdin; an empty read means the script dropped it.
      [[ "$input" == *schema_migrations* ]] || exit 1
      printf '[{"version":1,"name":"0001_init.sql","checksum":"%064d"},{"version":2,"name":"0002_x.sql","checksum":"%064d"}]\n' 1 2; exit 0; fi
    if [[ "$joined" == *mariadb-dump* ]]; then [[ "$fail_at" == mariadb ]] && exit 1; printf -- '-- mariadb dump\n'; exit 0; fi
    exit 0 ;;
  volume)
    [[ "$fail_at" == volume ]] && exit 1
    out=""; prev=""; for a in "$@"; do [[ "$prev" == --output ]] && out="$a"; prev="$a"; done
    tmp="$(mktemp -d)"; echo "volume $2 data" >"$tmp/file"; tar -C "$tmp" -cf "$out" .; rm -rf "$tmp"; exit 0 ;;
esac
exit 0
SH

# ------------------------------------------------------------------ fake restic
cat >"$bin/restic" <<'SH'
#!/usr/bin/env bash
set -u
state="$STUB_STATE"; log="$state/restic.log"; repo="$state/repo"; mkdir -p "$repo"
printf '%s\n' "$*" >>"$log"
fail_at="${FAIL_AT:-}"
case "$1" in
  cat) [[ "$fail_at" == repo ]] && exit 1; exit 0 ;;
  backup)
    [[ "$fail_at" == upload ]] && exit 1
    stage="$2"; id="$(printf '%s' "$stage-$RANDOM" | sha256sum | cut -d' ' -f1)"
    mkdir -p "$repo/$id"; cp -a "$stage/." "$repo/$id/"; printf '%s\n' "$stage" >"$repo/$id.stage"
    tags=(); prev=""; for a in "$@"; do [[ "$prev" == --tag ]] && tags+=("$a"); prev="$a"; done
    printf '%s\n' "${tags[@]}" >"$repo/$id.tags"
    if [[ "${BLOCK_AT:-}" == upload ]]; then
      echo "BLOCKED" >>"$log"
      trap 'echo "stub got TERM" >>"$log"; exit 143' TERM
      while :; do sleep 0.2; done
    fi
    printf '{"message_type":"status","percent_done":0.5}\n{"message_type":"summary","snapshot_id":"%s"}\n' "$id"
    exit 0 ;;
  snapshots)
    id="${!#}"; [[ -d "$repo/$id" ]] || { echo '[]'; exit 0; }
    python3 - "$id" "$repo/$id.tags" <<'PY'
import json, sys
tags = [l.strip() for l in open(sys.argv[2]) if l.strip()]
print(json.dumps([{"id": sys.argv[1], "short_id": sys.argv[1][:8], "tags": tags, "hostname": "telos-node"}]))
PY
    exit 0 ;;
  ls)
    id="$3"; stage="$(cat "$repo/$id.stage")"
    for f in "$repo/$id"/*; do printf '{"path":"%s/%s","type":"file"}\n' "$stage" "$(basename "$f")"; done; exit 0 ;;
  dump)
    id="$2"
    if [[ "$fail_at" == manifest-mismatch ]]; then sed 's/"release": "[^"]*"/"release": "tampered"/' "$repo/$id/manifest.json"; else cat "$repo/$id/manifest.json"; fi
    exit 0 ;;
  check) [[ "$fail_at" == check ]] && exit 1; exit 0 ;;
  forget) [[ "$fail_at" == prune ]] && exit 1; exit 0 ;;
esac
exit 0
SH
# systemd-run --scope [options] PROGRAM ARGS: records that a scope was used, then runs the program.
cat >"$bin/systemd-run" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_STATE/scope.log"
while [[ "${1:-}" == --* ]]; do shift; done
exec "$@"
SH
chmod +x "$bin/podman" "$bin/restic" "$bin/systemd-run"

# ------------------------------------------------------------------ the node
make_node() {  # prints the node directory
  local n="$work/node-$1"
  mkdir -p "$n/config" "$n/state" "$n/ops" "$work/storage-$1/books" "$work/release-$1" "$work/tmp-$1" "$work/stub-$1/running"
  echo "cursor-key-material" >"$n/config/cursor-keys.json"
  printf "STORAGE_PATH=%s\nPOSTGRES_PASSWORD=%s\n" "$work/storage-$1" "$CANARY_SECRET" >"$n/node.env"
  echo "a book" >"$work/storage-$1/books/book.epub"
  echo "release file" >"$work/release-$1/README"
  # A release root as install.sh leaves it, plus build output that must never be archived.
  mkdir -p "$work/release-$1/config" "$work/release-$1/frontend/src-tauri/target/debug" "$work/release-$1/telos-review"
  cp config/release-exclude.txt "$work/release-$1/config/release-exclude.txt"
  head -c 1048576 /dev/zero >"$work/release-$1/frontend/src-tauri/target/debug/huge.bin"
  echo "review" >"$work/release-$1/telos-review/index.html"
  chmod 0700 "$n/state"
  for c in telos-traefik telos-core telos-client telos-postgres telos-grimmory-db telos-jellyfin telos-grimmory; do touch "$work/stub-$1/running/$c"; done
  printf '%s' "$CANARY_PASSWORD" >"$work/pw-$1"; chmod 0600 "$work/pw-$1"
  printf 'AWS_SECRET_ACCESS_KEY=%s\n' "$CANARY_SECRET" >"$work/cred-$1"; chmod 0600 "$work/cred-$1"
  printf '%s' "$n"
}

run_backup() {  # CASE [VAR=value ...] -> runs the script; output in $work/out-CASE; returns its status
  local case_id="$1"; shift
  env -i HOME="$work" PATH="$bin:/usr/bin:/bin" \
    STUB_STATE="$work/stub-$case_id" TELOS_NODE_PATH="$work/node-$case_id" TELOS_BACKUP_TMP_ROOT="$work/tmp-$case_id" \
    TELOS_RELEASE_ROOT="$work/release-$case_id" TELOS_BACKUP_REQUIRE_ROOT=0 TELOS_START_IN_SCOPE=1 \
    RESTIC_REPOSITORY="sftp:backup@offsite.example:/srv/telos" RESTIC_PASSWORD_FILE="$work/pw-$case_id" \
    RESTIC_CREDENTIALS_FILE="$work/cred-$case_id" RESTIC_CACHE_DIR="$work/cache" "$@" \
    bash scripts/backup.sh >"$work/out-$case_id" 2>&1
}

assert_clean() {  # CASE [norestart] -> invariants that hold after EVERY run
  local c="$1" check_restart="${2:-yes}"
  [[ -z "$(find "$work/tmp-$c" -mindepth 1 -maxdepth 1 -name 'telos-backup.*' 2>/dev/null)" ]] || fail "$c: a plaintext staging directory survived"
  [[ ! -e "$work/node-$c/state/backup-stage.journal" ]] || fail "$c: the stage journal survived"
  for w in telos-traefik telos-core telos-jellyfin telos-grimmory; do
    if [[ "$check_restart" == yes ]]; then
      [[ -e "$work/stub-$c/running/$w" ]] || fail "$c: $w was stopped by the run and not restarted"
    else
      # The runtime refuses every start (that is the injected fault): the run must have kept
      # trying for each writer rather than giving up after the first.
      grep -q "^start $w" "$work/stub-$c/podman.log" || fail "$c: no attempt was made to restart $w"
    fi
  done
  ! grep -rqF -e "$CANARY_PASSWORD" -e "$CANARY_SECRET" "$work/out-$c" "$work/stub-$c"/*.log 2>/dev/null || fail "$c: a secret reached output or a command line"
}

# --------------------------------------------------------------- the success case
make_node ok >/dev/null
run_backup ok || fail "the success case failed: $(tail -5 "$work/out-ok")"
assert_clean ok
grep -q 'verified snapshot' "$work/out-ok" || fail "success did not report a verified snapshot"
ids=("$work/stub-ok/repo"/*/); [[ ${#ids[@]} -eq 1 ]] || fail "expected one snapshot, found ${#ids[@]}"
snap="${ids[0]%/}"
python3 -I scripts/lib/backup_manifest.py verify --manifest "$snap/manifest.json" || fail "the stored manifest is invalid"
for file in postgres.dump grimmory.sql node.env node-config.tar traefik-acme.tar jellyfin-config.tar grimmory-config.tar shared-storage.tar release-capsule.tar migrations.json manifest.json; do
  [[ -f "$snap/$file" ]] || fail "snapshot lacks $file"
done
grep -qx 'scheduled' "$snap.tags" && grep -qx 'telos' "$snap.tags" && grep -q '^release:' "$snap.tags" && grep -q '^candidate:' "$snap.tags" || fail "snapshot tags are wrong: $(cat "$snap.tags")"
# Restarted services run in a scope of their own: conmon left in the one-shot unit's cgroup
# would be killed, and the services with it, when the unit ends.
grep -q 'podman start telos-traefik' "$work/stub-ok/scope.log" && grep -q 'podman start telos-core' "$work/stub-ok/scope.log" \
  || fail "restarts did not go through a transient scope: $(cat "$work/stub-ok/scope.log" 2>/dev/null)"
# Order matters: writers are restarted BEFORE the upload begins.
restart_line="$(grep -n '^start telos-traefik' "$work/stub-ok/podman.log" | head -1 | cut -d: -f1)"
[[ -n "$restart_line" ]] || fail "writers were never restarted"
python3 - "$work/stub-ok/podman.log" "$work/stub-ok/restic.log" <<'PY'
import sys
podman = open(sys.argv[1]).read().splitlines()
restic = open(sys.argv[2]).read().splitlines()
verbs = [line.split()[0] for line in restic]
assert verbs == ["cat", "backup", "snapshots", "ls", "dump", "check", "forget"], verbs
assert any(l.startswith("backup ") and "--tag scheduled" in l for l in restic)
assert "--read-data-subset=5%" in restic[verbs.index("check")]
forget = restic[verbs.index("forget")]
assert "--keep-daily 14" in forget and "--keep-weekly 8" in forget and "--prune" in forget, forget
PY
# The release capsule carries the release, not what was built beside it.
capsule_members="$(tar -tf "$snap/release-capsule.tar")"
grep -qx './README' <<<"$capsule_members" && grep -qx './config/release-exclude.txt' <<<"$capsule_members" || fail "the release capsule lacks the release files"
! grep -qE 'src-tauri/target|telos-review' <<<"$capsule_members" || fail "build output leaked into the release capsule"
status_file="$work/node-ok/ops/last-backup.prom"
grep -q '^telos_backup_last_result 1$' "$status_file" || fail "success status not recorded: $(cat "$status_file")"
grep -Eq '^telos_backup_last_success_timestamp_seconds [1-9][0-9]+$' "$status_file" || fail "last-success time not recorded"
[[ "$(stat -c %a "$status_file")" == 640 ]] || fail "status file mode is $(stat -c %a "$status_file")"

# --------------------------------------------------------------- the failure matrix
# step name (as the script reports it) | FAIL_AT value | restic verbs that must NOT have run
declare -a MATRIX=(
  "container validation|validation|cat backup snapshots ls dump check forget"
  "repository check|repo|backup snapshots ls dump check forget"
  "quiesce|quiesce|backup snapshots ls dump check forget"
  "postgres dump|pgdump|backup snapshots ls dump check forget"
  "mariadb dump|mariadb|backup snapshots ls dump check forget"
  "volume export|volume|backup snapshots ls dump check forget"
  "restart writers|restart|backup snapshots ls dump check forget"
  "upload|upload|snapshots ls dump check forget"
  "snapshot verification|manifest-mismatch|check forget"
  "repository check (5% of data)|check|forget"
  "retention|prune|"
  "release capsule|capsule|backup snapshots ls dump check forget"
)
for row in "${MATRIX[@]}"; do
  IFS='|' read -r step injected forbidden <<<"$row"
  id="fail-$injected"
  make_node "$id" >/dev/null
  extra_env=(FAIL_AT="$injected")
  [[ "$injected" != capsule ]] || extra_env+=(TELOS_CAPSULE_MAX_BYTES=100)   # a capsule that large means build output leaked in
  if run_backup "$id" "${extra_env[@]}"; then fail "$id: injected failure '$injected' still exited zero"; fi
  if [[ "$injected" == restart ]]; then assert_clean "$id" norestart; else assert_clean "$id"; fi
  grep -qF "FAILED at step '$step'" "$work/out-$id" || fail "$id: expected a failure at '$step', got: $(tail -3 "$work/out-$id")"
  for verb in $forbidden; do
    ! grep -q "^$verb " "$work/stub-$id/restic.log" 2>/dev/null || fail "$id: restic '$verb' ran after the failure at '$step'"
  done
  if [[ "$injected" == manifest-mismatch || "$injected" == check || "$injected" == prune ]]; then
    grep -q 'is NOT verified' "$work/out-$id" || fail "$id: a snapshot that exists was not flagged as unverified"
  fi
  ! grep -q 'verified snapshot' "$work/out-$id" || fail "$id: a failed run called a snapshot verified"
  # The outcome is recorded for the collector; a failed run never erases the last success.
  grep -q '^telos_backup_last_result 0$' "$work/node-$id/ops/last-backup.prom" || fail "$id: failure was not recorded"
done

# A failure after a success keeps the earlier last-success time.
make_node keep >/dev/null
run_backup keep || fail "keep: first run failed"
before="$(sed -n 's/^telos_backup_last_success_timestamp_seconds //p' "$work/node-keep/ops/last-backup.prom")"
rm -rf "$work/stub-keep/repo"; sleep 1
run_backup keep FAIL_AT=pgdump && fail "keep: second run should have failed"
after="$(sed -n 's/^telos_backup_last_success_timestamp_seconds //p' "$work/node-keep/ops/last-backup.prom")"
[[ "$before" == "$after" ]] || fail "a failed run erased the last-success time ($before -> $after)"

# --------------------------------------------------------------- configuration refusals
make_node cfg >/dev/null
run_backup cfg RESTIC_REPOSITORY="$work/local-repo" && fail "a local repository path was accepted"
grep -q 'off-node' "$work/out-cfg" || fail "the local-repository refusal gave no reason"
[[ ! -s "$work/stub-cfg/podman.log" ]] || fail "podman was touched before the configuration was accepted"
chmod 0644 "$work/pw-cfg"
run_backup cfg && fail "a world-readable password file was accepted"
grep -q 'mode 0600' "$work/out-cfg" || fail "no reason given for the password file"

# --------------------------------------------------------------- maintenance lock contention
make_node busy >/dev/null
( exec 9>"$work/node-busy/state/maintenance.lock"; flock -n 9; sleep 4 ) &
holder=$!
sleep 0.5
set +e; run_backup busy; code=$?; set -e
[[ $code -eq 75 ]] || fail "contention exited $code, expected 75"
[[ ! -s "$work/stub-busy/podman.log" ]] || fail "runtime state changed before the lock was acquired"
wait "$holder" 2>/dev/null || true

# --------------------------------------------------------------- signals and SIGKILL
for sig in TERM INT; do
  id="sig-$sig"; make_node "$id" >/dev/null
  expected=$([[ $sig == TERM ]] && echo 143 || echo 130)
  # A background job in a non-interactive shell starts with SIGINT ignored, and an ignored
  # signal cannot be trapped, so the helper restores the default first, as a terminal would.
  # `exec` keeps one process, so the pid written first is the script's own.
  (
    exec env -i HOME="$work" PATH="$bin:/usr/bin:/bin" STUB_STATE="$work/stub-$id" TELOS_NODE_PATH="$work/node-$id" \
      TELOS_BACKUP_TMP_ROOT="$work/tmp-$id" TELOS_RELEASE_ROOT="$work/release-$id" TELOS_BACKUP_REQUIRE_ROOT=0 BLOCK_AT=upload \
      RESTIC_REPOSITORY="sftp:backup@offsite.example:/srv/telos" RESTIC_PASSWORD_FILE="$work/pw-$id" RESTIC_CACHE_DIR="$work/cache" \
      python3 -I -c 'import os, signal, sys; signal.signal(signal.SIGINT, signal.SIG_DFL); os.execvp("bash", ["bash", "-c", "echo $$ >\"$0\"; exec bash scripts/backup.sh", sys.argv[1]])' "$work/pid-$id" >"$work/out-$id" 2>&1
  ) &
  runner=$!
  for _ in $(seq 1 100); do grep -q BLOCKED "$work/stub-$id/restic.log" 2>/dev/null && break; sleep 0.1; done
  grep -q BLOCKED "$work/stub-$id/restic.log" || fail "$id: the run never reached the upload"
  kill -"$sig" "$(cat "$work/pid-$id")"
  set +e; wait "$runner"; code=$?; set -e
  [[ $code -eq $expected ]] || fail "$id: exit $code after SIG$sig, expected $expected"
  assert_clean "$id"
  ! grep -q '^forget ' "$work/stub-$id/restic.log" || fail "$id: retention ran after an interrupt"
done

# SIGKILL cannot be trapped: the NEXT run removes the journaled stage and restarts writers.
id=kill; make_node "$id" >/dev/null
(
  exec env -i HOME="$work" PATH="$bin:/usr/bin:/bin" STUB_STATE="$work/stub-$id" TELOS_NODE_PATH="$work/node-$id" \
    TELOS_BACKUP_TMP_ROOT="$work/tmp-$id" TELOS_RELEASE_ROOT="$work/release-$id" TELOS_BACKUP_REQUIRE_ROOT=0 BLOCK_AT=upload \
    RESTIC_REPOSITORY="sftp:backup@offsite.example:/srv/telos" RESTIC_PASSWORD_FILE="$work/pw-$id" RESTIC_CACHE_DIR="$work/cache" \
    setsid bash -c 'echo $$ >"$0"; exec bash scripts/backup.sh' "$work/pid-$id" >"$work/out-$id" 2>&1
) &
runner=$!
for _ in $(seq 1 100); do grep -q BLOCKED "$work/stub-$id/restic.log" 2>/dev/null && break; sleep 0.1; done
grep -q BLOCKED "$work/stub-$id/restic.log" || fail "$id: the run never reached the upload"
# Kill the whole session (the script and its blocked restic stub): nothing can clean up.
pgid="$(ps -o pgid= -p "$(cat "$work/pid-$id")" | tr -d ' ')"
kill -KILL -- "-$pgid" 2>/dev/null || true
wait "$runner" 2>/dev/null || true
[[ -n "$(find "$work/tmp-$id" -mindepth 1 -maxdepth 1 -name 'telos-backup.*')" ]] || fail "$id: expected an orphaned stage after SIGKILL"
[[ -f "$work/node-$id/state/backup-stage.journal" ]] || fail "$id: the orphaned stage was not journaled"
rm -rf "$work/stub-$id/repo"
run_backup "$id" || fail "$id: the run after SIGKILL failed: $(tail -3 "$work/out-$id")"
grep -q 'removed the staging directory an earlier run left behind' "$work/out-$id" || fail "$id: the orphan was not reported removed"
assert_clean "$id"

# A hostile journal can never make the script delete something that is not its stage.
id=hostile; make_node "$id" >/dev/null
mkdir -p "$work/precious"; echo keep >"$work/precious/file"
printf '%s\n' "$work/precious" >"$work/node-$id/state/backup-stage.journal"
run_backup "$id" && fail "$id: a journal naming a foreign directory was accepted"
[[ -f "$work/precious/file" ]] || fail "$id: the script deleted a directory outside its staging root"
ln -s "$work/precious" "$work/tmp-$id/telos-backup.linkAAA"
printf '%s\n' "$work/tmp-$id/telos-backup.linkAAA" >"$work/node-$id/state/backup-stage.journal"
run_backup "$id" && fail "$id: a symlink named by the journal was followed"
[[ -f "$work/precious/file" ]] || fail "$id: a symlinked stage deleted its target"

echo "backup: failure matrix, tags, manifest round trip, signals, SIGKILL recovery, lock contention and secret hygiene verified"
