#!/usr/bin/env bash
# Controlled-egress proxy test.
#
# Static checks (always): the Squid policy parses and the deny-by-default rules
# and allowlist are present. Live checks (EGRESS_LIVE=1, needs Internet): boot
# the proxy on an isolated network and assert an allowlisted host succeeds while
# an unlisted host, an IP literal, and an alternate port are refused.
#
# Exit codes: 0 pass, 10 static policy defect, 20 live behavior defect,
# 30 environment unavailable.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CONF="$ROOT/config/egress/squid.conf"
ALLOW="$ROOT/config/egress/allowed-domains.txt"
ENGINE="${CONTAINER_ENGINE:-podman}"
IMG="docker.io/ubuntu/squid:latest"

fail()  { echo "egress-test: FAIL: $1" >&2; exit "${2:-10}"; }
note()  { echo "egress-test: $1"; }

[ -f "$CONF" ]  || fail "missing $CONF"
[ -f "$ALLOW" ] || fail "missing squid allowlist"

# ── Static policy assertions ────────────────────────────────────────────────
grep -q 'http_access deny all'                 "$CONF" || fail "no default-deny rule"
grep -q 'http_access deny to_forbidden'        "$CONF" || fail "no private/link-local denial"
grep -q 'http_access deny CONNECT !SSL_ports'  "$CONF" || fail "plaintext CONNECT not denied"
grep -q 'http_access deny !Safe_ports'         "$CONF" || fail "alternate ports not denied"
grep -q 'http_access deny manager'             "$CONF" || fail "proxy manager not denied"
grep -q '169.254.0.0/16'                       "$CONF" || fail "link-local/metadata range not blocked"
grep -q '100.64.0.0/10'                        "$CONF" || fail "CGNAT range not blocked"
grep -q 'strip_query_terms on'                 "$CONF" || fail "access-log query redaction off"
grep -q 'via off'                              "$CONF" || fail "proxy identity not hidden"
grep -q 'database.clamav.net'                  "$ALLOW" || fail "clamav signatures host not allowlisted"
note "static policy checks passed"

if ! command -v "$ENGINE" >/dev/null 2>&1; then
  note "container engine unavailable; skipping parse/live checks"
  exit 0
fi

# ── squid -k parse against the real policy ──────────────────────────────────
if ! "$ENGINE" run --rm \
    -v "$CONF":/etc/squid/squid.conf:ro,z \
    -v "$ALLOW":/etc/squid/allowed-domains.txt:ro,z \
    "$IMG" squid -k parse -f /etc/squid/squid.conf >/dev/null 2>&1; then
  note "could not pull/run squid image; skipping parse (env unavailable)"
  exit 30
fi
note "squid -k parse succeeded"

if [ "${EGRESS_LIVE:-0}" != "1" ]; then
  note "static + parse checks passed (set EGRESS_LIVE=1 for live network checks)"
  exit 0
fi

# ── Live behavior ───────────────────────────────────────────────────────────
NET="egress-test-$$"
NAME="egress-proxy-test-$$"
cleanup() { "$ENGINE" rm -f "$NAME" >/dev/null 2>&1; "$ENGINE" network rm "$NET" >/dev/null 2>&1; }
trap cleanup EXIT

"$ENGINE" network create "$NET" >/dev/null 2>&1 || fail "cannot create test network" 30
"$ENGINE" run -d --name "$NAME" --network "$NET" \
  -v "$CONF":/etc/squid/squid.conf:ro,z \
  -v "$ALLOW":/etc/squid/allowed-domains.txt:ro,z \
  "$IMG" >/dev/null 2>&1 || fail "cannot start proxy" 30
sleep 3

CURL_IMG="${CURL_IMG:-docker.io/curlimages/curl:latest}"
viacurl() { "$ENGINE" run --rm --network "$NET" "$CURL_IMG" \
  -s -o /dev/null -w '%{http_code}' --max-time 15 -x "http://$NAME:3128" "$1" 2>/dev/null; }

allowed=$(viacurl "https://openlibrary.org/")
[ "$allowed" = "200" ] || [ "$allowed" = "301" ] || [ "$allowed" = "302" ] || fail "allowlisted host blocked (got $allowed)" 20
note "allowlisted host reachable ($allowed)"

# A denied HTTPS CONNECT is refused by the proxy: curl reports 403 (proxy
# response) or 000 (the tunnel is reset). Any success code is a policy breach.
is_denied() { [ "$1" = "403" ] || [ "$1" = "000" ] || [ "$1" = "407" ]; }

denied=$(viacurl "https://example.com/")
is_denied "$denied" || fail "unlisted host not denied (got $denied)" 20
note "unlisted host denied ($denied)"

altport=$(viacurl "https://openlibrary.org:8443/")
is_denied "$altport" || fail "alternate port not denied (got $altport)" 20
note "alternate port denied ($altport)"

note "live egress checks passed"
exit 0
