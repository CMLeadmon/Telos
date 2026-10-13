#!/usr/bin/env bash
# smoke-browser-node.sh — same-origin member journey against a Telos browser node.
#
#   scripts/smoke-browser-node.sh --public-url https://community.example.net \
#     (--rehearsal --source-commit SHA [--rehearsal-lock PATH] | --candidate-lock PATH) \
#     --result-out PATH --fixture-manifest PATH [--bootstrap-empty-node] \
#     [--stage-pipe BASE] [--host-identity PATH]
#
# Credentials are read from stdin as one JSON object ({"owner":{...},"member":{...},
# "bootstrapToken":...}); there is deliberately no flag that could carry one. The
# script exits 0 (passed), 3 (not_run) or 1 (failed) and writes the shared
# gate-result record -- never an evidence envelope.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$script_dir/lib/browser_node_smoke.py" "$@"
