#!/usr/bin/env bash
# restore_test.sh -- scripts/restore.sh driven through fake podman, restic and telos.
#
# Every refusal must happen BEFORE anything is changed: no volume created, no compose call,
# no file written under the node path. A failure after the databases were started must bring
# the stack down, leave the journal naming the step, remove the plaintext staging directory
# and never start the full stack. The success run must restore in the safe order: volumes,
# databases only, PostgreSQL, MariaDB, migrations, SANITIZE, and only then the stack.
set -euo pipefail
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "restore_test: $*" >&2; exit 1; }
bin="$work/bin"; mkdir -p "$bin"

# ------------------------------------------------------------------ fakes
cat >"$bin/podman" <<'SH'
#!/usr/bin/env bash
set -u
state="$STUB_STATE"; printf '%s\n' "$*" >>"$state/podman.log"
fail_at="${FAIL_AT:-}"
case "$1" in
  ps)     [[ -f "$state/existing-containers" ]] && cat "$state/existing-containers"; exit 0 ;;
  image)  [[ "$2" == exists ]] && { [[ -f "$state/images-present" ]]; exit $?; }; exit 0 ;;
  volume)
    case "$2" in
      exists) [[ -f "$state/existing-volumes" ]] && grep -qx "$3" "$state/existing-volumes"; exit $? ;;
      create) touch "$state/volume.$3"; exit 0 ;;
      import) [[ "$fail_at" == volume-import ]] && exit 1; exit 0 ;;
      export) out=""; prev=""; for a in "$@"; do [[ "$prev" == --output ]] && out="$a"; prev="$a"; done; [[ -n "$out" ]] && echo "exported $3" >"$out"; exit 0 ;;
      rm)     [[ -f "$state/existing-volumes" ]] && sed -i "/^$3\$/d" "$state/existing-volumes"; exit 0 ;;
    esac ;;
  exec)
    shift; [[ "$1" == -i ]] && { shift; cat >/dev/null; }
    joined="$*"
    if [[ "$joined" == *pg_isready* || "$joined" == *healthcheck.sh* ]]; then exit 0; fi
    if [[ "$joined" == *pg_restore* ]]; then [[ "$fail_at" == pg_restore ]] && exit 1; exit 0; fi
    if [[ "$joined" == *mariadb* ]]; then [[ "$fail_at" == mariadb ]] && exit 1; exit 0; fi
    exit 0 ;;
  compose)
    shift; args="$*"
    case "$args" in
      *" build "*)          echo "BUILT $*" >>"$state/build.log"; exit 0 ;;
      *"sanitize-restore --snapshot"*) [[ "$fail_at" == sanitize ]] && exit 1; echo "SANITIZED $*" >>"$state/order.log"; exit 0 ;;
      *"run --rm"*"telos-migrate"*)    [[ "$fail_at" == migrate ]] && exit 1; echo "MIGRATED" >>"$state/order.log"; exit 0 ;;
      *"up -d postgres grimmory-db"*) echo "DATABASES-UP" >>"$state/order.log"; exit 0 ;;
      *" down"*)            echo "STACK-DOWN" >>"$state/order.log"; exit 0 ;;
    esac
    exit 0 ;;
esac
exit 0
SH
cat >"$bin/restic" <<'SH'
#!/usr/bin/env bash
set -u
state="$STUB_STATE"; printf '%s\n' "$1" >>"$state/restic.log"
case "$1" in
  cat) exit 0 ;;
  restore)
    id="$2"; target=""; prev=""; for a in "$@"; do [[ "$prev" == --target ]] && target="$a"; prev="$a"; done
    mkdir -p "$target"; cp -a "$state/snapshot/." "$target/"; exit 0 ;;
esac
exit 0
SH
chmod +x "$bin/podman" "$bin/restic"
printf '#!/usr/bin/env bash\nexit 0\n' >"$bin/podman-compose"; chmod +x "$bin/podman-compose"

# The restored gateway's health, as the CLI's own probe sees it (TELOS_HEALTH_URL overrides it).
mkdir -p "$work/health"; printf '{"status":"ok","checks":[]}' >"$work/health/health"
python3 -u -I -m http.server 0 --bind 127.0.0.1 --directory "$work/health" >"$work/health.log" 2>&1 &
health_pid=$!
trap 'kill "$health_pid" 2>/dev/null; rm -rf "$work"' EXIT
for _ in $(seq 1 50); do health_port="$(sed -n 's/.*port \([0-9][0-9]*\).*/\1/p' "$work/health.log" | head -1)"; [[ -n "$health_port" ]] && break; sleep 0.1; done
[[ -n "${health_port:-}" ]] || fail "the fake health server did not start"
export HEALTH_URL="http://127.0.0.1:$health_port/health"

SNAPSHOT_ID="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
SOURCE_ID_FILE="$work/source-machine-id"; echo "source-machine-0000000000000000" >"$SOURCE_ID_FILE"
TARGET_ID_FILE="$work/target-machine-id"; echo "target-machine-1111111111111111" >"$TARGET_ID_FILE"
CANDIDATE_LOCK="$work/candidate-lock.json"; echo '{"sourceCommit":"'"$(printf '%040d' 1)"'","version":"0.1.0-beta.1"}' >"$CANDIDATE_LOCK"

# A snapshot of a node made by the SOURCE machine, with real component hashes.
make_snapshot() {  # DIR [candidate-digest]
  local dir="$1" candidate="${2:-}" data="$1/var/tmp/telos-backup.AbCdEf/data"
  mkdir -p "$data/inner"
  printf 'PGDMP' >"$data/postgres.dump"; printf -- '-- sql\n' >"$data/grimmory.sql"
  printf 'STORAGE_PATH=%s\nTELOS_DOMAIN=telos.test\n' "$work/restored-storage" >"$data/node.env"
  local content; content="$(mktemp -d)"; mkdir -p "$content/config"; echo key >"$content/config/cursor-keys.json"
  tar -C "$content" -cf "$data/node-config.tar" config
  for f in traefik-acme jellyfin-config grimmory-config release-capsule; do echo "$f" >"$content/f"; tar -C "$content" -cf "$data/$f.tar" f; done
  mkdir -p "$content/books"; echo book >"$content/books/b.epub"; tar -C "$content" -cf "$data/shared-storage.tar" books
  python3 - "$data/migrations.json" <<'PY'
import hashlib, json, os, sys
d = "backend/db/migrations"
out = [{"version": int(n[:4]), "name": n[5:-4], "checksum": hashlib.sha256(open(os.path.join(d, n), "rb").read()).hexdigest()} for n in sorted(os.listdir(d)) if n.endswith(".sql")]
json.dump(out, open(sys.argv[1], "w"))
PY
  local args=(--stage "$data" --out "$data/manifest.json" --started 2026-10-14T00:00:00Z --consistency-point 2026-10-14T00:00:30Z
              --host-id-sha256 "sha256:$(sha256sum "$SOURCE_ID_FILE" | cut -d' ' -f1)" --release beta.1 --tag scheduled --tag telos --migrations "$data/migrations.json")
  [[ -z "$candidate" ]] || args+=(--candidate-lock-digest "$candidate")
  python3 -I scripts/lib/backup_manifest.py create "${args[@]}"
  rm -rf "$content" "$data/inner"
}

make_host() {  # NAME [candidate-digest] -> prepares an empty target host and a snapshot
  local n="$1"
  mkdir -p "$work/host-$n/state-stub" "$work/host-$n/node" "$work/host-$n/tmp" "$work/host-$n/release/backend/db" "$work/host-$n/release/cli"
  cp -r backend/db/migrations "$work/host-$n/release/backend/db/"
  cp -r cli/lib "$work/host-$n/release/cli/lib"
  touch "$work/host-$n/release/docker-compose.yml"
  printf '#!/usr/bin/env bash\necho "telos $*" >>"$STUB_STATE/order.log"\n[[ "${FAIL_AT:-}" == start ]] && exit 1\nexit 0\n' >"$work/host-$n/release/telos"; chmod +x "$work/host-$n/release/telos"
  mkdir -p "$work/host-$n/stub/snapshot"
  make_snapshot "$work/host-$n/stub/snapshot" "${2:-}"
  printf '%s' "restic-password" >"$work/host-$n/pw"; chmod 0600 "$work/host-$n/pw"
}

run_restore() {  # NAME [--flags...] -- [VAR=value ...]
  local n="$1"; shift
  local flags=()
  while [[ $# -gt 0 && "$1" != -- ]]; do flags+=("$1"); shift; done
  [[ "${1:-}" == -- ]] && shift
  env -i HOME="$work" PATH="$bin:/usr/bin:/bin" NO_COLOR=1 PODMAN_COMPOSE_PROVIDER="$bin/podman-compose" TELOS_DEV=1 TELOS_HEALTH_URL="$HEALTH_URL" \
    STUB_STATE="$work/host-$n/stub" TELOS_NODE_PATH="$work/host-$n/node" TELOS_BACKUP_TMP_ROOT="$work/host-$n/tmp" \
    TELOS_RELEASE_ROOT="$work/host-$n/release" TELOS_BACKUP_REQUIRE_ROOT=0 TELOS_MACHINE_ID_FILE="$TARGET_ID_FILE" \
    RESTIC_REPOSITORY="sftp:backup@offsite.example:/srv/telos" RESTIC_PASSWORD_FILE="$work/host-$n/pw" RESTIC_CACHE_DIR="$work/cache" "$@" \
    bash scripts/restore.sh --mode separate-host --snapshot "$SNAPSHOT_ID" --result-out "$work/host-$n/result.json" "${flags[@]}" \
    >"$work/out-$n" 2>&1
}
GOOD=(--rehearsal --confirm RESTORE-TO-EMPTY-REFERENCE-HOST)

untouched() {  # NAME -> nothing was changed on the host
  local n="$1"
  [[ ! -s "$work/host-$n/stub/order.log" ]] || fail "$n: a stack operation ran: $(cat "$work/host-$n/stub/order.log")"
  ! ls "$work/host-$n/stub"/volume.* >/dev/null 2>&1 || fail "$n: a volume was created"
  [[ ! -e "$work/host-$n/node/node.env" ]] || fail "$n: node.env was written"
  [[ ! -e "$work/host-$n/result.json" ]] || fail "$n: a result was written for a refused restore"
  [[ -z "$(find "$work/host-$n/tmp" -mindepth 1 -maxdepth 1 -name 'telos-restore.*')" ]] || fail "$n: the plaintext staging directory survived"
}
refuses() {  # NAME FRAGMENT [flags/env as run_restore]
  local n="$1" fragment="$2"; shift 2
  if run_restore "$n" "$@"; then fail "$n: the restore was accepted but should be refused ($fragment)"; fi
  grep -qF -- "$fragment" "$work/out-$n" || fail "$n: expected '$fragment', got: $(tail -4 "$work/out-$n")"
  untouched "$n"
}

# ---------------------------------------------------------------- refusals before any change
make_host usage
run_restore usage --rehearsal && fail "usage: a missing confirmation was accepted"
grep -q "RESTORE-TO-EMPTY-REFERENCE-HOST" "$work/out-usage" || fail "usage: no instruction for the confirmation"
untouched usage
run_restore usage --confirm WRONG --rehearsal && fail "usage: a wrong confirmation was accepted"
run_restore usage --confirm RESTORE-TO-EMPTY-REFERENCE-HOST && fail "usage: neither a candidate lock nor --rehearsal was required"
untouched usage

make_host local
refuses local "off-node backend" "${GOOD[@]}" -- RESTIC_REPOSITORY="$work/local-repo"

make_host sameid
refuses sameid "machine that made the backup" "${GOOD[@]}" -- TELOS_MACHINE_ID_FILE="$SOURCE_ID_FILE"

candidate_digest="sha256:$(sha256sum "$CANDIDATE_LOCK" | cut -d' ' -f1)"
make_host candidate "sha256:$(printf 'd%.0s' $(seq 64))"
refuses candidate "different candidate" --candidate-lock "$CANDIDATE_LOCK" --confirm RESTORE-TO-EMPTY-REFERENCE-HOST

make_host tampered
echo evil >"$work/host-tampered/stub/snapshot/var/tmp/telos-backup.AbCdEf/data/postgres.dump"
refuses tampered "not trustworthy" "${GOOD[@]}"

make_host missing
rm "$work/host-missing/stub/snapshot/var/tmp/telos-backup.AbCdEf/data/grimmory.sql"
refuses missing "not trustworthy" "${GOOD[@]}"

make_host newer
python3 - "$work/host-newer/stub/snapshot/var/tmp/telos-backup.AbCdEf/data" <<'PY'
import json, sys, subprocess, os
data = sys.argv[1]
migrations = json.load(open(os.path.join(data, "migrations.json")))
migrations.append({"version": len(migrations) + 1, "name": "from_the_future", "checksum": "f" * 64})
json.dump(migrations, open(os.path.join(data, "migrations.json"), "w"))
os.remove(os.path.join(data, "manifest.json"))
manifest = json.load(open(os.path.join(os.path.dirname(data), "..", "..", "..", "x"))) if False else None
PY
( cd "$work/host-newer/stub/snapshot/var/tmp/telos-backup.AbCdEf/data"
  python3 -I "$repo_root/scripts/lib/backup_manifest.py" create --stage . --out manifest.json --started 2026-10-14T00:00:00Z --consistency-point 2026-10-14T00:00:30Z \
    --host-id-sha256 "sha256:$(sha256sum "$SOURCE_ID_FILE" | cut -d' ' -f1)" --release beta.1 --tag scheduled --tag telos --migrations migrations.json )
refuses newer "newer than this release" "${GOOD[@]}"

make_host drift
python3 - "$work/host-drift/stub/snapshot/var/tmp/telos-backup.AbCdEf/data" <<'PY'
import json, sys, os
data = sys.argv[1]
migrations = json.load(open(os.path.join(data, "migrations.json")))
migrations[3]["checksum"] = "0" * 64
json.dump(migrations, open(os.path.join(data, "migrations.json"), "w"))
PY
( cd "$work/host-drift/stub/snapshot/var/tmp/telos-backup.AbCdEf/data"; rm manifest.json
  python3 -I "$repo_root/scripts/lib/backup_manifest.py" create --stage . --out manifest.json --started 2026-10-14T00:00:00Z --consistency-point 2026-10-14T00:00:30Z \
    --host-id-sha256 "sha256:$(sha256sum "$SOURCE_ID_FILE" | cut -d' ' -f1)" --release beta.1 --tag scheduled --tag telos --migrations migrations.json )
refuses drift "history drift" "${GOOD[@]}"

make_host hostiletar
( cd "$work/host-hostiletar/stub/snapshot/var/tmp/telos-backup.AbCdEf/data"
  python3 - <<'PY'
import io, tarfile
with tarfile.open("shared-storage.tar", "w") as t:
    link = tarfile.TarInfo("books/escape"); link.type = tarfile.SYMTYPE; link.linkname = "../../../../etc"
    t.addfile(link)
PY
  rm manifest.json
  python3 -I "$repo_root/scripts/lib/backup_manifest.py" create --stage . --out manifest.json --started 2026-10-14T00:00:00Z --consistency-point 2026-10-14T00:00:30Z \
    --host-id-sha256 "sha256:$(sha256sum "$SOURCE_ID_FILE" | cut -d' ' -f1)" --release beta.1 --tag scheduled --tag telos --migrations migrations.json )
refuses hostiletar "leaves the extraction root" "${GOOD[@]}"

make_host notempty
echo "telos-core" >"$work/host-notempty/stub/existing-containers"
refuses notempty "already has Telos containers" "${GOOD[@]}"
make_host notemptyvol
echo "telos_postgres_data" >"$work/host-notemptyvol/stub/existing-volumes"
refuses notemptyvol "not an empty reference host" "${GOOD[@]}"

# ---------------------------------------------------------------- failures after the databases start
declare -a LATE=(
  "volume restore|volume-import"
  "postgres restore|pg_restore"
  "mariadb restore|mariadb"
  "migrations|migrate"
  "sanitize|sanitize"
  "start stack|start"
)
for row in "${LATE[@]}"; do
  step="${row%%|*}"; injected="${row##*|}"; id="late-$injected"
  make_host "$id"
  if run_restore "$id" "${GOOD[@]}" -- FAIL_AT="$injected"; then fail "$id: the injected failure '$injected' still exited zero"; fi
  grep -qF "FAILED at step '$step'" "$work/out-$id" || fail "$id: expected a failure at '$step', got: $(tail -3 "$work/out-$id")"
  [[ -z "$(find "$work/host-$id/tmp" -mindepth 1 -maxdepth 1 -name 'telos-restore.*')" ]] || fail "$id: the plaintext staging directory survived"
  grep -q "\"step\":\"$step\",\"state\":\"failed\"" "$work/host-$id/node/state/restore-journal.jsonl" || fail "$id: the journal does not name the failed step"
  if [[ "$injected" != start ]]; then
    ! grep -q '^telos start' "$work/host-$id/stub/order.log" || fail "$id: the full stack started although '$step' failed"
  fi
  grep -q '^STACK-DOWN' "$work/host-$id/stub/order.log" || fail "$id: the stack was not brought down after the failure"
  [[ ! -e "$work/host-$id/result.json" ]] || fail "$id: a result was written for a failed restore"
  # Credentials are never served: sanitize must have run before any start.
  if grep -q '^telos start' "$work/host-$id/stub/order.log" 2>/dev/null; then
    python3 - "$work/host-$id/stub/order.log" <<'PY' || fail "$id: the stack started before the sanitizer ran"
import sys
order = [l.split()[0] for l in open(sys.argv[1]).read().splitlines()]
assert "SANITIZED" in order and order.index("SANITIZED") < order.index("telos"), order
PY
  fi
done

# ---------------------------------------------------------------- the safe order on success, and preserved prior state
make_host ok
mkdir -p "$work/host-ok/node/config" "$work/restored-storage"
echo "old config" >"$work/host-ok/node/config/old.json"; echo "OLD=1" >"$work/host-ok/node/node.env"
echo "existing upload" >"$work/restored-storage/leftover.txt"
run_restore ok "${GOOD[@]}" || fail "the success run failed: $(tail -4 "$work/out-ok")"
python3 - "$work/host-ok/stub/order.log" <<'PY' || fail "unsafe restore order: $(cat "$work/host-ok/stub/order.log")"
import sys
order = [l.split()[0] for l in open(sys.argv[1]).read().splitlines()]
expected = ["DATABASES-UP", "MIGRATED", "SANITIZED", "telos"]
assert [x for x in order if x in expected] == expected, order
assert "STACK-DOWN" not in order, order
PY
podman_log="$work/host-ok/stub/podman.log"
python3 - "$podman_log" <<'PY' || fail "volumes/databases were not restored in order: $(cat "$podman_log")"
import sys
lines = open(sys.argv[1]).read().splitlines()
def first(prefix):
    for i, l in enumerate(lines):
        if l.startswith(prefix): return i
    raise SystemExit(f"missing: {prefix}")
assert first("volume create telos_traefik_acme") < first("compose") < first("exec -i telos-postgres") < first("exec -i telos-grimmory-db")
PY
for v in traefik_acme jellyfin_config grimmory_config; do [[ -e "$work/host-ok/stub/volume.telos_$v" ]] || fail "volume $v was not created"; done
[[ -f "$work/host-ok/node/node.env" ]] && grep -q 'TELOS_DOMAIN=telos.test' "$work/host-ok/node/node.env" || fail "node.env was not restored"
[[ "$(stat -c %a "$work/host-ok/node/node.env")" == 600 ]] || fail "node.env mode is $(stat -c %a "$work/host-ok/node/node.env")"
[[ -f "$work/host-ok/node/config/cursor-keys.json" ]] || fail "the signing keys were not restored"
[[ -f "$work/restored-storage/books/b.epub" ]] || fail "shared storage was not restored"
rollback="$(find "$work/host-ok" -maxdepth 2 -type d -path '*telos-rollback/*' | head -1)"
[[ -n "$rollback" && -f "$rollback/node.env" && -f "$rollback/config/old.json" && -f "$rollback/storage/leftover.txt" ]] \
  || fail "prior state was not preserved under a rollback directory (found: ${rollback:-none})"
[[ "$(stat -c %a "$rollback")" == 700 ]] || fail "the rollback directory is not private"
[[ ! -e "$work/restored-storage/leftover.txt" ]] || fail "prior shared storage was left mixed into the restored one"
python3 - "$work/host-ok/result.json" "$SNAPSHOT_ID" <<'PY' || fail "bad result record"
import json, sys
r = json.load(open(sys.argv[1]))
assert r["status"] == "restored" and r["mode"] == "separate-host" and r["snapshot"] == sys.argv[2]
assert r["recordKind"] == "rehearsal" and r["sourceHostIdSha256"] != r["targetHostIdSha256"]
assert r["snapshotFinishedAt"] and r["consistencyPointAt"] <= r["snapshotFinishedAt"]
assert r["incidentDeclaredAt"] is None   # nobody declared an incident, so the recovery clock never started
PY
# An operator-declared incident time is recorded as given, and a malformed one is refused before anything changes.
make_host declared >/dev/null
make_host badtime >/dev/null
run_restore declared "${GOOD[@]}" --incident-declared-at 2026-10-14T04:30:00Z || fail "a restore with a declared incident time failed: $(tail -3 "$work/out-declared")"
python3 -I -c 'import json,sys; assert json.load(open(sys.argv[1]))["incidentDeclaredAt"] == "2026-10-14T04:30:00Z"' "$work/host-declared/result.json" \
  || fail "the declared incident time was not recorded"
run_restore badtime "${GOOD[@]}" --incident-declared-at yesterday && fail "a malformed incident time was accepted"
[[ ! -e "$work/host-badtime/result.json" ]] || fail "a result was written for a refused restore"
[[ -z "$(find "$work/host-ok/tmp" -mindepth 1 -maxdepth 1 -name 'telos-restore.*')" ]] || fail "ok: the staging directory survived"
python3 - "$work/host-ok/node/state/restore-journal.jsonl" <<'PY' || fail "the journal is incomplete"
import json, sys
steps = [json.loads(l) for l in open(sys.argv[1])]
done = [s["step"] for s in steps if s["state"] == "done"]
for needed in ("configuration", "empty-host check", "snapshot retrieval", "verification", "preserve prior state", "configuration restore",
               "volume restore", "start databases", "postgres restore", "mariadb restore", "migrations", "sanitize", "start stack", "result"):
    assert needed in done, (needed, done)
PY
# ---------------------------------------------------------------- --mode rollback: undo a failed upgrade on the same host
FROM_GEN="$(printf '1%.0s' $(seq 64))"; TO_GEN="$(printf '2%.0s' $(seq 64))"
make_rollback_host() {  # NAME [journal-state] [host-machine-file] -> a host that holds a failed upgrade's state and its journal
  local n="$1" state="${2:-restore-required}" machine="${3:-$SOURCE_ID_FILE}"
  make_host "$n" >/dev/null
  local node="$work/host-$n/node"
  mkdir -p "$node/state" "$node/config" "$work/host-$n/srv/generations/$FROM_GEN" "$work/host-$n/srv/generations/$TO_GEN" "$work/restored-storage-$n"
  chmod 0700 "$node/state"
  echo "FAILED=1" >"$node/node.env"; echo "failed-state key" >"$node/config/failed.json"
  printf 'telos_traefik_acme\ntelos_jellyfin_config\ntelos_grimmory_config\ntelos_postgres_data\ntelos_grimmory_db_data\n' >"$work/host-$n/stub/existing-volumes"
  printf 'telos-core\ntelos-postgres\n' >"$work/host-$n/stub/existing-containers"
  ROLLBACK_OP="$(python3 -I scripts/lib/generations.py journal begin --node "$node" --kind upgrade --from "$FROM_GEN" --to "$TO_GEN" --schema 22 \
    --host "sha256:$(sha256sum "$machine" | cut -d' ' -f1)" | python3 -I -c 'import json,sys; print(json.load(sys.stdin)["operationId"])')"
  python3 -I scripts/lib/generations.py journal advance --node "$node" --state snapshot-verified --snapshot "$SNAPSHOT_ID" >/dev/null
  case "$state" in
    restore-required) python3 -I scripts/lib/generations.py journal advance --node "$node" --state restore-required --error "old binaries cannot run" >/dev/null ;;
    failed)           python3 -I scripts/lib/generations.py journal advance --node "$node" --state failed --error "stack would not start" >/dev/null ;;
    complete)         for st in maintenance migrating migrated activated serving complete; do
                        python3 -I scripts/lib/generations.py journal advance --node "$node" --state "$st" --post-schema 24 >/dev/null; done ;;
  esac
}
write_generation_manifests() {  # NAME TO_ROLLBACK_COMPATIBLE_THROUGH -> verifiable generations; FROM runs schema <=22, TO leaves 24
  python3 -I - "$work/host-$1/srv/generations" "$FROM_GEN" "$TO_GEN" "$2" <<'PY'
import os, sys
sys.path.insert(0, "scripts/lib")
import release_graph as rg
root, from_gen, to_gen, to_compat = sys.argv[1:]
def manifest(current, lo, compat):
    return {"schemaVersion": 1, "sourceCommit": "4" * 40, "version": "0.1.0-beta.1", "images": {"core": "sha256:" + "5" * 64},
            "migrations": {"min": lo, "current": current, "max": current, "checksumSetDigest": "sha256:" + "6" * 64},
            "predecessorIds": [], "rollbackCompatibleThrough": compat, "artifactIndexSubset": []}
for generation, record in ((from_gen, manifest(22, 1, 22)), (to_gen, manifest(24, 10, int(to_compat)))):
    directory = os.path.join(root, generation)
    os.chmod(directory, 0o755)
    path = os.path.join(directory, "generation.json")
    with open(path, "wb") as handle:
        handle.write(rg.canonical_bytes(record))
    os.chmod(path, 0o644)
PY
}
run_rollback() {  # NAME [flags...] -- [VAR=value ...]
  local n="$1"; shift
  local flags=()
  while [[ $# -gt 0 && "$1" != -- ]]; do flags+=("$1"); shift; done
  [[ "${1:-}" == -- ]] && shift
  env -i HOME="$work" PATH="$bin:/usr/bin:/bin" NO_COLOR=1 PODMAN_COMPOSE_PROVIDER="$bin/podman-compose" TELOS_DEV=1 TELOS_HEALTH_URL="$HEALTH_URL" \
    STUB_STATE="$work/host-$n/stub" TELOS_NODE_PATH="$work/host-$n/node" TELOS_BACKUP_TMP_ROOT="$work/host-$n/tmp" \
    TELOS_RELEASE_ROOT="$work/host-$n/release" TELOS_RELEASE_BASE="$work/host-$n/srv" TELOS_BACKUP_REQUIRE_ROOT=0 TELOS_MACHINE_ID_FILE="$SOURCE_ID_FILE" \
    RESTIC_REPOSITORY="sftp:backup@offsite.example:/srv/telos" RESTIC_PASSWORD_FILE="$work/host-$n/pw" RESTIC_CACHE_DIR="$work/cache" "$@" \
    bash scripts/restore.sh --mode rollback --operation-id "${ROLLBACK_OP_OVERRIDE:-$ROLLBACK_OP}" --result-out "$work/host-$n/result.json" "${flags[@]}" \
    >"$work/out-$n" 2>&1
}
ROLLBACK_GOOD=(--rehearsal --confirm RESTORE-RECORDED-ROLLBACK)
rollback_untouched() {  # NAME -> a refused rollback changed nothing
  local n="$1"
  ! grep -qE '^(volume (export|rm|create)|compose.* down)' "$work/host-$n/stub/podman.log" 2>/dev/null || fail "$n: the runtime was changed by a refused rollback: $(cat "$work/host-$n/stub/podman.log")"
  [[ ! -s "$work/host-$n/stub/order.log" ]] || fail "$n: a stack operation ran: $(cat "$work/host-$n/stub/order.log")"
  [[ "$(cat "$work/host-$n/node/node.env")" == FAILED=1 ]] || fail "$n: node.env was changed"
  [[ ! -e "$work/host-$n/result.json" ]] || fail "$n: a result was written for a refused rollback"
  [[ ! -e "$work/host-$n/srv/current" ]] || fail "$n: the generation pointer moved"
  [[ -z "$(find "$work/host-$n/tmp" -mindepth 1 -maxdepth 1 -name 'telos-restore.*')" ]] || fail "$n: the plaintext staging directory survived"
}
rollback_refuses() {  # NAME FRAGMENT [flags/env as run_rollback]
  local n="$1" fragment="$2"; shift 2
  if run_rollback "$n" "$@"; then fail "$n: the rollback restore was accepted but should be refused ($fragment)"; fi
  grep -qF -- "$fragment" "$work/out-$n" || fail "$n: expected '$fragment', got: $(tail -4 "$work/out-$n")"
  rollback_untouched "$n"
}

make_rollback_host rbusage
run_rollback rbusage --rehearsal && fail "rbusage: a missing confirmation was accepted"
grep -q "RESTORE-RECORDED-ROLLBACK" "$work/out-rbusage" || fail "rbusage: no instruction for the confirmation"
run_rollback rbusage "${ROLLBACK_GOOD[@]}" --snapshot "$SNAPSHOT_ID" && fail "rbusage: a snapshot named on the command line was accepted; the journal's is the only one"
run_rollback rbusage --rehearsal --confirm RESTORE-TO-EMPTY-REFERENCE-HOST && fail "rbusage: the separate-host confirmation unlocked a rollback"
rollback_untouched rbusage

make_rollback_host rbwrongop
ROLLBACK_OP_OVERRIDE="$(printf 'f%.0s' $(seq 32))" rollback_refuses rbwrongop "not the node's last operation" "${ROLLBACK_GOOD[@]}"
# A completed upgrade is handed to a restore only when swapping binaries cannot undo it.
make_rollback_host rbdone complete
rollback_refuses rbdone "cannot be handed to a restore" "${ROLLBACK_GOOD[@]}"        # generations that do not verify prove nothing
make_rollback_host rbdonecompat complete; write_generation_manifests rbdonecompat 24   # the previous binaries accept schema 24
rollback_refuses rbdonecompat "use rollback.sh" "${ROLLBACK_GOOD[@]}" -- TELOS_GENERATION_OWNER_CHECK=0
python3 -I -c 'import json,sys; assert json.load(open(sys.argv[1]))["state"] == "complete"' "$work/host-rbdonecompat/node/state/operation.json" || fail "rbdonecompat: a refused claim changed the journal"
make_rollback_host rbhost restore-required "$TARGET_ID_FILE"
rollback_refuses rbhost "not the host the upgrade ran on" "${ROLLBACK_GOOD[@]}"
make_rollback_host rbdiff restore-required "$TARGET_ID_FILE"   # the journal and this machine agree, but the snapshot came from another
rollback_refuses rbdiff "runs on the machine that made the backup" "${ROLLBACK_GOOD[@]}" -- TELOS_MACHINE_ID_FILE="$TARGET_ID_FILE"

for start_state in restore-required failed; do
  id="rb-$start_state"
  make_rollback_host "$id" "$start_state"
  # the snapshot was made by SOURCE; this host IS that machine
  run_rollback "$id" "${ROLLBACK_GOOD[@]}" || fail "$id: the rollback restore failed: $(tail -4 "$work/out-$id")"
  python3 - "$work/host-$id/stub/podman.log" <<'PY' || fail "$id: unsafe rollback order: $(cat "$work/host-$id/stub/podman.log")"
import sys
lines = open(sys.argv[1]).read().splitlines()
def first(prefix):
    for i, l in enumerate(lines):
        if prefix in l: return i
    raise SystemExit(f"missing: {prefix}")
down = first(" down")
for volume in ("postgres_data", "grimmory_db_data", "traefik_acme"):
    assert down < first(f"volume export telos_{volume}") < first(f"volume rm telos_{volume}") < first(f"volume create telos_traefik_acme"), volume
assert first("volume create telos_traefik_acme") < first("exec -i telos-postgres") < first("exec -i telos-grimmory-db")
PY
  rollback_dir="$(find "$work/host-$id" -maxdepth 2 -type d -path '*telos-rollback/*' | head -1)"
  [[ -n "$rollback_dir" && -f "$rollback_dir/node.env" && -f "$rollback_dir/config/failed.json" ]] || fail "$id: the failed state was not kept"
  for v in postgres_data grimmory_db_data traefik_acme jellyfin_config grimmory_config; do
    [[ -s "$rollback_dir/volumes/$v.tar" ]] || fail "$id: the failed state of $v was not exported"
  done
  [[ "$(stat -c %a "$rollback_dir")" == 700 && "$(stat -c %a "$rollback_dir/volumes")" == 700 ]] || fail "$id: the rollback directory is not private"
  [[ "$(readlink "$work/host-$id/srv/current")" == "generations/$FROM_GEN" ]] || fail "$id: the generation the upgrade left was not reactivated"
  grep -q 'TELOS_DOMAIN=telos.test' "$work/host-$id/node/node.env" || fail "$id: node.env was not restored from the snapshot"
  python3 -I - "$work/host-$id/stub/order.log" <<'PY' || fail "$id: bad stack order: $(cat "$work/host-$id/stub/order.log")"
import sys
order = [l.split()[0] for l in open(sys.argv[1]).read().splitlines()]
expected = ["STACK-DOWN", "DATABASES-UP", "MIGRATED", "SANITIZED", "telos"]
assert [x for x in order if x in expected] == expected, order
PY
  python3 -I - "$work/host-$id" "$ROLLBACK_OP" <<'PY' || fail "$id: bad journal or result"
import json, sys
host, op = sys.argv[1:3]
result = json.load(open(f"{host}/result.json"))
assert result["status"] == "restored" and result["mode"] == "rollback" and result["operationId"] == op, result
journal = json.load(open(f"{host}/node/state/operation.json"))
assert journal["state"] == "restored" and journal["operationId"] == op, journal
PY
  # the failed upgrade can now be superseded without acknowledgement, because it is closed
  python3 -I scripts/lib/generations.py journal begin --node "$work/host-$id/node" --kind upgrade --from "$FROM_GEN" --to "$TO_GEN" --schema 22 \
    --host "sha256:$(sha256sum "$SOURCE_ID_FILE" | cut -d' ' -f1)" >/dev/null || fail "$id: a restored operation still blocks the next one"
done
make_rollback_host rbclaim complete; write_generation_manifests rbclaim 22              # the previous binaries cannot run on schema 24
run_rollback rbclaim "${ROLLBACK_GOOD[@]}" -- TELOS_GENERATION_OWNER_CHECK=0 || fail "rbclaim: restoring a completed, schema-incompatible upgrade failed: $(tail -4 "$work/out-rbclaim")"
python3 -I - "$work/host-rbclaim" "$ROLLBACK_OP" <<'PY' || fail "rbclaim: bad journal or result"
import json, sys
host, op = sys.argv[1:3]
result = json.load(open(f"{host}/result.json"))
assert result["status"] == "restored" and result["mode"] == "rollback" and result["operationId"] == op, result
journal = json.load(open(f"{host}/node/state/operation.json"))
assert journal["state"] == "restored" and journal["operationId"] == op, journal
PY
[[ "$(readlink "$work/host-rbclaim/srv/current")" == "generations/$FROM_GEN" ]] || fail "rbclaim: the generation the upgrade left was not reactivated"
make_rollback_host rbfail
FAIL_AT=sanitize run_rollback rbfail "${ROLLBACK_GOOD[@]}" -- FAIL_AT=sanitize && fail "rbfail: a failing sanitize was accepted"
python3 -I -c 'import json,sys; assert json.load(open(sys.argv[1]))["state"] == "restore-required"' "$work/host-rbfail/node/state/operation.json" || fail "rbfail: a failed rollback restore closed the journal"
grep -q STACK-DOWN "$work/host-rbfail/stub/order.log" || fail "rbfail: the stack was not brought down after the failure"

! grep -rq "restic-password" "$work"/out-* "$work"/host-*/stub/*.log || fail "a secret reached output or a command line"

echo "restore (separate-host and rollback): refusals change nothing; late failures stop the stack and name the step; the order is volumes, databases, restore, migrate, sanitize, start; prior state is preserved"
