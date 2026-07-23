#!/usr/bin/env bash
set -euo pipefail

echo "Running release test suite..."
if [[ ! -f "release/images.lock" ]]; then
  echo "release/images.lock missing"
  exit 1
fi
if [[ ! -f "release/telos-release.pub" ]]; then
  echo "release/telos-release.pub missing"
  exit 1
fi
echo "Release test suite passed."
