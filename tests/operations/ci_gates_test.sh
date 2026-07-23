#!/usr/bin/env bash
set -euo pipefail

echo "Running CI gates test..."
if [[ ! -f "ci/tools.lock" ]]; then
  echo "ci/tools.lock missing"
  exit 1
fi
if [[ ! -f "ci/license-policy.json" ]]; then
  echo "ci/license-policy.json missing"
  exit 1
fi
echo "CI gates test passed."
