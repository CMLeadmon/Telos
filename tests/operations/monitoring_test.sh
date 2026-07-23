#!/usr/bin/env bash
set -euo pipefail

echo "Running monitoring test suite..."
if [[ ! -f "config/prometheus/prometheus.yml" ]]; then
  echo "prometheus.yml missing"
  exit 1
fi
if [[ ! -f "config/prometheus/alerts.yml" ]]; then
  echo "alerts.yml missing"
  exit 1
fi
echo "Monitoring test suite passed."
