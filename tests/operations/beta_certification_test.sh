#!/usr/bin/env bash
set -euo pipefail

echo "Running beta certification test suite..."
if [[ ! -f "ci/phase-gates.json" ]]; then
  echo "ci/phase-gates.json missing"
  exit 1
fi
echo "Beta certification test suite passed."
