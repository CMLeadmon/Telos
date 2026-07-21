#!/usr/bin/env bash
# restore_test.sh — fake-runtime tests for restore preflight validation, atomic
# pointer activation/rollback, and drill RPO/RTO thresholds. The live multi-node
# restore drill (with DNS cutover and external probes) is the P7 operator gate.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fail() { echo "FAIL: $1" >&2; exit 1; }

write_manifest() {
	# $1=path $2=schema $3=checksum $4=include_all(1/0)
	python3 - "$1" "$2" "$3" "$4" <<'PY'
import json, sys
path, schema, checksum, allc = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4] == "1"
req = ["postgres.sql","grimmory-mariadb.sql","service-config.tar","env-material.enc",
       "acme.tar","jellyfin-config.tar","grimmory-config.tar","shared-storage.tar","release-capsule.tar"]
comps = [{"name": r, "sha256": "deadbeef", "size": 1} for r in req]
if not allc:
    comps = comps[:-1]  # drop release-capsule
json.dump({"formatVersion":1,"release":"v1","schemaVersion":schema,
           "migrationChecksumSet":checksum,"components":comps}, open(path,"w"))
PY
}

# --- preflight: complete + compatible manifest passes ------------------------
write_manifest "$tmp/ok.json" 11 abc123 1
bash scripts/verify-restore.sh --manifest "$tmp/ok.json" --node-schema 11 --node-checksum abc123 >/dev/null \
	|| fail "compatible manifest rejected"

# --- preflight: incomplete manifest fails ------------------------------------
write_manifest "$tmp/incomplete.json" 11 abc123 0
if bash scripts/verify-restore.sh --manifest "$tmp/incomplete.json" --node-schema 11 --node-checksum abc123 >/dev/null 2>&1; then
	fail "incomplete manifest accepted"
fi

# --- preflight: future schema fails ------------------------------------------
write_manifest "$tmp/future.json" 12 xyz 1
if bash scripts/verify-restore.sh --manifest "$tmp/future.json" --node-schema 11 --node-checksum abc123 >/dev/null 2>&1; then
	fail "future-schema manifest accepted"
fi

# --- preflight: checksum mismatch fails --------------------------------------
if bash scripts/verify-restore.sh --manifest "$tmp/ok.json" --node-schema 11 --node-checksum DIFFERENT >/dev/null 2>&1; then
	fail "checksum-mismatch manifest accepted"
fi

# --- atomic pointer activation + rollback ------------------------------------
export TELOS_ACTIVE_GENERATION_FILE="$tmp/active" TELOS_RESTORE_JOURNAL="$tmp/journal"
echo "gen-old" > "$TELOS_ACTIVE_GENERATION_FILE"
bash scripts/restore-state.sh journal-begin gen-new
bash scripts/restore-state.sh activate gen-new
[ "$(bash scripts/restore-state.sh current)" = "gen-new" ] || fail "activation did not update pointer"
bash scripts/restore-state.sh rollback
[ "$(bash scripts/restore-state.sh current)" = "gen-old" ] || fail "rollback did not restore prior generation"

# --- drill thresholds --------------------------------------------------------
# Within RPO/RTO: pass.
bash scripts/restore-drill.sh --fixture --backup-epoch 1000 --restore-start 2000 --restore-end 3000 --out "$tmp/drill.json" 2>/dev/null \
	|| fail "in-bounds drill reported failure"
grep -q '"status": "pass"' "$tmp/drill.json" || fail "drill status not pass"
# RPO exceeded (>86400s between backup and restore start): fail.
if bash scripts/restore-drill.sh --fixture --backup-epoch 0 --restore-start 90000 --restore-end 90100 --out "$tmp/drill2.json" 2>/dev/null; then
	fail "over-RPO drill reported success"
fi
# RTO exceeded (>14400s restore duration): fail.
if bash scripts/restore-drill.sh --fixture --backup-epoch 0 --restore-start 100 --restore-end 20000 --out "$tmp/drill3.json" 2>/dev/null; then
	fail "over-RTO drill reported success"
fi

echo "PASS: restore preflight, atomic pointer activation/rollback, and drill thresholds"
