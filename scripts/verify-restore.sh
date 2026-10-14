#!/usr/bin/env bash
# verify-restore.sh -- everything about a restore that can be proved before a service is touched.
#
#   scripts/verify-restore.sh --data DIR --target-machine-id-sha256 D --migrations DIR
#       (--candidate-lock-digest D | --rehearsal) [--free LABEL=BYTES ...]
#   scripts/verify-restore.sh tar ARCHIVE [ARCHIVE ...]
#
# DIR is a snapshot restored into staging. The checks are scripts/lib/restore_check.py's: a valid
# manifest whose components match their recorded sizes and SHA-256, a host that differs from the
# one that made the backup, a matching candidate, a schema no newer than this release and no
# migration whose checksum drifted, room on disk, and archives free of traversal, links out of
# the tree, devices and FIFOs. scripts/restore.sh runs the same checks before its first change.
set -euo pipefail
exec python3 -I "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/restore_check.py" "${1:-plan}" "${@:2}"
