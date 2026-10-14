#!/usr/bin/env bash
set -euo pipefail

# The upgrade/rollback state machine against stub generations: a temporary release root holds real
# generation directories (verified by generations.py) whose `telos`, `scripts/backup.sh` and a fake
# `podman` are stubs that record calls and obey flag files. Nothing here is evidence about a real
# node; it proves ordering, refusals, journaling and recovery.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "upgrade_rollback_test: FAIL $*" >&2; exit 1; }

OLD_SCHEMA=22 NEW_SCHEMA=24
export TELOS_LIFECYCLE_REQUIRE_ROOT=0 TELOS_GENERATION_OWNER_CHECK=0 TELOS_LIFECYCLE_READY_SECONDS=1 TELOS_LIFECYCLE_POLL_SECONDS=0

mkdir -p "$work/bin"
cat >"$work/bin/podman" <<'SH'
#!/usr/bin/env bash
echo "podman $*" >>"$STUB_STATE/calls.log"
if [[ "$1" == stop ]]; then
  [[ "${CRASH_AT:-}" == stop-writers ]] && { kill -KILL "$PPID"; exit 137; }
  rm -f "$STUB_STATE"/running-*
fi
exit 0
SH
chmod +x "$work/bin/podman"

# make_generation SANDBOX NAME target_schema compat min predecessor_hex... -> prints the generation id
make_generation() {
  local sb="$1" name="$2" target="$3" compat="$4" lo="$5"; shift 5
  local lock="$sb/locks/$name.lock" id dir
  mkdir -p "$sb/locks"
  printf 'candidate lock of %s in %s\n' "$name" "$sb" >"$lock"
  id="$(sha256sum "$lock" | cut -d' ' -f1)"
  dir="$sb/srv/generations/$id"
  mkdir -p "$dir/scripts"
  printf 'CORE=sha256:%064d\n' 5 >"$dir/images.env"
  cat >"$dir/telos" <<'SH'
#!/usr/bin/env bash
id8="$(basename "$(dirname "$0")")"; id8="${id8:0:8}"
echo "$id8 $*" >>"$STUB_STATE/calls.log"
crash() { if [[ "${CRASH_AT:-}" == "$1" ]]; then kill -KILL "$PPID"; exit 137; fi; return 0; }
case "$1 ${2:-}" in
  "migrate --status") [[ -e "$STUB_STATE/db-down" ]] && exit 1; cat "$STUB_STATE/schema" ;;
  "migrate ")
    crash migrate-before
    if [[ -e "$STUB_STATE/fail-migrate-$id8" ]]; then
      [[ -s "$STUB_STATE/fail-migrate-$id8" ]] && cp "$STUB_STATE/fail-migrate-$id8" "$STUB_STATE/schema"
      exit 1
    fi
    cat "$(dirname "$0")/target-schema" >"$STUB_STATE/schema"
    crash migrate-after ;;
  "stop "*) crash target-stop; rm -f "$STUB_STATE"/running-* ;;
  "start "*)
    crash target-start
    [[ -e "$STUB_STATE/start-fails-$id8" ]] && exit 1
    touch "$STUB_STATE/running-$id8" ;;
  "status --json")
    if [[ -e "$STUB_STATE/running-$id8" && ! -e "$STUB_STATE/unhealthy-$id8" ]]; then echo '{"runtime":"podman","containers":[],"gateway":{"status":"ok"}}'; else echo '{"runtime":"podman","containers":[],"gateway":null}'; exit 1; fi ;;
  *) exit 2 ;;
esac
SH
  cat >"$dir/scripts/backup.sh" <<'SH'
#!/usr/bin/env bash
echo "backup $* repo=${RESTIC_REPOSITORY:-unset}" >>"$STUB_STATE/calls.log"
# The real backup.sh adopts the maintenance lock its caller holds (TELOS_MAINTENANCE_LOCK_CLAIM) and checks that the
# lock's file descriptor really arrived; a caller that closed it makes the backup refuse (found by the live rehearsal).
if [[ -n "${TELOS_MAINTENANCE_LOCK_CLAIM:-}" && ! -e /proc/self/fd/9 ]]; then echo "stub backup: the inherited maintenance lock is gone" >&2; exit 77; fi
[[ "${CRASH_AT:-}" == backup ]] && { kill -KILL "$PPID"; exit 137; }
[[ -e "$STUB_STATE/fail-backup" ]] && exit 1
result=""
while [[ $# -gt 0 ]]; do [[ "$1" == --result-out ]] && result="$2"; shift; done
printf '{"status":"passed","snapshot":"abcdef0123456789"}\n' >"$result"
SH
  chmod +x "$dir/telos" "$dir/scripts/backup.sh"
  echo "$target" >"$dir/target-schema"
  python3 -I - "$dir" "$target" "$compat" "$lo" "$@" <<'PY'
import hashlib, os, sys
sys.path.insert(0, "scripts/lib")
import release_graph as rg
directory, target, compat, lo, *predecessors = sys.argv[1:]
data = open(os.path.join(directory, "images.env"), "rb").read()
manifest = {"schemaVersion": 1, "sourceCommit": "4" * 40, "version": "0.1.0-beta.1", "images": {"core": "sha256:" + "5" * 64},
            "migrations": {"min": int(lo), "current": int(target), "max": int(target), "checksumSetDigest": "sha256:" + "6" * 64},
            "predecessorIds": sorted("sha256:" + p for p in predecessors), "rollbackCompatibleThrough": int(compat),
            "artifactIndexSubset": [{"path": "images.env", "sha256": hashlib.sha256(data).hexdigest()}]}
with open(os.path.join(directory, "generation.json"), "wb") as handle:
    handle.write(rg.canonical_bytes(manifest))
for base, dirs, files in os.walk(directory):
    os.chmod(base, 0o755)
    for name in files:
        path = os.path.join(base, name)
        os.chmod(path, 0o755 if os.access(path, os.X_OK) else 0o644)
PY
  echo "$id"
}

# sandbox NAME [compat_through] [predecessor_listed(yes|no)] -> OLD and NEW ids in $OLD/$NEW, node ready on OLD
sandbox() {
  SB="$work/$1"; local compat="${2:-24}" listed="${3:-yes}"
  rm -rf "$SB"; mkdir -p "$SB/srv/generations" "$SB/node/state" "$SB/state-stub"
  chmod 0700 "$SB/node/state"
  export STUB_STATE="$SB/state-stub" TELOS_RELEASE_BASE="$SB/srv" TELOS_NODE_PATH="$SB/node" TELOS_MACHINE_ID_FILE="$SB/machine-id"
  echo "machine-$1" >"$SB/machine-id"
  printf 'RESTIC_REPOSITORY=sftp:test@vault.invalid:/repo\n' >"$SB/backup.env"; chmod 0600 "$SB/backup.env"
  OLD="$(make_generation "$SB" old "$OLD_SCHEMA" "$OLD_SCHEMA" 1)"
  if [[ "$listed" == yes ]]; then NEW="$(make_generation "$SB" new "$NEW_SCHEMA" "$compat" 10 "$OLD")"; else NEW="$(make_generation "$SB" new "$NEW_SCHEMA" "$compat" 10)"; fi
  echo "$OLD_SCHEMA" >"$STUB_STATE/schema"
  touch "$STUB_STATE/running-${OLD:0:8}"
  python3 -I scripts/lib/generations.py activate --root "$SB/srv" --id "$OLD" >/dev/null
  python3 -I scripts/lib/generations.py receipt --root "$SB/srv" --node "$SB/node" --id "$OLD" --no-owner-check >/dev/null
  : >"$STUB_STATE/calls.log"
}

generation_arg() { if [[ "$1" == /* ]]; then printf '%s' "$1"; else printf '%s/generations/%s' "$TELOS_RELEASE_BASE" "$1"; fi; }
upgrade() {   # runs the real script; output in $SB/out; returns its status
  env -i HOME="$SB" PATH="$work/bin:/usr/bin:/bin" STUB_STATE="$STUB_STATE" TELOS_RELEASE_BASE="$TELOS_RELEASE_BASE" TELOS_NODE_PATH="$TELOS_NODE_PATH" \
    TELOS_MACHINE_ID_FILE="$TELOS_MACHINE_ID_FILE" TELOS_LIFECYCLE_REQUIRE_ROOT=0 TELOS_GENERATION_OWNER_CHECK=0 TELOS_LIFECYCLE_READY_SECONDS=1 \
    TELOS_LIFECYCLE_POLL_SECONDS=0 CRASH_AT="${CRASH_AT:-}" TELOS_BACKUP_ENV="$SB/backup.env" \
    bash scripts/upgrade.sh --generation "$(generation_arg "${1:-$NEW}")" --candidate-lock "$SB/locks/${2:-new}.lock" \
      --current-candidate-lock "$SB/locks/${3:-old}.lock" --result-out "$SB/result.json" --confirm UPGRADE-THIS-NODE >"$SB/out" 2>&1
}
rollback() {
  env -i HOME="$SB" PATH="$work/bin:/usr/bin:/bin" STUB_STATE="$STUB_STATE" TELOS_RELEASE_BASE="$TELOS_RELEASE_BASE" TELOS_NODE_PATH="$TELOS_NODE_PATH" \
    TELOS_MACHINE_ID_FILE="$TELOS_MACHINE_ID_FILE" TELOS_LIFECYCLE_REQUIRE_ROOT=0 TELOS_GENERATION_OWNER_CHECK=0 TELOS_LIFECYCLE_READY_SECONDS=1 \
    TELOS_LIFECYCLE_POLL_SECONDS=0 \
    bash scripts/rollback.sh --operation-id "$1" --result-out "$SB/rollback-result.json" --confirm ROLLBACK-THIS-NODE >"$SB/rollback-out" 2>&1
}

journal_field() { python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$TELOS_NODE_PATH/state/operation.json" "$1"; }
current_id() { basename "$(readlink "$TELOS_RELEASE_BASE/current")"; }
calls() { cat "$STUB_STATE/calls.log"; }
expect_state() { [[ "$(journal_field state)" == "$1" ]] || fail "$CASE: journal state is $(journal_field state), expected $1 ($(tail -3 "$SB/out" | tr '\n' ' '))"; }
expect_active() { [[ "$(current_id)" == "$1" ]] || fail "$CASE: active generation is $(current_id | cut -c1-8), expected ${1:0:8}"; }
expect_serving() { [[ -e "$STUB_STATE/running-${1:0:8}" && "$(ls "$STUB_STATE" | grep -c '^running-')" == 1 ]] || fail "$CASE: expected only ${1:0:8} to be running: $(ls "$STUB_STATE" | grep running- | tr '\n' ' ')"; }
expect_no() { ! grep -q -- "$1" "$STUB_STATE/calls.log" || fail "$CASE: unexpected call matching '$1': $(grep -- "$1" "$STUB_STATE/calls.log" | head -2 | tr '\n' ' ')"; }
expect_calls_in_order() {   # patterns...
  local last=0 pattern line
  for pattern in "$@"; do
    line="$(grep -n -m1 -- "$pattern" "$STUB_STATE/calls.log" | cut -d: -f1 || true)"
    [[ -n "$line" ]] || fail "$CASE: no call matching '$pattern': $(calls | tr '\n' '|')"
    [[ "$line" -gt "$last" ]] || fail "$CASE: '$pattern' ran out of order: $(calls | tr '\n' '|')"
    last="$line"
  done
}

# ------------------------------------------------------------------------ success
CASE=success; sandbox success
upgrade || fail "$CASE: $(tail -5 "$SB/out")"
expect_state complete; expect_active "$NEW"; expect_serving "$NEW"
[[ "$(journal_field snapshotId)" == abcdef0123456789 && "$(journal_field postSchema)" == "$NEW_SCHEMA" && "$(journal_field preSchema)" == "$OLD_SCHEMA" ]] || fail "$CASE: journal facts wrong"
# The order is the safety argument: snapshot, writers down, migrate, then stop-and-start on the target.
grep -q "backup --candidate-lock .* repo=sftp:test@vault.invalid:/repo" "$STUB_STATE/calls.log" || fail "$CASE: the backup did not receive the node's repository environment"
expect_calls_in_order "backup --candidate-lock" "podman stop" "${NEW:0:8} migrate$" "${NEW:0:8} stop" "${NEW:0:8} start"
python3 -I scripts/lib/generations.py check-receipt --root "$TELOS_RELEASE_BASE" --node "$TELOS_NODE_PATH" --id "$NEW" --no-owner-check >/dev/null || fail "$CASE: the receipt does not name the new generation"
python3 -I -c 'import json,sys; r=json.load(open(sys.argv[1])); assert r["status"]=="complete" and r["snapshotId"] and r["postSchema"]==24' "$SB/result.json" || fail "$CASE: result record"
[[ -d "$TELOS_RELEASE_BASE/generations/$OLD" ]] || fail "$CASE: the previous generation was removed"
[[ "$(stat -c %a "$TELOS_NODE_PATH/state/operation.json")" == 600 ]] || fail "$CASE: journal mode"

# load_backup_env trusts the file like an EnvironmentFile: root-owned and 0600, else it refuses
bad_env="$work/bad-backup.env"; printf 'RESTIC_REPOSITORY=sftp:x@y:/r\n' >"$bad_env"; chmod 0644 "$bad_env"
if env -i PATH="/usr/bin:/bin" TELOS_BACKUP_ENV="$bad_env" bash -c '. scripts/lib/maintenance.sh; load_backup_env' 2>/dev/null; then fail "load_backup_env accepted a file that is not root-owned mode 0600"; fi
env -i PATH="/usr/bin:/bin" TELOS_BACKUP_ENV="$work/no-such.env" bash -c '. scripts/lib/maintenance.sh; load_backup_env' || fail "a missing backup.env must be left for backup.sh to refuse, not fail here"

# ------------------------------------------------------------------------ refusals change nothing
refuses() {   # CASE message-pattern [upgrade args...]
  local pattern="$1"; shift
  if upgrade "$@"; then fail "$CASE: the upgrade succeeded but should have been refused"; fi
  grep -q -- "$pattern" "$SB/out" || fail "$CASE: expected '$pattern', got: $(tail -3 "$SB/out")"
  expect_active "$OLD"; expect_serving "$OLD"; expect_no "podman stop"; expect_no "backup"; expect_no "migrate$"
  [[ ! -e "$TELOS_NODE_PATH/state/operation.json" ]] || fail "$CASE: a journal was started for a refused upgrade"
}
CASE="wrong confirmation"; sandbox confirm
if env -i PATH="$work/bin:/usr/bin:/bin" bash scripts/upgrade.sh --generation "$SB/srv/generations/$NEW" --candidate-lock "$SB/locks/new.lock" --current-candidate-lock "$SB/locks/old.lock" --result-out "$SB/r.json" --confirm yes >/dev/null 2>&1; then fail "$CASE: accepted"; fi

CASE="unlisted predecessor"; sandbox pred 24 no; refuses "not allowed from the installed generation"
CASE="tampered generation"; sandbox tamper; echo tampered >>"$SB/srv/generations/$NEW/images.env"; refuses "target generation did not verify"
CASE="group-writable file"; sandbox dirty; chmod 666 "$SB/srv/generations/$NEW/images.env"; refuses "target generation did not verify"
CASE="symlink in generation"; sandbox link; ln -s /etc/passwd "$SB/srv/generations/$NEW/leak"; refuses "target generation did not verify"
CASE="generation path escape"; sandbox escape; mkdir -p "$SB/elsewhere"; refuses "--generation must be" "$SB/elsewhere"
CASE="not the installed generation"; sandbox mismatch; printf 'some other candidate\n' >"$SB/locks/other.lock"; refuses "active generation" "$NEW" new other
CASE="already installed"; sandbox same; refuses "already the installed generation" "$OLD" old old
CASE="receipt mismatch"; sandbox receipt; rm "$TELOS_NODE_PATH/state/installed-generation.json"; refuses "receipt does not verify"
CASE="schema outside window"; sandbox window; echo 99 >"$STUB_STATE/schema"; refuses "not allowed from the installed generation"
CASE="database unreadable"; sandbox dbdown; touch "$STUB_STATE/db-down"; refuses "could not read the database schema"

CASE=contention; sandbox busy
( exec 9>"$TELOS_NODE_PATH/state/maintenance.lock"; flock -n 9; sleep 4 ) &
holder=$!
sleep 0.5
set +e; upgrade; code=$?; set -e
[[ $code -eq 75 ]] || fail "$CASE: exited $code, expected 75"
expect_no "podman stop"; expect_no "backup"
wait "$holder" 2>/dev/null || true

# ------------------------------------------------------------------------ failures before the first migration
CASE="backup failure"; sandbox backup; touch "$STUB_STATE/fail-backup"
upgrade && fail "$CASE: succeeded"
expect_state aborted; expect_active "$OLD"; expect_serving "$OLD"; expect_no "podman stop"; expect_no "migrate$"
[[ "$(journal_field snapshotId)" == None ]] || fail "$CASE: a snapshot was recorded for a failed backup"

CASE="migration fails, schema unchanged"; sandbox migfail; touch "$STUB_STATE/fail-migrate-${NEW:0:8}"
upgrade && fail "$CASE: succeeded"
expect_state aborted; expect_active "$OLD"; expect_serving "$OLD"
expect_calls_in_order "backup" "podman stop" "${NEW:0:8} migrate$" "${OLD:0:8} start"

CASE="migration fails part-way"; sandbox migpartial; echo 23 >"$SB/partial"; cp "$SB/partial" "$STUB_STATE/fail-migrate-${NEW:0:8}"
upgrade && fail "$CASE: succeeded"
expect_state restore-required; expect_active "$OLD"
grep -q "restore.sh --mode rollback --operation-id $(journal_field operationId)" "$SB/out" || fail "$CASE: no restore instruction: $(tail -4 "$SB/out")"
[[ -z "$(ls "$STUB_STATE" | grep running- || true)" ]] || fail "$CASE: something is serving a half-migrated database"

# ------------------------------------------------------------------------ failures after migration
CASE="target never ready, compatible"; sandbox unready 24; touch "$STUB_STATE/start-fails-${NEW:0:8}"
upgrade && fail "$CASE: succeeded"
expect_state rolled-back; expect_active "$OLD"; expect_serving "$OLD"
[[ "$(cat "$STUB_STATE/schema")" == "$NEW_SCHEMA" ]] || fail "$CASE: the schema was changed back without a restore"
python3 -I scripts/lib/generations.py check-receipt --root "$TELOS_RELEASE_BASE" --node "$TELOS_NODE_PATH" --id "$OLD" --no-owner-check >/dev/null || fail "$CASE: receipt does not name the restored generation"

CASE="target never ready, incompatible"; sandbox unready_incompat 22; touch "$STUB_STATE/start-fails-${NEW:0:8}"
upgrade && fail "$CASE: succeeded"
expect_state restore-required; expect_no "${OLD:0:8} start"
grep -q "RESTORE-RECORDED-ROLLBACK" "$SB/out" || fail "$CASE: no restore instruction"

CASE="target up but unhealthy"; sandbox unhealthy 24; touch "$STUB_STATE/unhealthy-${NEW:0:8}"
upgrade && fail "$CASE: succeeded"
expect_state rolled-back; expect_active "$OLD"

# ------------------------------------------------------------------------ interruption at every step
# kill -9 the script at a point, run the SAME command again, and require the documented outcome.
interrupt() {   # name crash_at expected_state_after_recovery expected_active_after_recovery [then_retry_succeeds]
  CASE="interrupt $1"; sandbox "int-$1"
  CRASH_AT="$2" upgrade && fail "$CASE: survived a SIGKILL at $2"
  unset CRASH_AT
  local before; before="$(journal_field state)"
  upgrade || true
  expect_state "$3"; [[ "$4" == new ]] && expect_active "$NEW" || expect_active "$OLD"
  [[ "$4" == new ]] && expect_serving "$NEW" || expect_serving "$OLD"
  echo "  interrupted at $2 (journal was '$before') -> $3"
  if [[ "${5:-}" == retry ]]; then
    upgrade || fail "$CASE: the retry after recovery failed: $(tail -3 "$SB/out")"
    expect_state complete; expect_active "$NEW"; expect_serving "$NEW"
  fi
}
interrupt backup backup aborted old retry
interrupt stop-writers stop-writers aborted old retry
interrupt migrate-before migrate-before aborted old retry
interrupt migrate-after migrate-after complete new
interrupt target-stop target-stop complete new
interrupt target-start target-start complete new
unset CRASH_AT

# A second invocation never stacks a new upgrade on an unfinished one, and a clean node recovers nothing.
CASE="rerun after success"; sandbox rerun
upgrade || fail "$CASE: first upgrade"
upgrade && fail "$CASE: a second upgrade to the installed generation was accepted"
grep -q "already the installed generation\|active generation" "$SB/out" || fail "$CASE: wrong refusal: $(tail -2 "$SB/out")"

# ------------------------------------------------------------------------ rollback.sh
CASE="rollback, compatible"; sandbox rb 24
upgrade || fail "$CASE: upgrade"
opid="$(journal_field operationId)"
: >"$STUB_STATE/calls.log"
rollback "$opid" || fail "$CASE: $(tail -4 "$SB/rollback-out")"
expect_state complete; expect_active "$OLD"; expect_serving "$OLD"
[[ "$(journal_field kind)" == rollback && "$(journal_field fromGeneration)" == "$NEW" && "$(journal_field toGeneration)" == "$OLD" ]] || fail "$CASE: rollback journal"
[[ -d "$TELOS_NODE_PATH/state/operations" && -f "$TELOS_NODE_PATH/state/operations/$opid.json" ]] || fail "$CASE: the upgrade's journal was not kept"
[[ -d "$TELOS_RELEASE_BASE/generations/$NEW" ]] || fail "$CASE: the failed generation was deleted"
python3 -I scripts/lib/generations.py check-receipt --root "$TELOS_RELEASE_BASE" --node "$TELOS_NODE_PATH" --id "$OLD" --no-owner-check >/dev/null || fail "$CASE: receipt"
rollback "$opid" && fail "$CASE: rolled back twice"

CASE="rollback, incompatible schema"; sandbox rbx 22
upgrade || fail "$CASE: upgrade"
opid="$(journal_field operationId)"; : >"$STUB_STATE/calls.log"
rollback "$opid" && fail "$CASE: rolled back onto a schema the old binaries cannot run"
grep -q "RESTORE-RECORDED-ROLLBACK" "$SB/rollback-out" || fail "$CASE: no restore instruction"
expect_active "$NEW"; expect_serving "$NEW"; expect_no "podman stop"; expect_no " stop"

CASE="rollback, wrong operation"; sandbox rbw 24
upgrade || fail "$CASE: upgrade"
rollback "$(printf 'f%.0s' $(seq 32))" && fail "$CASE: accepted another operation id"
expect_active "$NEW"

echo "upgrade/rollback: ordering, refusals, journal, recovery at every step, and compatible/incompatible rollback verified"
