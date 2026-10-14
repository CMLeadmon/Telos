#!/usr/bin/env bash
set -euo pipefail

# The backup fixture suite: the maintenance lock and due logic, then the encrypted off-node backup
# against a fake runtime and a fake restic repository -- every step failing in turn, signals,
# SIGKILL recovery, contention, secret hygiene, the manifest round trip and the release capsule.
# Hermetic; none of it is evidence about a real node (the live backup is the `encrypted-backup` gate).
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
bash scripts/tests/backup_test.sh
bash tests/operations/backup_restore_test.sh
echo "backup fixtures: lock, failure matrix, signals, manifest and capsule verified"
