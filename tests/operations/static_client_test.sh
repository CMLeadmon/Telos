#!/usr/bin/env bash
set -euo pipefail

# Image-content, identity and network contract for the standalone static client.
# Builds a throwaway tag from frontend/Dockerfile, so it needs Podman. A missing
# runtime is not_run (exit 3): nothing was built or judged.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if ! command -v podman >/dev/null 2>&1; then
  echo "not_run: podman is required to build and run the static client image" >&2
  exit 3
fi

image="localhost/telos-client-phase2:$RANDOM$RANDOM"
cid=""
work="$(mktemp -d)"
cleanup() {
  [[ -z "$cid" ]] || podman rm -f "$cid" >/dev/null 2>&1 || true
  podman rmi "$image" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

podman build -f frontend/Dockerfile -t "$image" frontend

# Same hardening Compose applies, with tmpfs owned by the unprivileged worker.
cid="$(podman run -d --read-only --cap-drop=all --security-opt=no-new-privileges \
  --tmpfs /var/cache/nginx:size=16m,mode=1777 \
  --tmpfs /var/run:size=1m,mode=1777 \
  -p 127.0.0.1::8080 "$image")"

uid="$(podman exec "$cid" id -u)"
[[ "$uid" != 0 ]] || { echo "client runs as root" >&2; exit 1; }
[[ "$(podman inspect --format '{{.Config.User}}' "$image")" == "101:101" ]] || { echo "image does not declare USER 101:101" >&2; exit 1; }

port="$(podman port "$cid" 8080/tcp | sed 's/.*://')"
base="http://127.0.0.1:$port"
for _ in $(seq 1 100); do
  curl -fsS "$base/healthz" >/dev/null 2>&1 && break
  sleep 0.1
done
[[ "$(curl -fsS "$base/healthz")" == "ok" ]] || { echo "/healthz did not answer ok" >&2; exit 1; }

root_html="$(curl -fsS "$base/")"
grep -q '<html' <<<"$root_html" || { echo "root did not serve HTML" >&2; exit 1; }
asset_path="$(grep -oE '/_next/static/[A-Za-z0-9._/?=&%-]+' <<<"$root_html" | head -n 1)"
[[ -n "$asset_path" ]] || { echo "no hashed asset referenced from the root page" >&2; exit 1; }
curl -fsSI "$base$asset_path" | grep -qi 'cache-control:.*immutable' || { echo "hashed asset is not immutable" >&2; exit 1; }
curl -fsSI "$base/" | grep -qi 'cache-control:.*no-store' || { echo "HTML is cacheable" >&2; exit 1; }

for route in /login/ /stream/; do
  [[ "$(curl -s -o /dev/null -w '%{http_code}' "$base$route")" == 200 ]] || { echo "$route did not resolve" >&2; exit 1; }
done
[[ "$(curl -s -o /dev/null -w '%{http_code}' "$base/api/v1/health")" == 404 ]] || { echo "unknown API path was answered with 200" >&2; exit 1; }
[[ "$(curl -s -o /dev/null -w '%{http_code}' "$base/definitely-missing")" == 404 ]] || { echo "missing path is not a 404" >&2; exit 1; }

# A multi-stage history proves nothing about the final filesystem: inspect both.
podman history --no-trunc "$image" >"$work/history.txt"
if grep -Eiq 'node.env|telos-secrets|\.env\b' "$work/history.txt"; then
  echo "image history references secret or environment files" >&2
  exit 1
fi
exported="$(podman create "$image")"
podman export "$exported" >"$work/rootfs.tar"
podman rm "$exported" >/dev/null
tar -tf "$work/rootfs.tar" >"$work/files.txt"
forbidden="$(grep -E '^usr/share/nginx/html/.*(/|^)(\.env[^/]*|node\.env|telos-secrets\.json|node_modules)(/|$)|^usr/share/nginx/html/.*\.map$|^usr/share/nginx/html/(src|\.next)(/|$)' "$work/files.txt" || true)"
[[ -z "$forbidden" ]] || { echo "forbidden files in the static root:" >&2; echo "$forbidden" >&2; exit 1; }
if grep -Eq '^(app|usr/lib/node_modules|root/\.npm)(/|$)' "$work/files.txt"; then
  echo "build-stage Node content leaked into the runtime image" >&2
  exit 1
fi

# The image must not publish anything but the declared port.
ports="$(podman inspect --format '{{range $p, $_ := .Config.ExposedPorts}}{{$p}} {{end}}' "$image")"
[[ "$ports" == "8080/tcp " ]] || { echo "unexpected exposed ports: $ports" >&2; exit 1; }

echo "static client image: non-root, read-only, health-checked, immutable assets, clean filesystem"
