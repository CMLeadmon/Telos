#!/usr/bin/env bash
set -euo pipefail

# `telos status` asks the gateway for its health the way a browser would reach it:
# through Traefik by the node's domain in production, on loopback :8080 in
# development. Production publishes no gateway port, so the earlier loopback-only
# probe reported a healthy node as "telos-core is not serving".
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/node"

# curl shim: records its argv; answers per $CURL_BODY / $CURL_FAIL.
cat >"$work/bin/curl" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$CURL_LOG"
[[ "${CURL_FAIL:-0}" == 1 ]] && exit 7
printf '%s' "${CURL_BODY:-}"
SH
chmod +x "$work/bin/curl"
export PATH="$work/bin:$PATH" CURL_LOG="$work/curl.log"
printf "TELOS_DOMAIN=telos.test\n" >"$work/node/node.env"

probe() { # MODE [env...] — print the health body the status command would use
  (
    export CLI_DIR="$repo_root/cli" REPO_ROOT="$repo_root" TELOS_NODE_PATH="$work/node"
    [[ "$1" == dev ]] && export TELOS_DEV=1 || export TELOS_DEV=0
    shift
    for kv in "$@"; do export "$kv"; done
    # shellcheck disable=SC1091
    source "$repo_root/cli/lib/common.sh"; source "$repo_root/cli/lib/config.sh"; source "$repo_root/cli/commands/status.sh"
    node_path() { printf '%s' "$work/node"; }
    gateway_health
  )
}
fail() { echo "status_gateway_probe_test: $*" >&2; exit 1; }

: >"$CURL_LOG"
body="$(probe prod CURL_BODY='{"status":"fail","checks":[{"name":"jellyfin","status":"fail"}]}')" || fail "a 503 body must still be returned"
[[ "$body" == '{"status":"fail"'* ]] || fail "production probe lost the body: $body"
grep -q -- '--resolve telos.test:443:127.0.0.1' "$CURL_LOG" || fail "production must pin the domain to this machine: $(cat "$CURL_LOG")"
grep -q 'https://telos.test:443/api/v1/health' "$CURL_LOG" || fail "production must use the public front door: $(cat "$CURL_LOG")"
! grep -q '127.0.0.1:8080' "$CURL_LOG" || fail "production must not probe a loopback gateway port"

: >"$CURL_LOG"
probe prod CURL_BODY='<html>bad gateway</html>' && fail "a non-JSON answer is not a health report"
probe prod CURL_FAIL=1 && fail "an unreachable front door must be reported as unreachable"

: >"$CURL_LOG"
probe dev CURL_BODY='{"status":"ok"}' >/dev/null || fail "development probe failed"
grep -q 'http://127.0.0.1:8080/api/v1/health' "$CURL_LOG" || fail "development must probe loopback :8080"

: >"$CURL_LOG"
probe prod TELOS_HEALTH_URL=http://example.invalid/h CURL_BODY='{"status":"ok"}' >/dev/null || fail "override probe failed"
grep -q 'http://example.invalid/h' "$CURL_LOG" || fail "TELOS_HEALTH_URL must be honoured"
echo "status gateway probe: production by domain through Traefik, development on loopback, override honoured"
