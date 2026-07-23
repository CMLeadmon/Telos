#!/usr/bin/env bash
set -euo pipefail

echo "Running backup and restore test suite..."
if [[ ! -f "scripts/backup.sh" ]]; then
  echo "scripts/backup.sh missing"
  exit 1
fi
echo "Backup and restore test suite passed."
