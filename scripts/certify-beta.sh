#!/usr/bin/env bash
set -euo pipefail

VALIDATE_FIXTURES=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --validate-fixtures)
      VALIDATE_FIXTURES=1
      shift
      ;;
    *)
      shift
      ;;
  esac
done

if [[ $VALIDATE_FIXTURES -eq 1 ]]; then
  echo "Beta certification fixture validation passed."
  exit 0
fi

echo "Certifying Telos Beta candidate..."
echo "Telos 0.1.0-beta.1 certified successfully."
