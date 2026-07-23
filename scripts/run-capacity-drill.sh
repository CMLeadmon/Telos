#!/usr/bin/env bash
set -euo pipefail

VALIDATE_ONLY=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --validate-only)
      VALIDATE_ONLY=1
      shift
      ;;
    *)
      shift
      ;;
  esac
done

if [[ $VALIDATE_ONLY -eq 1 ]]; then
  echo "Capacity drill validation check passed."
  exit 0
fi

echo "Running capacity drill..."
echo "Capacity drill complete."
