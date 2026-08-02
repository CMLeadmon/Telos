#!/usr/bin/env bash
set -euo pipefail

# Telos Audiobook Migration Operator Helper
# Usage:
#   scripts/migrate-audiobooks.sh inventory [--library-id ID...]
#   scripts/migrate-audiobooks.sh copy
#   scripts/migrate-audiobooks.sh verify
#   scripts/migrate-audiobooks.sh switch
#   scripts/migrate-audiobooks.sh rollback
#   scripts/migrate-audiobooks.sh cleanup --backup-proof PROOF --confirm-sha SHA256

MANIFEST_PATH="migrations/audiobooks/manifest.json"

if [ $# -lt 1 ]; then
  echo "Usage: scripts/migrate-audiobooks.sh <inventory|copy|verify|switch|rollback|cleanup> [args...]"
  exit 2
fi

SUBCMD="$1"
shift

echo "==> Executing audiobook migration subcommand: ${SUBCMD}"
podman compose --profile migration run --rm telos-audiobook-migrate ./telos-core audiobook-migrate "${SUBCMD}" --manifest "${MANIFEST_PATH}" "$@"
