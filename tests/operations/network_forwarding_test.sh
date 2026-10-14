#!/usr/bin/env bash
set -euo pipefail

# The open Telos bridges must forward IPv4. netavark leaves /run/sysctl.d/10-netavark-<bridge>.conf behind and systemd-sysctl
# re-applies it to a later bridge of the same name, so after a restore (databases first) the ingress network can come up with
# forwarding off: loopback health passes, but no other host can reach the node. `telos start` repairs the open bridges only,
# `telos status` reports them without changing anything, and a lifecycle operation does not call the node ready while one is off.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "network_forwarding_test: $*" >&2; exit 1; }

mkdir -p "$work/bin"
cat >"$work/bin/podman" <<'SH'
#!/usr/bin/env bash
case "$1 $2" in
  "network inspect")
    case "$3" in
      telos-ingress) echo "${INGRESS_BRIDGE:-podman2}" ;;
      telos-egress)  echo "podman4" ;;
      telos-db)      echo "podman1" ;;
      *) exit 1 ;;
    esac ;;
  "ps -a") ;;
esac
exit 0
SH
chmod +x "$work/bin/podman"
export PATH="$work/bin:$PATH"

proc="$work/proc"
setup() {   # ingress egress db -> contents of the forwarding files ("-" = no file)
  rm -rf "$proc"; mkdir -p "$proc/net/ipv4/conf"
  local bridge value
  for pair in "podman2:$1" "podman4:$2" "podman1:$3"; do
    bridge="${pair%%:*}"; value="${pair#*:}"
    [[ "$value" == "-" ]] && continue
    mkdir -p "$proc/net/ipv4/conf/$bridge"; printf '%s\n' "$value" >"$proc/net/ipv4/conf/$bridge/forwarding"
  done
}
value_of() { cat "$proc/net/ipv4/conf/$1/forwarding"; }

# run FUNCTION [env...]: the helper functions as the CLI loads them
in_cli() {
  (
    export CLI_DIR="$repo_root/cli" REPO_ROOT="$repo_root" TELOS_NODE_PATH="$work/node" TELOS_PROC_SYS="$proc" NO_COLOR=1
    for kv in "${@:2}"; do export "$kv"; done
    # shellcheck disable=SC1091
    source "$repo_root/cli/lib/common.sh"; source "$repo_root/cli/lib/config.sh"; source "$repo_root/cli/commands/status.sh"
    expected_containers() { :; }; gateway_health() { :; }
    "$1"
  )
}

# ---- nothing to do when both open bridges forward
setup 1 1 0
out="$(in_cli ensure_open_network_forwarding 2>&1)" || fail "a healthy host was refused: $out"
[[ -z "$out" ]] || fail "a healthy host produced output: $out"
[[ "$(value_of podman2)$(value_of podman4)$(value_of podman1)" == 110 ]] || fail "a healthy host was changed"

# ---- the defect: ingress off. Only the open bridge is switched on; the internal one stays off.
setup 0 1 0
out="$(in_cli ensure_open_network_forwarding 2>&1)" || fail "the repair failed: $out"
[[ "$(value_of podman2)" == 1 ]] || fail "the ingress bridge was not repaired"
[[ "$(value_of podman1)" == 0 ]] || fail "an INTERNAL bridge was changed: forwarding must stay off there"
grep -q "podman2 (telos-ingress)" <<<"$out" || fail "the repair did not say what it did: $out"

# ---- both open bridges off
setup 0 0 0
in_cli ensure_open_network_forwarding >/dev/null 2>&1 || fail "the repair failed"
[[ "$(value_of podman2)$(value_of podman4)$(value_of podman1)" == 110 ]] || fail "both open bridges must be repaired and the internal one left alone"

# ---- unknown (no file): nothing to repair, nothing to fail
setup - 1 0
in_cli ensure_open_network_forwarding >/dev/null 2>&1 || fail "a bridge without a forwarding file must not fail the start"

# ---- impossible repair is a failure that says how to fix it (root ignores file modes, so only a non-root run can show it)
if [[ "$(id -u)" -ne 0 ]]; then
  setup 0 1 0
  chmod 0444 "$proc/net/ipv4/conf/podman2/forwarding"
  out="$(in_cli ensure_open_network_forwarding 2>&1)" && fail "an impossible repair was reported as success"
  grep -q "sysctl -w net.ipv4.conf.podman2.forwarding=1" <<<"$out" || fail "no instruction for the operator: $out"
  chmod 0644 "$proc/net/ipv4/conf/podman2/forwarding"
fi

# ---- development (rootless, loopback bindings) has no host bridge to forward
setup 0 0 0
in_cli ensure_open_network_forwarding TELOS_DEV=1 >/dev/null 2>&1 || fail "development must skip the check"
[[ "$(value_of podman2)" == 0 ]] || fail "development changed a bridge"

# ---- status reports and never repairs
status() {   # [args] -> the status command's output; its exit status is the command's
  (
    export CLI_DIR="$repo_root/cli" REPO_ROOT="$repo_root" TELOS_NODE_PATH="$work/node" TELOS_PROC_SYS="$proc" NO_COLOR=1
    # shellcheck disable=SC1091
    source "$repo_root/cli/lib/common.sh"; source "$repo_root/cli/lib/config.sh"; source "$repo_root/cli/commands/status.sh"
    expected_containers() { :; }; gateway_health() { :; }
    run "$@"
  ) 2>&1
}
setup 0 1 0
if status_out="$(status --json)"; then fail "status must fail while an open bridge forwards nothing"; fi
python3 -I - "$status_out" <<'PY' || fail "status --json did not report the bridges: $status_out"
import json, sys
network = {row["name"]: row for row in json.loads(sys.argv[1])["network"]}
assert network["telos-ingress"] == {"name": "telos-ingress", "bridge": "podman2", "forwarding": "off"}, network
assert network["telos-egress"]["forwarding"] == "on", network
assert "telos-db" not in network, "internal networks are not reported as open"
PY
[[ "$(value_of podman2)" == 0 ]] || fail "status repaired something; only start may"
if human="$(status)"; then fail "the human report must fail too"; fi
grep -q "forwarding OFF" <<<"$human" || fail "the human report does not flag the bridge: $human"
setup 1 1 0
ok_json="$(status --json)" || fail "status failed on a healthy host: $ok_json"
grep -q '"forwarding":"on"' <<<"$ok_json" || fail "a healthy host's bridges are not reported: $ok_json"

# ---- a lifecycle operation is not ready while an open bridge forwards nothing
ready() {   # STATUS_JSON -> exit status of node_ready
  (
    script_dir="$repo_root/scripts"; release_base="$work/srv"; node_path="$work/node"; state_dir="$work/node/state"
    log() { :; }; die() { exit 2; }
    # shellcheck disable=SC1091
    source "$repo_root/scripts/lib/lifecycle.sh"
    cli() { printf '%s' "$STATUS_JSON"; }
    STATUS_JSON="$1"; node_ready id
  )
}
ready '{"gateway":{"status":"ok"},"network":[{"name":"telos-ingress","bridge":"podman2","forwarding":"on"}]}' || fail "a node with forwarding on must be ready"
ready '{"gateway":{"status":"ok"},"network":[]}' || fail "no bridge facts (development, older CLI) must not block readiness"
ready '{"gateway":{"status":"ok"}}' || fail "an older CLI without the network key must not block readiness"
ready '{"gateway":{"status":"ok"},"network":[{"name":"telos-ingress","bridge":"podman2","forwarding":"off"}]}' && fail "a node whose ingress forwards nothing was called ready"
ready '{"gateway":{"status":"fail"},"network":[]}' && fail "a failing gateway was called ready"

echo "network forwarding: open bridges repaired at start, internal ones never touched, status read-only, readiness requires the path"
