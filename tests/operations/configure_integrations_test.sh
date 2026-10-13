#!/usr/bin/env bash
set -euo pipefail

# Behavior of `telos configure integrations`: loopback-only setup overlay, hidden
# prompts under a real PTY, stdin-only validation requests, atomic node.env update
# and guaranteed teardown. A stub `podman` simulates the runtime (including real
# loopback listeners for the setup ports) so nothing here needs containers.
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
declare -a background_pids=()
cleanup() {
  for pid in "${background_pids[@]:-}"; do kill -KILL "$pid" 2>/dev/null || true; done
  for pidfile in "$work"/state/listener.*.pid; do
    [[ -f "$pidfile" ]] && kill -KILL "$(cat "$pidfile")" 2>/dev/null || true
  done
  chmod -R u+rwX "$work" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT
fail() { echo "configure test: $*" >&2; exit 1; }

# --- the overlay itself -----------------------------------------------------------
python3 - "$repo_root/docker-compose.setup.yml" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
services = doc["services"]
assert set(services) == {"telos-setup-proxy"}, services
relay = services["telos-setup-proxy"]
# Only the relay publishes, and only on loopback; the upstream UIs publish nothing
# (a container on an internal network cannot have a working published port).
assert relay["ports"] == ["127.0.0.1:${JELLYFIN_SETUP_PORT:-8096}:8096", "127.0.0.1:${GRIMMORY_SETUP_PORT:-6060}:6060"], relay["ports"]
assert set(relay["networks"]) == {"telos-backend", "telos-setup"}, relay["networks"]
assert doc["networks"]["telos-setup"].get("internal") in (None, False), "the setup network must be an ordinary bridge"
assert set(doc) == {"networks", "services"}, "the overlay may only add the relay and its network"
PY
python3 - "$repo_root/docker-compose.yml" <<'PY'
import sys, yaml
services = yaml.safe_load(open(sys.argv[1]))["services"]
for name in ("jellyfin", "grimmory"):
    assert "ports" not in services[name], f"{name} publishes a port in the production model"
PY

# --- skeleton -----------------------------------------------------------------------
skeleton="$work/skeleton"
mkdir -p "$skeleton/stub" "$work/state"
cp -r "$repo_root/cli" "$skeleton/cli"
cp "$repo_root/telos" "$repo_root/.env.example" "$repo_root/docker-compose.yml" \
   "$repo_root/docker-compose.dev.yml" "$repo_root/docker-compose.setup.yml" "$skeleton/"

cat >"$skeleton/stub/openssl" <<'EOF'
#!/usr/bin/env bash
state="${OPENSSL_STATE:?}"
count=$(( $(cat "$state" 2>/dev/null || echo 0) + 1 )); echo "$count" >"$state"
seed="$(printf 'telos-configure-%s' "$count" | sha256sum | cut -d' ' -f1)"
case "$2" in
  -base64) printf '%s' "$seed" | xxd -r -p | head -c 32 | base64 ;;
  -hex)    printf '%s\n' "${seed:0:48}" ;;
esac
EOF
cat >"$skeleton/stub/podman" <<'EOF'
#!/usr/bin/env bash
# Runtime stub for configure tests: logs every call and simulates containers,
# loopback listeners and the validation container.
state="${STUB_STATE:?}"
printf 'podman %s\n' "$*" >>"${STUB_LOG:?}"
container_for() {
  case "$1" in
    jellyfin) echo telos-jellyfin ;; grimmory-db) echo telos-grimmory-db ;;
    grimmory) echo telos-grimmory ;; *) echo "$1" ;;
  esac
}
start_listener() {
  local port="$1"
  [[ -e "$state/listener.$port.pid" ]] && return 0
  python3 - "$port" "$state/listener.$port.pid" <<'PY' &
import os, socket, sys, time
port, pidfile = int(sys.argv[1]), sys.argv[2]
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", port)); s.listen(5)
open(pidfile, "w").write(str(os.getpid()))
time.sleep(600)
PY
  for _ in $(seq 1 50); do [[ -s "$state/listener.$port.pid" ]] && break; sleep 0.05; done
}
case "$1" in
  info) echo false ;;
  ps) cat "$state/containers" 2>/dev/null || true ;;
  rm)
    # Direct removal by name (podman-compose has no `rm`). Fails like the real thing when
    # the test asks for a removal failure, and leaves the listener alive when asked.
    [[ "${STUB_RM_FAILS:-0}" == 1 ]] && exit 1
    : >"$state/containers"
    if [[ "${STUB_RM_LEAVES_LISTENER:-0}" != 1 ]]; then
      for pidfile in "$state"/listener.*.pid; do
        [[ -f "$pidfile" ]] && kill -KILL "$(cat "$pidfile")" 2>/dev/null && rm -f "$pidfile"
      done
    fi ;;
  network) ;;
  compose)
    shift
    verb=""
    for arg in "$@"; do case "$arg" in build|up|rm|run) verb="$arg"; break ;; esac; done
    case "$verb" in
      build) ;;
      up)
        services=(); seen_up=0
        for arg in "$@"; do
          [[ "$seen_up" -eq 1 && "$arg" != -* ]] && services+=("$arg")
          [[ "$arg" == up ]] && seen_up=1
        done
        for service in "${services[@]}"; do container_for "$service" >>"$state/containers"; done
        if [[ " $* " == *" docker-compose.setup.yml"* || " $* " == *"/docker-compose.setup.yml "* ]]; then
          start_listener "${JELLYFIN_SETUP_PORT:?}"; start_listener "${GRIMMORY_SETUP_PORT:?}"
        fi ;;
      rm)
        : >"$state/containers"
        if [[ "${STUB_RM_LEAVES_LISTENER:-0}" != 1 ]]; then
          for pidfile in "$state"/listener.*.pid; do
            [[ -f "$pidfile" ]] && kill -KILL "$(cat "$pidfile")" 2>/dev/null && rm -f "$pidfile"
          done
        fi ;;
      run)
        count=$(( $(cat "$state/run.count" 2>/dev/null || echo 0) + 1 )); echo "$count" >"$state/run.count"
        printf '%s\n' "$*" >"$state/run.$count.argv"
        cat >"$state/run.$count.stdin"
        mode="$(cat "$state/mode" 2>/dev/null || echo ok)"
        if [[ "$mode" == reject ]]; then
          echo "integration validation failed: jellyfin_rejected: Jellyfin rejected the token or the request" >&2
          exit 1
        fi
        if [[ "$count" -ge 2 && -f "$state/response.second.json" ]]; then cat "$state/response.second.json"; else cat "$state/response.json"; fi ;;
    esac ;;
esac
exit 0
EOF
printf '#!/usr/bin/env bash\nexit 0\n' >"$skeleton/stub/podman-compose"
chmod +x "$skeleton/stub"/* "$skeleton/telos"

free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'; }
export STUB_STATE="$work/state" STUB_LOG="$work/stub.log" OPENSSL_STATE="$work/openssl.count" NO_COLOR=1
export JELLYFIN_SETUP_PORT="$(free_port)" GRIMMORY_SETUP_PORT="$(free_port)"
export PATH="$skeleton/stub:$PATH" PODMAN_COMPOSE_PROVIDER="$skeleton/stub/podman-compose"
unset TELOS_NODE_PATH TELOS_DEV
: >"$STUB_LOG"
node="$skeleton/.telos"
env_file="$node/node.env"
telos() { (cd "$work" && "$skeleton/telos" "$@"); }

secret_token="jf-secret-token-4f7a1c"
secret_password="gm-secret-password-9d2b3e"
secret_password2="gm-secret-password-replaced-77"

telos init --dev --storage "$work/storage" >/dev/null 2>&1 || fail "fixture init failed"
initial_env="$work/node.env.initial"
cp -p "$env_file" "$initial_env"
reset_state() {
  rm -rf "$work/state"/*; : >"$STUB_LOG"
  cp -p "$initial_env" "$env_file"; chmod 600 "$env_file"
  cat >"$work/state/response.json" <<'JSON'
{"jellyfin":[{"id":"jf-movies","name":"Movies"},{"id":"jf-music","name":"Music"}],"grimmory":[{"id":"7","name":"Books"},{"id":"9","name":"Comics"}]}
JSON
}
mark_running() { printf '%s\n' telos-jellyfin telos-grimmory >"$work/state/containers"; }
port_open() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
drive() {
  # drive STEPS_JSON TRANSCRIPT [args...] -- runs telos configure integrations in a PTY
  local steps="$1" transcript="$2"; shift 2
  : >"$transcript"
  local status errexit=0
  case "$-" in *e*) errexit=1 ;; esac
  set +e
  (cd "$work" && python3 "$repo_root/tests/helpers/pty_drive.py" "$transcript" --timeout 25 -- "$skeleton/telos" --dev configure integrations "$@" <<<"$steps")
  status=$?
  [[ $errexit -eq 0 ]] || set -e
  return $status
}
no_secrets_in() {
  local file
  for file in "$@"; do
    for secret in "$secret_token" "$secret_password" "$secret_password2"; do
      ! grep -aq -- "$secret" "$file" || fail "a secret appeared in $file"
    done
  done
}

CRED_STEPS='{"expect":"Jellyfin API key: "},{"send":"'"$secret_token"'\n"},{"expect":"Grimmory gateway account \\[[^]]*\\]: "},{"send":"gateway-user\n"},{"expect":"Grimmory password[^:]*: "},{"send":"'"$secret_password"'\n"}'
SELECT_STEPS='{"expect":"Expose which Jellyfin libraries\\? "},{"send":"9\n"},{"expect":"choose one or more of the listed numbers"},{"expect":"Expose which Jellyfin libraries\\? "},{"send":"2, 1,2\n"},{"expect":"Expose which Grimmory libraries\\? "},{"send":"1\n"}'
HAPPY="[$CRED_STEPS,$SELECT_STEPS]"
CREDS_ONLY="[$CRED_STEPS]"

# --- success without --setup -------------------------------------------------------------
reset_state; mark_running
drive "$HAPPY" "$work/t-happy.txt" || fail "configure failed: $(tail -c 800 "$work/t-happy.txt")"
for needle in "Movies" "jf-movies" "Music" "jf-music" "Books" "(id 7)"; do
  grep -aq -- "$needle" "$work/t-happy.txt" || fail "the listing did not show '$needle'"
done
no_secrets_in "$work/t-happy.txt" "$STUB_LOG"
grep -qx 'JELLYFIN_ADMIN_TOKEN='"$secret_token" "$env_file" || fail "token not recorded"
grep -qx 'JELLYFIN_LIBRARY_IDS=jf-movies,jf-music' "$env_file" || fail "library IDs must be deduplicated in displayed order, got: $(grep JELLYFIN_LIBRARY_IDS "$env_file")"
grep -qx 'GRIMMORY_ADMIN_USER=gateway-user' "$env_file" || fail "user not recorded"
grep -qx 'GRIMMORY_LIBRARY_IDS=7' "$env_file" || fail "Grimmory IDs not recorded"
grep -qx 'GRIMMORY_ADMIN_PASSWORD='"$secret_password" "$env_file" || fail "an explicit password must be recorded"
[[ "$(stat -c '%a' "$env_file")" == 600 ]] || fail "node.env mode changed"
diff <(grep -vE '^(JELLYFIN_ADMIN_TOKEN|JELLYFIN_LIBRARY_IDS|GRIMMORY_ADMIN_USER|GRIMMORY_LIBRARY_IDS|GRIMMORY_ADMIN_PASSWORD)=' "$env_file") \
     <(grep -vE '^(JELLYFIN_ADMIN_TOKEN|JELLYFIN_LIBRARY_IDS|GRIMMORY_ADMIN_USER|GRIMMORY_LIBRARY_IDS|GRIMMORY_ADMIN_PASSWORD)=' "$initial_env") >/dev/null \
  || fail "unrelated lines of node.env changed"

# The request reached the validation container over stdin only.
[[ "$(cat "$work/state/run.count")" -eq 2 ]] || fail "expected two discovery runs, got $(cat "$work/state/run.count")"
for run_number in 1 2; do
  grep -q 'run --rm -T --no-deps telos-core ./telos-core validate-integrations' "$work/state/run.$run_number.argv" || fail "wrong validation command: $(cat "$work/state/run.$run_number.argv")"
  no_secrets_in "$work/state/run.$run_number.argv"
  python3 - "$work/state/run.$run_number.stdin" "$secret_token" "$secret_password" <<'PY'
import json, sys
request = json.load(open(sys.argv[1]))
assert request == {"jellyfinToken": sys.argv[2], "grimmoryUser": "gateway-user", "grimmoryPassword": sys.argv[3]}, request
PY
done
! grep -q 'docker-compose.setup.yml' "$work/state/run.1.argv" || fail "the setup overlay was used without --setup"
# Saving Jellyfin's Base URL leaves Jellyfin unanswering until it restarts, so only the
# final validation may carry the flag; the first (listing) one must stay read-only.
! grep -q -- '--set-jellyfin-base-url' "$work/state/run.1.argv" || fail "the first validation must be read-only"
grep -q -- 'validate-integrations --set-jellyfin-base-url$' "$work/state/run.2.argv" || fail "the final validation must save Jellyfin's Base URL: $(cat "$work/state/run.2.argv")"
grep -q 'compose --env-file '"$env_file"' -p telos' "$STUB_LOG" || fail "Compose was not run against node.env with a stable project"

# --- blank password keeps the stored one; an explicit one replaces it --------------------------
reset_state; mark_running
drive '[{"expect":"Jellyfin API key: "},{"send":"'"$secret_token"'\n"},{"expect":"Grimmory gateway account"},{"send":"\n"},{"expect":"Grimmory password"},{"send":"\n"},{"expect":"Expose which Jellyfin"},{"send":"1\n"},{"expect":"Expose which Grimmory"},{"send":"2\n"}]' "$work/t-blank.txt" || fail "blank-password run failed"
before_password="$(grep '^GRIMMORY_ADMIN_PASSWORD=' "$initial_env")"
grep -qxF "$before_password" "$env_file" || fail "a blank password prompt changed the stored password"
grep -qx 'GRIMMORY_LIBRARY_IDS=9' "$env_file" || fail "selection not recorded"
python3 - "$work/state/run.1.stdin" "$initial_env" <<'PY'
import json, re, sys
request = json.load(open(sys.argv[1]))
stored = re.search(r"^GRIMMORY_ADMIN_PASSWORD=(.*)$", open(sys.argv[2]).read(), re.M).group(1)
assert request["grimmoryPassword"] == stored, "the stored password must be reused for validation"
PY

# --- failures never touch node.env ---------------------------------------------------------------
reset_state; mark_running; echo reject >"$work/state/mode"
set +e; drive "$CREDS_ONLY" "$work/t-reject.txt"; status=$?; set -e
[[ $status -ne 0 ]] || fail "a rejected token must fail"
cmp -s "$env_file" "$initial_env" || fail "node.env changed after a validation error"
grep -aq 'jellyfin_rejected' "$work/t-reject.txt" || fail "the failure reason was not shown"
no_secrets_in "$work/t-reject.txt"

reset_state; mark_running
echo '{"jellyfin":[{"id":"jf-music","name":"Music"}],"grimmory":[{"id":"7","name":"Books"}]}' >"$work/state/response.second.json"
set +e; drive "$HAPPY" "$work/t-drift.txt"; status=$?; set -e
[[ $status -ne 0 ]] || fail "a library that disappeared between validations must fail"
cmp -s "$env_file" "$initial_env" || fail "node.env changed after the second validation failed"

for bad_selection in "0" "3" "x" ",," ""; do
  reset_state; mark_running
  set +e
  drive '[{"expect":"Jellyfin API key: "},{"send":"'"$secret_token"'\n"},{"expect":"Grimmory gateway account"},{"send":"u\n"},{"expect":"Grimmory password"},{"send":"'"$secret_password"'\n"},{"expect":"Expose which Jellyfin"},{"send":"'"$bad_selection"'\n"},{"expect":"choose one or more of the listed numbers"},{"signal":"INT"}]' "$work/t-badsel.txt"
  set -e
  cmp -s "$env_file" "$initial_env" || fail "node.env changed after the invalid selection '$bad_selection'"
done

# --- the atomic writer ---------------------------------------------------------------------------
reset_state
python3 - "$skeleton/cli/lib/node_config.py" "$env_file" "$initial_env" <<'PY'
import importlib.util, os, signal, sys, glob
module, env_file, initial = sys.argv[1:]
spec = importlib.util.spec_from_file_location("nc", module); nc = importlib.util.module_from_spec(spec); spec.loader.exec_module(nc)
original = open(initial, "rb").read()
real_replace = os.replace
def interrupt(_source, _target):
    os.kill(os.getpid(), signal.SIGTERM)   # arrives between the temp write and the rename
    real_replace(_source, _target)
os.replace = interrupt
try:
    nc.set_values(env_file, [("JELLYFIN_ADMIN_TOKEN", "t"), ("JELLYFIN_LIBRARY_IDS", "a"), ("GRIMMORY_ADMIN_USER", "u"), ("GRIMMORY_LIBRARY_IDS", "b")])
except BaseException as error:
    assert type(error).__name__ == "_Interrupted", type(error)
else:
    raise SystemExit("the interrupted rewrite completed")
os.replace = real_replace
assert open(env_file, "rb").read() == original, "node.env changed during an interrupted rewrite"
assert not glob.glob(os.path.join(os.path.dirname(env_file), ".node.env.*")), "staging file left behind"
PY

# --- --setup lifecycle ---------------------------------------------------------------------------
check_setup_started() {
  grep -q "compose .*-f $skeleton/docker-compose.yml -f $skeleton/docker-compose.setup.yml up -d jellyfin grimmory-db grimmory telos-egress-proxy telos-setup-proxy" "$STUB_LOG" \
    || fail "--setup must start exactly Jellyfin, Grimmory, its database and the egress proxy with base+setup files: $(grep ' up ' "$STUB_LOG")"
  [[ "$(grep -c ' up ' "$STUB_LOG")" -eq 1 ]] || fail "--setup started more than one set of services"
  ! grep -E ' up .*(postgres|redis|telos-core|traefik|telos-client|clamav)' "$STUB_LOG" >/dev/null || fail "--setup started an unrelated service"
}
assert_setup_closed() {
  port_open "$JELLYFIN_SETUP_PORT" && fail "Jellyfin setup port is still open"
  port_open "$GRIMMORY_SETUP_PORT" && fail "Grimmory setup port is still open"
  grep -q "podman rm -f --ignore -t 10 telos-jellyfin telos-grimmory-db telos-grimmory telos-egress-proxy telos-setup-proxy" "$STUB_LOG" || fail "teardown did not remove the setup containers by name"
  ! grep -E 'down|volume|rm .*-v' "$STUB_LOG" >/dev/null || fail "teardown touched volumes"
  return 0
}
SETUP_STEPS='{"expect":"Press Enter when both services"},{"send":"\n"}'
SETUP_HAPPY="[$SETUP_STEPS,$CRED_STEPS,$SELECT_STEPS]"
SETUP_CREDS_ONLY="[$SETUP_STEPS,$CRED_STEPS]"

reset_state
drive "$SETUP_HAPPY" "$work/t-setup.txt" --setup || fail "--setup run failed: $(tail -c 800 "$work/t-setup.txt")"
check_setup_started
grep -aq "http://127.0.0.1:$JELLYFIN_SETUP_PORT" "$work/t-setup.txt" || fail "no loopback URL printed"
grep -aq "ssh -L $JELLYFIN_SETUP_PORT:127.0.0.1:$JELLYFIN_SETUP_PORT -L $GRIMMORY_SETUP_PORT:127.0.0.1:$GRIMMORY_SETUP_PORT" "$work/t-setup.txt" || fail "no SSH tunnel example printed"
grep -q 'docker-compose.setup.yml' "$work/state/run.1.argv" || fail "validation during --setup must use the setup overlay"
assert_setup_closed
no_secrets_in "$work/t-setup.txt"

reset_state; echo reject >"$work/state/mode"
set +e; drive "$SETUP_CREDS_ONLY" "$work/t-setup-fail.txt" --setup; status=$?; set -e
[[ $status -ne 0 ]] || fail "--setup with a rejected token must fail"
assert_setup_closed
cmp -s "$env_file" "$initial_env" || fail "node.env changed after a failed --setup run"

for sig in INT TERM; do
  reset_state
  set +e
  drive '[{"expect":"Press Enter when both services"},{"signal":"'"$sig"'"}]' "$work/t-setup-$sig.txt" --setup
  status=$?
  set -e
  [[ $status -ne 0 ]] || fail "a $sig during --setup must not exit 0"
  assert_setup_closed
  cmp -s "$env_file" "$initial_env" || fail "node.env changed after $sig"
done

# A cleanup that leaves the port open must fail loudly and name the removal command.
reset_state
set +e
STUB_RM_LEAVES_LISTENER=1 drive "$SETUP_HAPPY" "$work/t-setup-leak.txt" --setup
status=$?
set -e
[[ $status -ne 0 ]] || fail "a setup port left open was reported as success"
grep -aq "STILL OPEN" "$work/t-setup-leak.txt" || fail "an open setup port was not reported"
grep -aq "podman ps --filter publish=" "$work/t-setup-leak.txt" || fail "the exact removal command was not named"
for pidfile in "$work"/state/listener.*.pid; do kill -KILL "$(cat "$pidfile")" 2>/dev/null || true; done
sleep 0.2

# --setup refuses a running stack and pre-existing containers, and starts nothing.
for running in telos-core telos-traefik telos-jellyfin; do
  reset_state; printf '%s\n' "$running" >"$work/state/containers"
  set +e; drive '[]' "$work/t-refuse.txt" --setup; status=$?; set -e
  [[ $status -ne 0 ]] || fail "--setup ran while $running exists"
  ! grep -q ' up ' "$STUB_LOG" || fail "--setup started services while $running exists"
done

# Without --setup the upstreams must already be running.
reset_state
set +e; drive '[]' "$work/t-norun.txt"; status=$?; set -e
[[ $status -ne 0 ]] || fail "configure ran with no upstream containers"
grep -aq 'is not running' "$work/t-norun.txt" || fail "missing upstream containers were not explained"

# --- command line --------------------------------------------------------------------------------
help_text="$(telos configure integrations --help 2>&1)"
! grep -qiE -- '--(token|password|key|secret|passphrase)' <<<"$help_text" || fail "help advertises a secret-valued flag"
for option in --password --token --jellyfin-token --yes; do
  set +e; telos configure integrations "$option" value >"$work/opt.out" 2>&1; status=$?; set -e
  [[ $status -eq 2 ]] || fail "$option must be an unknown option (exit 2), got $status"
done
set +e; telos configure integrations </dev/null >"$work/notty.out" 2>&1; status=$?; set -e
[[ $status -eq 2 ]] || fail "configure without a terminal must exit 2"
grep -q 'needs a terminal' "$work/notty.out" || fail "missing terminal was not explained"
set +e; telos configure bogus >"$work/sub.out" 2>&1; status=$?; set -e
[[ $status -eq 2 ]] || fail "unknown subcommand must exit 2"

echo "configure integrations: overlay, hidden prompts, stdin-only validation, atomic write and teardown verified"
