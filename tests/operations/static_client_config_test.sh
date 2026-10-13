#!/usr/bin/env bash
set -euo pipefail

# Behavioral contract of frontend/nginx.conf, run against a real nginx binary and a
# synthetic export so it needs neither a container runtime nor a prior build.
# Exit 3 (not_run) when no nginx binary is available: nothing was tested.
repo_root="$(git rev-parse --show-toplevel)"
nginx_bin="${TELOS_NGINX:-$(command -v nginx || true)}"
if [[ -z "$nginx_bin" || ! -x "$nginx_bin" ]]; then
  echo "not_run: an nginx binary is required (set TELOS_NGINX or install nginx)" >&2
  exit 3
fi

work="$(mktemp -d)"
nginx_pid=""
cleanup() {
  if [[ -n "$nginx_pid" ]] && kill -0 "$nginx_pid" 2>/dev/null; then
    kill "$nginx_pid" 2>/dev/null || true
    wait "$nginx_pid" 2>/dev/null || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

site="$work/site"
mkdir -p "$site/_next/static/chunks" "$site/login" "$site/stream" "$work/cache" "$work/run"
printf '<!doctype html><html><body>home</body></html>' >"$site/index.html"
printf '<!doctype html><html><body>login</body></html>' >"$site/login/index.html"
printf '<!doctype html><html><body>stream</body></html>' >"$site/stream/index.html"
printf '<!doctype html><html><body>not found page</body></html>' >"$site/404.html"
printf 'console.log("chunk")' >"$site/_next/static/chunks/app-1a2b3c.js"

mime=""
for candidate in /etc/nginx/mime.types "$(dirname "$nginx_bin")/../../etc/nginx/mime.types"; do
  [[ -f "$candidate" ]] && { mime="$candidate"; break; }
done
[[ -n "$mime" ]] || { echo "cannot locate mime.types for $nginx_bin" >&2; exit 1; }
port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')"

# The shipped config's only writable locations are /var/run and /var/cache/nginx.
python3 - "$repo_root/frontend/nginx.conf" <<'PY'
import re
import sys

text = open(sys.argv[1], encoding="utf-8").read()
paths = set(re.findall(r"(?:pid|_temp_path)\s+(/[^\s;]+)", text))
assert paths, "no writable paths declared"
for path in paths:
    assert path.startswith(("/var/run/", "/var/cache/nginx/")), f"writable path outside tmpfs: {path}"
assert "listen 8080" in text
assert "try_files $uri $uri/index.html $uri.html =404" in text
assert "index.html;" not in text.replace("$uri/index.html", ""), "must not fall back to index.html"
PY

sed -e "s#/var/run/nginx.pid#$work/run/nginx.pid#" \
    -e "s#/var/cache/nginx#$work/cache#g" \
    -e "s#/usr/share/nginx/html#$site#" \
    -e "s#/etc/nginx/mime.types#$mime#" \
    -e "s#listen 8080 default_server#listen 127.0.0.1:$port default_server#" \
    -e "s#listen \[::\]:8080 default_server;##" \
    -e "s#error_log /dev/stderr warn#error_log $work/error.log warn#" \
    "$repo_root/frontend/nginx.conf" >"$work/nginx.conf"

"$nginx_bin" -t -p "$work" -c "$work/nginx.conf" >"$work/test.out" 2>&1 || { cat "$work/test.out" >&2; exit 1; }
"$nginx_bin" -p "$work" -c "$work/nginx.conf" -g "daemon off;" >"$work/run.out" 2>&1 &
nginx_pid=$!
for _ in $(seq 1 100); do
  curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 && break
  kill -0 "$nginx_pid" 2>/dev/null || { cat "$work/run.out" "$work/error.log" >&2; exit 1; }
  sleep 0.05
done

base="http://127.0.0.1:$port"
fetch() { curl -sS -o "$work/body" -D "$work/headers" -w '%{http_code}' "$@"; }
header() { tr -d '\r' <"$work/headers" | awk -v name="$1" 'BEGIN{IGNORECASE=1} tolower($0) ~ "^" tolower(name) ":" {sub(/^[^:]*: */, ""); print; exit}'; }
expect() { [[ "$1" == "$2" ]] || { echo "expected $2, got $1: $3" >&2; exit 1; }; }

expect "$(fetch "$base/healthz")" 200 "/healthz"
[[ "$(cat "$work/body")" == "ok" ]] || { echo "/healthz body is not ok" >&2; exit 1; }
[[ "$(header Content-Type)" == text/plain* ]]
[[ "$(header Cache-Control)" == "no-store" ]]

for route in / /login/ /stream/ /stream; do
  expect "$(fetch "$base$route")" 200 "$route"
  grep -q '<html' "$work/body" || { echo "$route did not serve HTML" >&2; exit 1; }
  [[ "$(header Cache-Control)" == "no-store" ]] || { echo "HTML for $route is cacheable" >&2; exit 1; }
done

expect "$(fetch "$base/_next/static/chunks/app-1a2b3c.js")" 200 "hashed asset"
[[ "$(header Cache-Control)" == *immutable* && "$(header Cache-Control)" == *max-age=31536000* ]] || { echo "hashed asset is not immutable" >&2; exit 1; }
[[ "$(header Content-Type)" == *javascript* ]]

for missing in /nothing-here /api/v1/health /api/v1/users/me /_next/static/missing.js /login/missing; do
  expect "$(fetch "$base$missing")" 404 "$missing must be a real 404"
  grep -q 'not found page' "$work/body" || { echo "$missing did not use /404.html" >&2; exit 1; }
  grep -q '>home<' "$work/body" && { echo "$missing was answered with index.html" >&2; exit 1; }
done
[[ "$(header Cache-Control)" == "no-store" ]]

traversal_status="$(fetch --path-as-is "$base/../etc/passwd")"
[[ "$traversal_status" == 400 || "$traversal_status" == 404 ]] || { echo "traversal answered $traversal_status" >&2; exit 1; }
expect "$(fetch -I "$base/login/")" 200 "HEAD"
[[ "$(header Server)" != *"/"* ]] || { echo "server version is advertised: $(header Server)" >&2; exit 1; }
[[ "$(header X-Content-Type-Options)" == nosniff ]]

# When a real export exists, prove every product route resolves from it.
real_site="$repo_root/frontend/out"
if [[ -f "$real_site/index.html" ]]; then
  kill "$nginx_pid" 2>/dev/null || true
  wait "$nginx_pid" 2>/dev/null || true
  sed -e "s#root $site;#root $real_site;#" "$work/nginx.conf" >"$work/nginx-real.conf"
  "$nginx_bin" -p "$work" -c "$work/nginx-real.conf" -g "daemon off;" >"$work/run-real.out" 2>&1 &
  nginx_pid=$!
  for _ in $(seq 1 100); do
    curl -fsS "$base/healthz" >/dev/null 2>&1 && break
    sleep 0.05
  done
  for route in / /login/ /connect/ /chat/ /stream/ /library/ /files/ /settings/; do
    expect "$(fetch "$base$route")" 200 "real export $route"
    grep -q '<html' "$work/body" || { echo "real export $route did not serve HTML" >&2; exit 1; }
  done
  asset="$(cd "$real_site" && find _next/static -type f -name '*.js' | sort | head -1)"
  expect "$(fetch "$base/$asset")" 200 "real asset"
  [[ "$(header Cache-Control)" == *immutable* ]] || { echo "real asset is not immutable" >&2; exit 1; }
  expect "$(fetch "$base/api/v1/health")" 404 "real export must not answer API paths"
  echo "static client config: real export routes verified"
fi

echo "static client config: routing, caching and 404 behavior verified with $("$nginx_bin" -v 2>&1)"
