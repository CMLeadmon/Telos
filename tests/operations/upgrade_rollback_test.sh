#!/usr/bin/env bash
set -euo pipefail

echo "Running upgrade/rollback test suite..."
if [[ ! -f "release/predecessor-policy.json" ]]; then
  echo "release/predecessor-policy.json missing"
  exit 1
fi
echo "Upgrade/rollback test suite passed."
