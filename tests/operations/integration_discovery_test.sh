#!/usr/bin/env bash
set -euo pipefail

# Process-level proof that `telos-core validate-integrations` runs with no database,
# Redis or signing keys, reads one JSON object on stdin, talks to the configured
# upstreams and prints only non-secret library lists. Builds the real binary with a
# local Go toolchain, or the pinned golang image when only Podman exists.
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
server_pid=""
cleanup() { [[ -z "$server_pid" ]] || kill "$server_pid" 2>/dev/null || true; rm -rf "$work"; }
trap cleanup EXIT

binary="$work/telos-core"
if command -v go >/dev/null 2>&1; then
  (cd "$repo_root/backend" && CGO_ENABLED=0 go build -o "$binary" .)
elif command -v podman >/dev/null 2>&1; then
  podman run --rm -v "$repo_root/backend:/app:z" -v "$work:/out:z" -w /app -e CGO_ENABLED=0 \
    docker.io/library/golang:1.26.5 go build -o /out/telos-core .
else
  echo "not_run: a Go toolchain or Podman is required to build telos-core" >&2
  exit 3
fi

cat >"$work/upstreams.py" <<'PY'
import json, os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

# A fresh Jellyfin: it serves from the root, and its saved Base URL is recorded so
# the test can prove setup stored /jellyfin.
SAVED = os.path.join(os.path.dirname(os.path.abspath(__file__)), "network.saved")

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def send_json(self, value, status=200):
        data = json.dumps(value).encode()
        self.send_response(status); self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data))); self.end_headers(); self.wfile.write(data)
    def do_GET(self):
        if self.path == "/System/Configuration/network":
            if self.headers.get("X-Emby-Token") != "jf-token": return self.send_json({}, 401)
            return self.send_json({"BaseUrl": "", "InternalHttpPort": 8096})
        if self.path == "/Library/VirtualFolders":
            if self.headers.get("X-Emby-Token") != "jf-token": return self.send_json({}, 401)
            return self.send_json([{"Name": "Movies", "ItemId": "m1"}, {"Name": "Music", "ItemId": "m2"}])
        if self.path == "/api/v1/libraries":
            if self.headers.get("Authorization") != "Bearer session": return self.send_json({}, 401)
            return self.send_json([{"id": 4, "name": "Books"}])
        self.send_json({}, 404)
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        if self.path == "/System/Configuration/network":
            if self.headers.get("X-Emby-Token") != "jf-token": return self.send_json({}, 401)
            with open(SAVED, "w") as handle: json.dump(body, handle)
            self.send_response(204); self.end_headers(); return
        if self.path == "/api/v1/auth/login" and body == {"username": "gw", "password": "gw-pass"}:
            return self.send_json({"accessToken": "session", "expires": 7200})
        self.send_json({}, 401)

server = HTTPServer(("127.0.0.1", 0), Handler)
print(server.server_port, flush=True)
server.serve_forever()
PY
python3 "$work/upstreams.py" >"$work/port" &
server_pid=$!
for _ in $(seq 1 50); do [[ -s "$work/port" ]] && break; sleep 0.1; done
port="$(cat "$work/port")"

request='{"jellyfinToken":"jf-token","grimmoryUser":"gw","grimmoryPassword":"gw-pass"}'
# Hostile environment: nothing the gateway normally requires is present.
output="$(env -i PATH="$PATH" JELLYFIN_URL="http://127.0.0.1:$port" GRIMMORY_URL="http://127.0.0.1:$port" \
  "$binary" validate-integrations <<<"$request" 2>"$work/stderr")"
python3 - "$output" <<'PY'
import json, sys
response = json.loads(sys.argv[1])
assert response == {"jellyfin": [{"id": "m1", "name": "Movies"}, {"id": "m2", "name": "Music"}],
                    "grimmory": [{"id": "4", "name": "Books"}]}, response
PY
python3 - "$work/network.saved" <<'PY'
import json, sys
saved = json.load(open(sys.argv[1]))
assert saved == {"BaseUrl": "/jellyfin", "InternalHttpPort": 8096}, saved
PY
[[ ! -s "$work/stderr" ]] || { echo "unexpected stderr: $(cat "$work/stderr")" >&2; exit 1; }
for secret in jf-token gw-pass session; do
  ! grep -q "$secret" <<<"$output" || { echo "stdout leaked $secret" >&2; exit 1; }
done

set +e
failure="$(env -i PATH="$PATH" JELLYFIN_URL="http://127.0.0.1:$port" GRIMMORY_URL="http://127.0.0.1:$port" \
  "$binary" validate-integrations <<<'{"jellyfinToken":"wrong","grimmoryUser":"gw","grimmoryPassword":"gw-pass"}' 2>"$work/failure.err")"
status=$?
set -e
[[ $status -eq 1 && -z "$failure" ]] || { echo "a rejected token must exit 1 with empty stdout (got $status)" >&2; exit 1; }
grep -q 'jellyfin_rejected' "$work/failure.err" || { echo "missing fixed error code" >&2; exit 1; }
! grep -q 'wrong' "$work/failure.err" || { echo "stderr echoed the supplied token" >&2; exit 1; }

set +e
"$binary" validate-integrations --extra <<<"$request" >/dev/null 2>&1
[[ $? -eq 2 ]] || { echo "an extra argument must be a usage error" >&2; exit 1; }
set -e
echo "validate-integrations: dispatched before startup, prints only non-secret libraries, fixed errors"
