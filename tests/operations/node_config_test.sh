#!/usr/bin/env bash
set -euo pipefail

# Hermetic behavior tests for node initialization, validation, start refusal and
# doctor. The CLI runs from a copied repository skeleton against stub openssl,
# podman and podman-compose executables, so nothing touches a real node, a
# container runtime or the network.
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
declare -a background_pids=()
cleanup() {
  for pid in "${background_pids[@]:-}"; do kill -KILL "$pid" 2>/dev/null || true; done
  rm -rf "$work"
}
trap cleanup EXIT

fail() { echo "node-config test: $*" >&2; exit 1; }
digest_tree() { (cd "$1" && find . -type f -print0 | sort -z | xargs -0 sha256sum 2>/dev/null; find . -printf '%p %m\n' | sort); }

# --- skeleton -------------------------------------------------------------------
make_skeleton() {
  local skeleton="$1"
  mkdir -p "$skeleton/bin" "$skeleton/stub"
  cp -r "$repo_root/cli" "$skeleton/cli"
  cp "$repo_root/telos" "$repo_root/.env.example" "$repo_root/docker-compose.yml" \
     "$repo_root/docker-compose.dev.yml" "$skeleton/"
  cat >"$skeleton/stub/openssl" <<'EOF'
#!/usr/bin/env bash
# Deterministic, distinct output per invocation. OPENSSL_BLOCK_AT=N hangs the Nth call.
state="${OPENSSL_STATE:?}"
count=$(( $(cat "$state" 2>/dev/null || echo 0) + 1 ))
echo "$count" >"$state"
if [[ -n "${OPENSSL_BLOCK_AT:-}" && "$count" -eq "$OPENSSL_BLOCK_AT" ]]; then
  printf '%s %s\n' "$PPID" "$$" >"${OPENSSL_STARTED:?}"
  exec sleep 300
fi
[[ "$1" == rand ]] || exit 2
seed="$(printf 'telos-fixture-%s' "$count" | sha256sum | cut -d' ' -f1)"
case "$2" in
  -base64) printf '%s' "$seed" | xxd -r -p | head -c 32 | base64 ;;
  -hex)    printf '%s\n' "${seed:0:48}" ;;
  *) exit 2 ;;
esac
EOF
  cat >"$skeleton/stub/podman" <<'EOF'
#!/usr/bin/env bash
echo "podman $*" >>"${STUB_LOG:?}"
case "$1" in
  info) echo "${STUB_ROOTLESS:-true}" ;;
  compose) exit 0 ;;
  *) exit 0 ;;
esac
EOF
  printf '#!/usr/bin/env bash\nexit 0\n' >"$skeleton/stub/podman-compose"
  chmod +x "$skeleton/stub"/* "$skeleton/telos"
}

skeleton="$work/skeleton"
make_skeleton "$skeleton"
: >"$work/stub.log"
export STUB_LOG="$work/stub.log" OPENSSL_STATE="$work/openssl.count" NO_COLOR=1
original_path="$PATH"
export PATH="$skeleton/stub:$PATH"
export PODMAN_COMPOSE_PROVIDER="$skeleton/stub/podman-compose"
unset TELOS_NODE_PATH TELOS_DEV
telos() { (cd "$work" && "$skeleton/telos" "$@"); }
node="$skeleton/.telos"
env_file="$node/node.env"
reset_node() { rm -rf "$node" "$work/storage" "$skeleton/.env" "$OPENSSL_STATE"; : >"$STUB_LOG"; }

mode_of() { stat -c '%a' "$1"; }

# --- first development init ------------------------------------------------------
reset_node
output="$(telos init --dev --storage "$work/storage" 2>&1)" || fail "development init failed: $output"
[[ "$(mode_of "$env_file")" == 600 ]] || fail "node.env must be mode 0600"
[[ "$(mode_of "$node/config")" == 700 ]] || fail "config directory must be mode 0700"
for file in cursor-keys.json hls-key; do
  [[ "$(mode_of "$node/config/$file")" == 600 ]] || fail "$file must be mode 0600"
done
[[ "$(mode_of "$node/state")" == 700 ]] || fail "state directory must be mode 0700"
grep -q 'telos-storage\|storage' <<<"$output" || fail "init did not report the storage tree"
for sub in "" /media /books /bookdrop; do [[ -d "$work/storage$sub" ]] || fail "storage$sub was not created"; done

python3 - "$node" <<'PY'
import base64, json, sys
node = sys.argv[1]
ring = json.load(open(f"{node}/config/cursor-keys.json"))
assert set(ring) == {"active", "keys"} and ring["active"] in ring["keys"], ring
cursor = [base64.b64decode(v, validate=True) for v in ring["keys"].values()]
hls = base64.b64decode(open(f"{node}/config/hls-key").read().strip(), validate=True)
assert all(len(k) >= 32 for k in cursor) and len(hls) >= 32
assert hls not in cursor, "the HLS root must differ from the cursor key"
assert len(set(cursor)) == len(cursor)
PY

grep -q "^TELOS_CONFIG_PATH=$node/config\$" "$env_file" || fail "node.env must record the host config directory"
grep -qx 'TELOS_CURSOR_KEYS_FILE=/run/telos/cursor-keys.json' "$env_file" || fail "cursor path must be the container path"
grep -qx 'TELOS_HLS_SIGNING_KEY_FILE=/run/telos/hls-key' "$env_file" || fail "HLS path must be the container path"
grep -qx 'TELOS_ENV=development' "$env_file" || fail "development init must write TELOS_ENV=development"
! grep -E '=(change-me|generate-me)' "$env_file" >/dev/null || fail "init left a placeholder secret"
secrets_distinct="$(grep -E '^(POSTGRES_PASSWORD|TELOS_OWNER_PASSWORD|TELOS_RUNTIME_PASSWORD|REDIS_PASSWORD|GRIMMORY_ADMIN_PASSWORD|GRIMMORY_DB_PASSWORD|MARIADB_ROOT_PASSWORD|TELOS_BOOTSTRAP_TOKEN)=' "$env_file" | cut -d= -f2 | sort -u | wc -l)"
[[ "$secrets_distinct" -eq 8 ]] || fail "the eight generated secrets must all differ"
[[ ! -e "$env_file".staging.* && -z "$(ls -A "$node" | grep -E '^\.config-staging' || true)" ]] || fail "init left staging paths"

# --- init is one-way -------------------------------------------------------------
before="$(digest_tree "$node")"
if telos init --dev --storage "$work/storage" >"$work/second.out" 2>&1; then fail "a second init succeeded"; fi
grep -q 'already exists' "$work/second.out" || fail "second init did not explain the refusal"
[[ "$(digest_tree "$node")" == "$before" ]] || fail "second init changed the node"

set +e
telos init --dev --force >"$work/force.out" 2>&1
force_status=$?
set -e
[[ $force_status -eq 2 ]] || fail "--force must be an unknown option (exit 2), got $force_status"
grep -q "unknown option '--force'" "$work/force.out" || fail "--force diagnostic missing"
[[ "$(digest_tree "$node")" == "$before" ]] || fail "--force changed the node"
[[ -z "$(find "$node" "$skeleton" -maxdepth 2 -name '*.bak*' -print -quit)" ]] || fail "--force created a backup"
! grep -rq 'init --force' "$skeleton/cli" || fail "the CLI still recommends init --force"

# --- an existing checkout .env must be imported, never regenerated over --------------
reset_node
printf 'TELOS_DOMAIN=old.example.net\nPOSTGRES_PASSWORD=keep-this-exact-value\nSTORAGE_PATH=%s\n' "$work/storage" >"$skeleton/.env"
if telos init --dev --storage "$work/storage" >"$work/legacy.out" 2>&1; then fail "init ran over a checkout .env"; fi
grep -q 'init --import-env' "$work/legacy.out" || fail "legacy refusal lacks the import instruction"
[[ ! -e "$env_file" && ! -e "$node/config" ]] || fail "init wrote files despite a checkout .env"
telos init --dev --storage "$work/storage" --import-env "$skeleton/.env" >"$work/import.out" 2>&1 || fail "import failed: $(cat "$work/import.out")"
grep -qx 'POSTGRES_PASSWORD=keep-this-exact-value' "$env_file" || fail "import regenerated an existing credential"
grep -qx 'TELOS_DOMAIN=old.example.net' "$env_file" || fail "import lost the domain"
[[ -f "$skeleton/.env" ]] || fail "import removed the source .env"
[[ -s "$node/config/hls-key" ]] || fail "import did not create signing keys"

# --- interruption ----------------------------------------------------------------
run_blocked_init() {
  # Starts init, waits until the stub openssl is blocked on call N, then signals the
  # Python initializer itself (the stub records its parent's pid) and its sleeper.
  local block_at="$1" signal="$2" python_pid sleeper_pid
  reset_node
  rm -f "$work/openssl.started"
  (cd "$work" && OPENSSL_BLOCK_AT="$block_at" OPENSSL_STARTED="$work/openssl.started" \
    "$skeleton/telos" init --dev --storage "$work/storage" >"$work/blocked.out" 2>&1) &
  local pid=$!
  background_pids+=("$pid")
  for _ in $(seq 1 100); do [[ -s "$work/openssl.started" ]] && break; sleep 0.1; done
  [[ -s "$work/openssl.started" ]] || fail "init never reached openssl call $block_at"
  read -r python_pid sleeper_pid <"$work/openssl.started"
  kill "-$signal" "$python_pid"
  kill -KILL "$sleeper_pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  sleep 0.3
}

run_blocked_init 2 TERM
[[ ! -e "$env_file" ]] || fail "an interrupted init left node.env"
[[ ! -e "$node/config" ]] || fail "an interrupted init left a configuration directory"
[[ -z "$(ls -A "$node" 2>/dev/null | grep -E '^\.config-staging' || true)" ]] || fail "an interrupted init left staging"

run_blocked_init 4 TERM
[[ ! -e "$env_file" ]] || fail "init interrupted after the key commit left node.env"
[[ ! -e "$node/config" ]] || fail "init removed no part of its own committed-but-unreferenced config"
telos init --dev --storage "$work/storage" >"$work/retry.out" 2>&1 || fail "init could not retry after a clean interruption: $(cat "$work/retry.out")"

# A hard kill cannot clean up: the orphan must be refused, named and never deleted.
run_blocked_init 4 KILL
[[ ! -e "$env_file" ]] || fail "a killed init produced node.env"
[[ -d "$node/config" ]] || fail "expected the orphan configuration directory after SIGKILL"
orphan_digest="$(digest_tree "$node/config")"
if telos init --dev --storage "$work/storage" >"$work/orphan.out" 2>&1; then fail "init adopted an orphan config directory"; fi
grep -q "$node/config" "$work/orphan.out" || fail "orphan refusal does not name the directory"
grep -qi 'inspect' "$work/orphan.out" || fail "orphan refusal lacks recovery instructions"
[[ "$(digest_tree "$node/config")" == "$orphan_digest" ]] || fail "init modified or deleted the orphan"

# --- concurrent initialization -----------------------------------------------------
reset_node
rm -f "$work/openssl.started"
(cd "$work" && OPENSSL_BLOCK_AT=2 OPENSSL_STARTED="$work/openssl.started" "$skeleton/telos" init --dev --storage "$work/storage" >"$work/first.out" 2>&1) &
first_pid=$!
background_pids+=("$first_pid")
for _ in $(seq 1 100); do [[ -s "$work/openssl.started" ]] && break; sleep 0.1; done
[[ -s "$work/openssl.started" ]] || fail "the first concurrent init never started"
read -r first_python first_sleeper <"$work/openssl.started"
set +e
telos init --dev --storage "$work/storage" >"$work/second-concurrent.out" 2>&1
concurrent_status=$?
set -e
[[ $concurrent_status -ne 0 ]] || fail "two initializations ran at once"
grep -q 'changing this node.s configuration' "$work/second-concurrent.out" || fail "concurrent init did not report the lock"
kill -TERM "$first_python" 2>/dev/null || true
kill -KILL "$first_sleeper" 2>/dev/null || true
wait "$first_pid" 2>/dev/null || true
[[ ! -e "$env_file" ]] || fail "the interrupted first init left node.env"

# --- start refuses before Compose ---------------------------------------------------
reset_node
telos init --dev --storage "$work/storage" >/dev/null 2>&1 || fail "fixture init failed"
valid_snapshot="$work/valid-node"
cp -a "$node" "$valid_snapshot"

restore_valid() { rm -rf "$node"; cp -a "$valid_snapshot" "$node"; : >"$STUB_LOG"; }
assert_start_refused() {
  local label="$1" mode="${2:-dev}" status output
  : >"$STUB_LOG"
  set +e
  if [[ "$mode" == dev ]]; then output="$(telos start --dev 2>&1)"; else output="$(TELOS_NODE_PATH="$node" telos start 2>&1)"; fi
  status=$?
  set -e
  [[ $status -ne 0 ]] || fail "start accepted: $label"
  grep -q 'compose' "$STUB_LOG" && fail "start called Compose despite: $label"
  [[ -z "${3:-}" ]] || grep -q -- "$3" <<<"$output" || fail "start refusal for '$label' lacked '$3': $output"
  return 0
}

restore_valid; rm -f "$node/config/hls-key"
assert_start_refused "missing signing file" dev "hls-key is missing"
restore_valid; chmod 640 "$node/config/hls-key"
assert_start_refused "wrong file mode" dev "require 0600"
restore_valid; chmod 755 "$node/config"
assert_start_refused "loose directory mode" dev "0700 or stricter"
restore_valid; cp "$node/config/hls-key" "$work/hls.copy"
python3 - "$node/config/cursor-keys.json" "$work/hls.copy" <<'PY'
import json, sys
path, hls = sys.argv[1:]
ring = json.load(open(path))
ring["keys"][ring["active"]] = open(hls).read().strip()
json.dump(ring, open(path, "w"))
PY
assert_start_refused "cursor key equal to the HLS root" dev "must differ"
restore_valid; printf 'AAAA\n' >"$node/config/hls-key"
assert_start_refused "short key" dev "at least 32 bytes"
restore_valid; printf '{"active":"k9","keys":{"k1":"%s"}}' "$(head -c 32 /dev/zero | base64)" >"$node/config/cursor-keys.json"
assert_start_refused "active key missing from the ring" dev "active key"
restore_valid; rm -f "$env_file"
assert_start_refused "missing node.env" dev "node.env not found"
restore_valid; chmod 600 "$env_file"; printf 'BROKEN LINE\n' >>"$env_file"
assert_start_refused "malformed node.env" dev "not an assignment"
restore_valid; ln -sfn "$node/config" "$work/config-link"
python3 - "$env_file" "$work/config-link" <<'PY'
import re, sys
path, link = sys.argv[1:]
text = open(path).read()
text = re.sub(r"^TELOS_CONFIG_PATH=.*$", f"TELOS_CONFIG_PATH={link}", text, flags=re.M)
open(path, "w").write(text)
PY
assert_start_refused "symlinked configuration directory" dev "symbolic link"

# Production mode: placeholders and empty library lists. Validation happens before
# the rootful-runtime check, so a refusal here proves nothing reached Compose.
restore_valid
python3 - "$env_file" <<'PY'
import re, sys
path = sys.argv[1]
text = open(path).read()
for key, value in {"TELOS_ENV": "production", "TELOS_DOMAIN": "node.community.test", "ACME_EMAIL": "ops@community.test"}.items():
    text = re.sub(rf"^{key}=.*$", f"{key}={value}", text, flags=re.M)
open(path, "w").write(text)
PY
assert_start_refused "blank Jellyfin token" prod "JELLYFIN_ADMIN_TOKEN is a placeholder"
assert_start_refused "empty Jellyfin library list" prod "JELLYFIN_LIBRARY_IDS names no library"
assert_start_refused "empty Grimmory library list" prod "GRIMMORY_LIBRARY_IDS names no library"
python3 - "$env_file" <<'PY'
import re, sys
path = sys.argv[1]
text = open(path).read()
text = re.sub(r"^JELLYFIN_ADMIN_TOKEN=.*$", "JELLYFIN_ADMIN_TOKEN=abc123token", text, flags=re.M)
text = re.sub(r"^JELLYFIN_LIBRARY_IDS=.*$", "JELLYFIN_LIBRARY_IDS=lib1", text, flags=re.M)
text = re.sub(r"^GRIMMORY_LIBRARY_IDS=.*$", "GRIMMORY_LIBRARY_IDS=7", text, flags=re.M)
open(path, "w").write(text)
PY
for placeholder_key in POSTGRES_PASSWORD REDIS_PASSWORD; do
  cp "$env_file" "$work/env.before"
  sed -i "s/^$placeholder_key=.*/$placeholder_key=change-me/" "$env_file"
  assert_start_refused "placeholder $placeholder_key" prod "$placeholder_key is missing or a placeholder"
  cp "$work/env.before" "$env_file"
done
# Fully configured production fixture passes validation and is stopped only by the
# non-root runtime check (so Compose still never runs).
: >"$STUB_LOG"
set +e
production_output="$(TELOS_NODE_PATH="$node" telos start 2>&1)"
production_status=$?
set -e
[[ $production_status -eq 2 ]] || fail "complete production fixture should reach the rootful check (exit 2), got $production_status: $production_output"
grep -q 'rootful Podman' <<<"$production_output" || fail "complete production fixture was refused for another reason: $production_output"

# Unreadable config directory (as a non-root user this is a real read failure).
if [[ $(id -u) -ne 0 ]]; then
  restore_valid; chmod 000 "$node/config"
  assert_start_refused "unreadable config directory" dev "signing"
  chmod 700 "$node/config"
fi

# Valid development node reaches Compose with the node's env file and a stable project.
restore_valid
: >"$STUB_LOG"
telos start --dev >"$work/start.out" 2>&1 || true
grep -q "podman compose --env-file $env_file --project-directory $skeleton -p telos" "$STUB_LOG" \
  || fail "start did not invoke Compose with node.env: $(cat "$STUB_LOG")"

# --- doctor ----------------------------------------------------------------------------
restore_valid
chmod 640 "$node/config/hls-key"; chmod 644 "$env_file"
set +e
doctor_output="$(telos doctor --dev 2>&1)"
set -e
! grep -q 'init --force' <<<"$doctor_output" || fail "doctor recommended init --force"
grep -q 'chmod 600 .*node.env' <<<"$doctor_output" || fail "doctor did not name the node.env repair"
grep -q 'hls-key is mode' <<<"$doctor_output" || fail "doctor did not report the loose hls-key"
key_before="$(sha256sum "$node/config/hls-key" "$node/config/cursor-keys.json")"
env_before="$(grep -v '^$' "$env_file" | sha256sum)"
telos doctor --fix --dev >/dev/null 2>&1 || true
[[ "$(mode_of "$env_file")" == 600 && "$(mode_of "$node/config/hls-key")" == 600 ]] || fail "doctor --fix did not tighten modes"
[[ "$(sha256sum "$node/config/hls-key" "$node/config/cursor-keys.json")" == "$key_before" ]] || fail "doctor --fix changed key contents"
[[ "$(grep -v '^$' "$env_file" | sha256sum)" == "$env_before" ]] || fail "doctor --fix edited node.env"
rm -f "$node/config/hls-key"
doctor_output="$(telos doctor --fix --dev 2>&1 || true)"
[[ ! -e "$node/config/hls-key" ]] || fail "doctor --fix regenerated a missing key"
grep -q 'never regenerated\|never regenerate' <<<"$doctor_output" || fail "doctor did not explain that keys are never regenerated"

# --- parser, writer and Compose round trip -----------------------------------------------
restore_valid
python3 - "$skeleton/cli/lib/node_config.py" "$work" <<'PY'
import importlib.util, os, subprocess, sys
module_path, work = sys.argv[1:]
spec = importlib.util.spec_from_file_location("node_config", module_path)
nc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(nc)

for bad_value in ["pre${NAME}post", "${X:-d}"]:
    try:
        nc.encode_value(bad_value)
    except nc.ConfigError:
        pass
    else:
        raise SystemExit(f"encoder accepted a value Compose would expand: {bad_value!r}")
for bad in ["A='${HOME}'\n", "A=1\nA=2\n", "lower=1\n", "A=$(id)\n", "A=`id`\n", "A=a b\n", "A='unterminated\n", "A='a\\nb'\n", "no equals\n", "A=1\x00\n", "A=${B}\n"]:
    try:
        nc.parse_env_text(bad)
    except nc.ConfigError:
        continue
    raise SystemExit(f"parser accepted {bad!r}")

samples = ["plain", "with space", "dollar $HOME $(x)", "hash # not a comment", "it's", 'say "hi"', "back\\slash \\' tricky", "mix $ # ' \" \\ end", "", "unicode é"]
env = os.path.join(work, "roundtrip.env")
open(env, "w").write("# header\nKEEP=untouched\n")
os.chmod(env, 0o600)
for index, value in enumerate(samples):
    key = f"V{index}"
    r, w = os.pipe()
    os.write(w, value.encode()); os.close(w)
    done = subprocess.run([sys.executable, module_path, "set", env, key, "--value-fd", str(r)], pass_fds=(r,), capture_output=True, text=True)
    os.close(r)
    assert done.returncode == 0, (value, done.stderr)
values, _ = nc.parse_env_text(open(env).read())
assert values["KEEP"] == "untouched"
for index, value in enumerate(samples):
    assert values[f"V{index}"] == value, (index, values[f"V{index}"], value)
text = open(env).read()
assert text.startswith("# header\nKEEP=untouched\n"), "set must leave other bytes untouched"
open(os.path.join(work, "roundtrip.compose.yml"), "w").write(
    "services:\n  probe:\n    image: probe\n    environment:\n"
    + "".join(f"      - OUT{i}=${{V{i}}}\n" for i in range(len(samples))))
PY
rendered="$(python3 "$repo_root/tests/helpers/compose_double/render_compose.py" --env-file "$work/roundtrip.env" -f "$work/roundtrip.compose.yml" config)"
python3 - "$rendered" <<'PY'
import sys, yaml
samples = ["plain", "with space", "dollar $HOME $(x)", "hash # not a comment", "it's", 'say "hi"', "back\\slash \\' tricky", "mix $ # ' \" \\ end", "", "unicode é"]
doc = yaml.safe_load(sys.argv[1])
environment = doc["services"]["probe"]["environment"]
rendered = environment if isinstance(environment, dict) else dict(item.split("=", 1) for item in environment)
for index, value in enumerate(samples):
    assert rendered[f"OUT{index}"] == value, (index, rendered[f"OUT{index}"], value)
PY
if [[ -n "${PODMAN_COMPOSE_REAL_PROVIDER:-}" ]]; then
  PATH="$original_path" PYTHONPATH="${PODMAN_COMPOSE_REAL_PYTHONPATH:-}" python3 "$PODMAN_COMPOSE_REAL_PROVIDER" --env-file "$work/roundtrip.env" -f "$work/roundtrip.compose.yml" config >"$work/real.out" 2>/dev/null \
    || fail "the real podman-compose provider could not render the round-trip file"
  python3 - "$work/real.out" <<'PY'
import sys, yaml
samples = ["plain", "with space", "dollar $HOME $(x)", "hash # not a comment", "it's", 'say "hi"', "back\\slash \\' tricky", "mix $ # ' \" \\ end", "", "unicode é"]
doc = yaml.safe_load(open(sys.argv[1]))
environment = doc["services"]["probe"]["environment"]
rendered = environment if isinstance(environment, dict) else dict(item.split("=", 1) for item in environment)
for index, value in enumerate(samples):
    assert rendered[f"OUT{index}"] == value, ("real provider", index, rendered[f"OUT{index}"], value)
PY
fi

# The host support policy (Ubuntu 24.04+, Podman 5+, the pinned provider) that init,
# doctor and the installer share.
bash "$repo_root/tests/operations/install_test.sh"

echo "node config: init, refusal, interruption, doctor, host policy and Compose round trip verified"
