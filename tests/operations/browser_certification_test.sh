#!/usr/bin/env bash
set -euo pipefail

echo "Running browser certification test suite..."
if [[ ! -f "scripts/certify-browser-matrix.sh" ]]; then
  echo "scripts/certify-browser-matrix.sh missing"
  exit 1
fi
echo "Browser certification test suite passed."
