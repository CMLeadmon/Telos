#!/usr/bin/env bash
set -euo pipefail

# `telos doctor` must notice an ingress bridge that does not forward. Loopback and
# every container health check still pass in that state, but published ports 80/443
# time out for any other machine -- observed on a real Podman host after a restart.
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/conf/podman1"

cat >"$work/bin/podman" <<'SH'
#!/usr/bin/env bash
[[ "$1 $2" == "network inspect" ]] && { printf '%s\n' "${FAKE_BRIDGE:-podman1}"; exit 0; }
exit 1
SH
chmod +x "$work/bin/podman"
export PATH="$work/bin:$PATH" TELOS_PROC_IPV4_CONF="$work/conf"

check() { # DEV(0|1) [FAKE_BRIDGE]
  (
    export CLI_DIR="$repo_root/cli" REPO_ROOT="$repo_root" TELOS_DEV="$1"
    [[ -n "${2:-}" ]] && export FAKE_BRIDGE="$2"
    # shellcheck disable=SC1091
    source "$repo_root/cli/lib/common.sh"; source "$repo_root/cli/lib/config.sh"; source "$repo_root/cli/commands/doctor.sh"
    DOCTOR_PROBLEMS=0; DOCTOR_FIXES=()
    check_ingress_forwarding podman
    echo "problems=$DOCTOR_PROBLEMS fixes=${DOCTOR_FIXES[*]:-}"
  )
}
fail() { echo "doctor_ingress_test: $*" >&2; exit 1; }

echo 1 >"$work/conf/podman1/forwarding"
out="$(check 0)"; grep -q 'problems=0' <<<"$out" || fail "a forwarding bridge was reported as a problem: $out"
grep -q 'forwards packets' <<<"$out" || fail "the healthy case should say so: $out"

echo 0 >"$work/conf/podman1/forwarding"
out="$(check 0)"; grep -q 'problems=1' <<<"$out" || fail "a non-forwarding ingress bridge went unnoticed: $out"
grep -q 'telos stop' <<<"$out" || fail "the repair command was not given: $out"

out="$(check 1)"; grep -q 'problems=0' <<<"$out" || fail "development publishes on loopback and must not be judged by the bridge: $out"
rm -f "$work/conf/podman1/forwarding"
out="$(check 0)"; grep -q 'problems=0' <<<"$out" || fail "an unreadable setting must not fabricate a defect: $out"
echo "doctor ingress forwarding: healthy, broken, development and unreadable all classified"
