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

# Reuse the CLI's Compose contract (Podman only, pinned provider, node.env) instead
# of assuming a driver. CLI_DIR is what the shared helpers expect.
export REPO_ROOT
CLI_DIR="$REPO_ROOT/cli"
export CLI_DIR
# shellcheck source=../cli/lib/common.sh
. "$CLI_DIR/lib/common.sh"
# shellcheck source=../cli/lib/config.sh
. "$CLI_DIR/lib/config.sh"

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
compose --profile migration run --rm telos-audiobook-migrate ./telos-core audiobook-migrate "${SUBCMD}" --manifest "${MANIFEST_PATH}" "$@"
