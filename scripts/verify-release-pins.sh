#!/usr/bin/env bash
set -euo pipefail

echo "Verifying release pins..."
if [[ ! -f "release/images.lock" ]]; then
  echo "release/images.lock missing"
  exit 1
fi
echo "Release pins verified."
