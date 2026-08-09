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
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Reuse the CLI's driver detection instead of assuming one. This script
# hardcoded `podman compose`, which does not exist on hosts that run
# podman-compose — including the reference development host — so every
# subcommand failed before reaching the gateway.
# shellcheck source=../cli/lib/common.sh
. "$REPO_ROOT/cli/lib/common.sh"
COMPOSE="$(detect_compose)"

if [ $# -lt 1 ]; then
  echo "Usage: scripts/migrate-audiobooks.sh <inventory|copy|verify|switch|rollback|cleanup> [args...]"
  exit 2
fi

SUBCMD="$1"
shift

# NOTE: the gateway subcommands this invokes are not implemented. They report
# that on stderr and exit non-zero rather than printing a success line, so a
# `switch` or `cleanup` here fails visibly instead of appearing to complete.
echo "==> Executing audiobook migration subcommand: ${SUBCMD}"
$COMPOSE --profile migration run --rm telos-audiobook-migrate ./telos-core audiobook-migrate "${SUBCMD}" --manifest "${MANIFEST_PATH}" "$@"
