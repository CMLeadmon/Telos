#!/usr/bin/env bash
# configure-integrations.sh — run the real `telos configure integrations --setup` unattended.
#
# REHEARSAL ONLY (run on the reference node as a sudo-capable user, from the repository
# checkout). The product command is interactive: it starts Jellyfin and Grimmory on
# loopback, waits for a person to finish their first-run setup, then reads the Jellyfin
# API key and Grimmory account at hidden prompts. This script plays the person: it
# provisions both services through their public HTTP APIs (provision-upstreams.py),
# types the key at the real prompt through a pseudo-terminal, and accepts the stored
# Grimmory password. Everything after that is the shipped command unchanged.
set -euo pipefail
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="${REHEARSAL_DIR:-/tmp/telos-rehearsal}"
mkdir -p "$work"; chmod 700 "$work"
rm -f "$work/ready" "$work/run.transcript"
cat >"$work/steps.json" <<JSON
[{"expect":"Press Enter when both services are set up"},{"waitfile":"$work/ready","timeout":900},{"send":"\n"},
 {"expect":"Jellyfin API key: "},{"sendfile":"$work/jellyfin-key"},
 {"expect":"Grimmory gateway account"},{"send":"\n"},{"expect":"Grimmory password"},{"send":"\n"},
 {"expect":"Expose which Jellyfin libraries"},{"send":"1,2\n"},{"expect":"Expose which Grimmory libraries"},{"send":"1\n"},
 {"expect":"Recorded Jellyfin libraries"}]
JSON
(
  # Provision only once the services answer, then release the waiting prompt.
  until curl -fsS -m 3 -o /dev/null http://127.0.0.1:8096/System/Info/Public 2>/dev/null \
     && curl -fsS -m 3 -o /dev/null http://127.0.0.1:6060/api/v1/healthcheck 2>/dev/null; do sleep 3; done
  sudo python3 "$repo/tests/rehearsal/provision-upstreams.py" --node-env /var/lib/telos/node.env --out "$work"
  sudo chown "$(id -un)" "$work/jellyfin-key"
  touch "$work/ready"
) &
helper=$!
status=0
sudo python3 -I "$repo/tests/helpers/pty_drive.py" "$work/run.transcript" --timeout 900 -- telos configure integrations --setup <"$work/steps.json" >"$work/run.out" 2>&1 || status=$?
kill "$helper" 2>/dev/null || true
wait "$helper" 2>/dev/null || true
sed 's/\x1b\[[0-9;]*m//g' "$work/run.transcript" | tail -8
exit "$status"
