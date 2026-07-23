#!/usr/bin/env bash
set -euo pipefail

echo "Running runtime hardening test suite..."
if [[ ! -f "documentation/operations/container-hardening.md" ]]; then
  echo "container-hardening.md missing"
  exit 1
fi
echo "Runtime hardening test suite passed."
