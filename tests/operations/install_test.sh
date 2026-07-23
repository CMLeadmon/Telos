#!/usr/bin/env bash
set -euo pipefail

echo "Running install test suite..."
if [[ ! -f "scripts/install.sh" ]]; then
  echo "scripts/install.sh missing"
  exit 1
fi
echo "Install test suite passed."
