#!/usr/bin/env bash
set -euo pipefail

echo "Running accumulated suite test..."
if [[ ! -f "scripts/verify-accumulated-suite.sh" ]]; then
  echo "scripts/verify-accumulated-suite.sh missing"
  exit 1
fi
echo "Accumulated suite test passed."
