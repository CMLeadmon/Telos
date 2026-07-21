#!/usr/bin/env bash
# verify-restore.sh — validate a backup manifest against this node before any
# active state is changed. Fails closed on a missing component, a future/
# incompatible schema, or a changed migration checksum set.
#
# Usage:
#   scripts/verify-restore.sh --manifest <path> --node-schema <n> --node-checksum <hex>
set -euo pipefail

manifest=""
node_schema=""
node_checksum=""
while [ $# -gt 0 ]; do
	case "$1" in
	--manifest) shift; manifest="$1" ;;
	--node-schema) shift; node_schema="$1" ;;
	--node-checksum) shift; node_checksum="$1" ;;
	*) echo "unknown arg: $1" >&2; exit 2 ;;
	esac
	shift
done

[ -f "$manifest" ] || { echo "verify-restore: manifest not found: $manifest" >&2; exit 1; }

python3 - "$manifest" "$node_schema" "$node_checksum" <<'PY'
import json, sys
manifest, node_schema, node_checksum = sys.argv[1], int(sys.argv[2]), sys.argv[3]
m = json.load(open(manifest))

required = [
    "postgres.sql","grimmory-mariadb.sql","service-config.tar","env-material.enc",
    "acme.tar","jellyfin-config.tar","grimmory-config.tar","shared-storage.tar",
    "release-capsule.tar",
]
present = {c["name"]: c for c in m.get("components", [])}
for r in required:
    if r not in present or not present[r].get("sha256"):
        print(f"verify-restore: FAIL missing/unhashed component {r}", file=sys.stderr); sys.exit(1)

bs = m.get("schemaVersion", 0)
if bs > node_schema:
    print(f"verify-restore: FAIL backup schema {bs} newer than node {node_schema}", file=sys.stderr); sys.exit(1)
if bs == node_schema and m.get("migrationChecksumSet") != node_checksum:
    print("verify-restore: FAIL migration checksum set mismatch", file=sys.stderr); sys.exit(1)

print("verify-restore: OK (schema %d, %d components)" % (bs, len(present)))
PY
