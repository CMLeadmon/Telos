#!/usr/bin/env bash
set -euo pipefail

# The reference-host controller: stages stop only the named service, the probe can
# only ask for the three fixed stages, a failed stage fails the probe instead of
# hanging it, and services are restored on every exit path. Compose is a stub that
# flips marker files the HTTPS fixture node watches.
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
declare -a pids=()
cleanup() { for pid in "${pids[@]:-}"; do kill -KILL "$pid" 2>/dev/null || true; done; rm -rf "$work"; }
trap cleanup EXIT
fail() { echo "browser-node rehearsal test: $*" >&2; exit 1; }
command -v openssl >/dev/null || { echo "not_run: openssl is required" >&2; exit 3; }

# --- fixture CA and node --------------------------------------------------------------------
cd "$work"
printf 'basicConstraints=critical,CA:TRUE\nkeyUsage=critical,keyCertSign,cRLSign\nsubjectKeyIdentifier=hash\n' >ca.ext
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.pem -subj "/CN=Telos Fixture CA" -days 2 \
  -addext "$(sed -n 1p ca.ext)" -addext "$(sed -n 2p ca.ext)" -addext "$(sed -n 3p ca.ext)" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout node.key -out node.csr -subj "/CN=localhost" >/dev/null 2>&1
printf 'subjectAltName=DNS:localhost\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid\n' >node.ext
openssl x509 -req -in node.csr -CA ca.pem -CAkey ca.key -CAcreateserial -out node.pem -days 2 -extfile node.ext >/dev/null 2>&1
cd "$repo_root"

mkdir -p "$work/flags" "$work/stub"
cat >"$work/stub/podman" <<'STUB'
#!/usr/bin/env bash
printf 'podman %s\n' "$*" >>"${STUB_LOG:?}"
case "$1" in
  info) echo false ;;
  compose)
    shift
    verb=""; for a in "$@"; do case "$a" in stop|start) verb="$a"; break ;; esac; done
    [[ -n "$verb" ]] || exit 0
    services=(); seen=0
    for a in "$@"; do [[ "$seen" -eq 1 ]] && services+=("$a"); [[ "$a" == "$verb" ]] && seen=1; done
    for service in "${services[@]}"; do
      if [[ "$verb" == stop ]]; then
        [[ -z "${STUB_STOP_FAILS:-}" || "$service" != "$STUB_STOP_FAILS" ]] || exit 1
        : >"${STUB_FLAGS:?}/$service-down"
      else
        rm -f "${STUB_FLAGS:?}/$service-down"
      fi
    done ;;
esac
exit 0
STUB
printf '#!/usr/bin/env bash\nexit 0\n' >"$work/stub/podman-compose"
chmod +x "$work/stub"/*

# A node.env so compose_with has something to point at (dev node under the work dir).
export TELOS_NODE_PATH="$work/node" TELOS_DEV=1
mkdir -p "$TELOS_NODE_PATH"
printf 'TELOS_ENV=development\n' >"$TELOS_NODE_PATH/node.env"
export STUB_LOG="$work/stub.log" STUB_FLAGS="$work/flags" PATH="$work/stub:$PATH" PODMAN_COMPOSE_PROVIDER="$work/stub/podman-compose" NO_COLOR=1
export TELOS_REHEARSAL_SETTLE_SECONDS=0

commit="$(printf '%040d' 9)"
cat >"$work/manifest.json" <<'JSON'
{"schemaVersion":1,"chatChannel":"general","video":{"id":"v1"},"audio":{"id":"a1"},"ebook":{"id":"book-1","format":"epub"},"upload":{"filename":"smoke.txt","text":"fixture upload bytes"}}
JSON
secret_owner="rehearsal-owner-canary-1234"
credentials() { printf '{"owner":{"username":"owner1","password":"%s"},"member":{"username":"member1","password":"rehearsal-member-pass-77"},"bootstrapToken":"fixture-bootstrap-token"}' "$secret_owner"; }

start_node() {
  rm -f "$work/port" "$work/flags"/*
  python3 "$repo_root/tests/helpers/fake_node_server.py" "$work/node.pem" "$work/node.key" "$work/port" --flag-dir "$work/flags" >/dev/null 2>&1 &
  node_pid=$!; pids+=("$node_pid")
  for _ in $(seq 1 100); do [[ -s "$work/port" ]] && break; sleep 0.05; done
  port="$(cat "$work/port")"
  : >"$STUB_LOG"
}
stop_node() { kill -KILL "$node_pid" 2>/dev/null || true; wait "$node_pid" 2>/dev/null || true; }
rehearse() { # OUT_DIR [extra args]
  local out="$1"; shift
  rm -rf "$out"; mkdir -p "$out"
  SSL_CERT_FILE="$work/ca.pem" "$repo_root/scripts/rehearse-browser-node.sh" --public-url "https://localhost:$port" \
    --rehearsal --source-commit "$commit" --result-out "$out/result.json" --fixture-manifest "$work/manifest.json" "$@"
}

# --- the full journey: ordered stages, nothing else touched ------------------------------------
start_node
set +e; credentials | rehearse "$work/out-ok" --bootstrap-empty-node >"$work/ok.out" 2>&1; status=$?; set -e
[[ $status -eq 0 ]] || fail "the controlled journey exited $status: $(cat "$work/ok.out")"
python3 "$repo_root/scripts/lib/release_graph.py" validate --kind gate-result "$work/out-ok/result.json" >/dev/null
mapfile -t compose_calls < <(grep -E ' (stop|start) ' "$STUB_LOG" | sed -E 's/.* (stop|start) /\1 /')
expected=("stop jellyfin" "start jellyfin" "stop grimmory" "start jellyfin grimmory")
[[ "${compose_calls[*]}" == "${expected[*]}" ]] || fail "stage order was: ${compose_calls[*]}"
! grep -E ' (stop|start|rm|down|kill) .*(postgres|redis|telos-core|traefik|telos-client)' "$STUB_LOG" >/dev/null || fail "a stage touched an unrelated service"
! grep -E ' down|volume' "$STUB_LOG" >/dev/null || fail "the controller removed something"
[[ -z "$(ls -A "$work/flags")" ]] || fail "services were left down after a successful rehearsal"
! grep -q "$secret_owner" "$work/ok.out" "$work/out-ok"/* || fail "a credential leaked"
stop_node

# --- a stage that cannot be performed fails the probe, never hangs it ------------------------------
start_node
set +e; credentials | STUB_STOP_FAILS=grimmory rehearse "$work/out-stopfail" --bootstrap-empty-node >"$work/sf.out" 2>&1; status=$?; set -e
[[ $status -eq 1 ]] || fail "a failed stage must fail the journey (exit 1), got $status: $(cat "$work/sf.out")"
python3 - "$work/out-stopfail/report.json" <<'PY'
import json, sys
statuses = {a["id"]: a["status"] for a in json.load(open(sys.argv[1]))["assertions"]}
assert statuses["outage.jellyfin"] == "passed", statuses
assert statuses["outage.grimmory"] == "failed", statuses
PY
[[ -z "$(ls -A "$work/flags")" ]] || fail "services were left down after a failed stage"
stop_node

# --- an interrupted rehearsal restores the services -----------------------------------------------------
start_node
credentials | TELOS_REHEARSAL_SETTLE_SECONDS=30 rehearse "$work/out-int" --bootstrap-empty-node >"$work/int.out" 2>&1 &
controller=$!; pids+=("$controller")
for _ in $(seq 1 200); do [[ -e "$work/flags/jellyfin-down" ]] && break; sleep 0.05; done
[[ -e "$work/flags/jellyfin-down" ]] || fail "the outage stage never started"
kill -TERM "$controller" 2>/dev/null || true
wait "$controller" 2>/dev/null || true
for _ in $(seq 1 100); do [[ -z "$(ls -A "$work/flags")" ]] && break; sleep 0.05; done
[[ -z "$(ls -A "$work/flags")" ]] || fail "an interrupted rehearsal left Jellyfin down"
grep -q ' start jellyfin grimmory' "$STUB_LOG" || fail "the EXIT trap did not restart the services"
stop_node

# --- the probe cannot name its own stage or smuggle a flag ----------------------------------------------------
set +e
credentials | "$repo_root/scripts/rehearse-browser-node.sh" --stage-pipe /tmp/x >"$work/sp.out" 2>&1
[[ $? -eq 2 ]] || fail "--stage-pipe must be controller-owned"
set -e
start_node
cat >"$work/rogue-probe.sh" <<'ROGUE'
#!/usr/bin/env bash
# A probe that asks for an arbitrary stage must be refused with FAIL, not obeyed.
base=""; while [[ $# -gt 0 ]]; do [[ "$1" == --stage-pipe ]] && base="$2"; shift; done
printf 'STAGE rm -rf /\n' >"$base.out"
read -r reply <"$base.in"
[[ "$reply" == FAIL ]] || exit 7
exit 0
ROGUE
chmod +x "$work/rogue-probe.sh"
set +e
credentials | TELOS_REHEARSAL_SMOKE="$work/rogue-probe.sh" rehearse "$work/out-rogue" >"$work/rogue.out" 2>&1
rogue_status=${PIPESTATUS[1]}
set -e
[[ $rogue_status -eq 0 ]] || fail "the controller obeyed or hung on an arbitrary stage name (exit $rogue_status): $(cat "$work/rogue.out")"
! grep -E ' (stop|start) ' "$STUB_LOG" >/dev/null || fail "an arbitrary stage ran a compose action"
stop_node

echo "browser-node rehearsal: ordered single-service outages, FAIL on error, restore on every exit, fixed stage vocabulary"
