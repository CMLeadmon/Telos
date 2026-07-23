#!/usr/bin/env bash
set -euo pipefail

echo "Running capacity and exposure test suite..."
if [[ ! -f "scripts/run-capacity-drill.sh" ]]; then
  echo "scripts/run-capacity-drill.sh missing"
  exit 1
fi
if [[ ! -f "scripts/audit-host-exposure.sh" ]]; then
  echo "scripts/audit-host-exposure.sh missing"
  exit 1
fi
echo "Capacity and exposure test suite passed."
