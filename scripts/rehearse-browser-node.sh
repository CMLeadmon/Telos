#!/usr/bin/env bash
# rehearse-browser-node.sh — reference-host controller for the hosted-browser journey.
#
# Runs scripts/smoke-browser-node.sh against a node and, on request from the probe
# over a private FIFO pair, stops ONLY Jellyfin, then ONLY Grimmory, then restores
# both, so the probe can assert each outage degrades its own module and nothing
# else. The probe cannot name an arbitrary command: it can only send one of three
# fixed stage names. Services are restored in an EXIT trap, so an interrupted or
# failed rehearsal never leaves the node degraded.
#
# Arguments are exactly the smoke probe's (the controller adds --stage-pipe);
# credentials are read from stdin and passed through untouched.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export REPO_ROOT="${REPO_ROOT:-$(cd "$script_dir/.." && pwd)}"
export CLI_DIR="$REPO_ROOT/cli"
# shellcheck source=../cli/lib/common.sh
. "$CLI_DIR/lib/common.sh"
# shellcheck source=../cli/lib/config.sh
. "$CLI_DIR/lib/config.sh"

for argument in "$@"; do
  case "$argument" in
    --stage-pipe|--stage-pipe=*) die "rehearse-browser-node: --stage-pipe is controller-owned" ;;
  esac
done

work="$(mktemp -d)"
chmod 700 "$work"
smoke_pid=""
restore_services() {
  # The stage handler runs in a subshell, so it leaves a marker instead of a variable.
  if [ -e "$work/services-touched" ]; then
    compose_with -- start jellyfin grimmory >/dev/null 2>&1 \
      || warn "could not restart Jellyfin/Grimmory; run: podman compose -p telos start jellyfin grimmory"
    rm -f "$work/services-touched"
  fi
}
cleanup() {
  local status=$?
  trap - EXIT INT TERM HUP
  [ -z "$smoke_pid" ] || kill "$smoke_pid" 2>/dev/null || true
  restore_services
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

mkfifo "$work/stage.out" "$work/stage.in"

# Credentials flow stdin -> probe without touching argv, the environment or disk.
exec 3<&0
# TELOS_REHEARSAL_SMOKE replaces the probe in tests only; operators leave it unset.
"${TELOS_REHEARSAL_SMOKE:-$script_dir/smoke-browser-node.sh}" "$@" --stage-pipe "$work/stage" <&3 &
smoke_pid=$!
exec 3<&-

handle_stage() {
  case "$1" in
    "STAGE jellyfin-down")
      : >"$work/services-touched"
      compose_with -- stop jellyfin >/dev/null || return 1 ;;
    "STAGE grimmory-down")
      : >"$work/services-touched"
      compose_with -- start jellyfin >/dev/null || return 1
      compose_with -- stop grimmory >/dev/null || return 1 ;;
    "STAGE restored")
      compose_with -- start jellyfin grimmory >/dev/null || return 1
      rm -f "$work/services-touched" ;;
    *) return 1 ;;
  esac
  # Give the gateway's upstream caches and health checks time to notice the change.
  sleep "${TELOS_REHEARSAL_SETTLE_SECONDS:-8}"
}

(
  # A failed stage answers FAIL -- and keeps answering FAIL to later requests -- so
  # the probe fails loudly instead of blocking on a pipe nobody reads. The restore
  # stage is still attempted after a failure. The loop ends when the parent kills it
  # after the probe exits.
  broken=0
  while IFS= read -r line < "$work/stage.out"; do
    reply=GO
    if [ "$broken" -eq 1 ] && [ "$line" != "STAGE restored" ]; then
      reply=FAIL
    elif ! handle_stage "$line"; then
      broken=1
      reply=FAIL
    elif [ "$broken" -eq 1 ]; then
      reply=FAIL
    fi
    printf '%s\n' "$reply" > "$work/stage.in"
  done
) &
controller_pid=$!

set +e
wait "$smoke_pid"
smoke_status=$?
set -e
smoke_pid=""
kill "$controller_pid" 2>/dev/null || true
wait "$controller_pid" 2>/dev/null || true
exit "$smoke_status"
