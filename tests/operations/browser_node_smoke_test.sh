#!/usr/bin/env bash
set -euo pipefail

# Protocol and error-handling contract of scripts/smoke-browser-node.sh against an
# ephemeral HTTPS fixture node (tests/helpers/fake_node_server.py) whose certificate
# comes from a throwaway CA trusted only through SSL_CERT_FILE for these runs. This
# proves the probe's behavior; it is NOT release evidence and cannot replace a real
# node.
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
declare -a pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do kill -KILL "$pid" 2>/dev/null || true; done
  rm -rf "$work"
}
trap cleanup EXIT
fail() { echo "browser-node smoke test: $*" >&2; exit 1; }
command -v openssl >/dev/null || { echo "not_run: openssl is required to mint the fixture CA" >&2; exit 3; }

# --- throwaway CA, a certificate for localhost and one for the wrong name ------------------
cd "$work"
# Python's default TLS context is strict (RFC 5280 key identifiers, CA keyUsage), as
# are real browsers; a sloppy test CA would hide genuine verification behavior.
printf 'basicConstraints=critical,CA:TRUE\nkeyUsage=critical,keyCertSign,cRLSign\nsubjectKeyIdentifier=hash\n' >ca.ext
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.pem -subj "/CN=Telos Fixture CA" -days 2 -addext "$(sed -n 1p ca.ext)" -addext "$(sed -n 2p ca.ext)" -addext "$(sed -n 3p ca.ext)" >/dev/null 2>&1
mint() { # NAME SAN
  openssl req -newkey rsa:2048 -nodes -keyout "$1.key" -out "$1.csr" -subj "/CN=$2" >/dev/null 2>&1
  printf 'subjectAltName=DNS:%s\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid\n' "$2" >"$1.ext"
  openssl x509 -req -in "$1.csr" -CA ca.pem -CAkey ca.key -CAcreateserial -out "$1.pem" -days 2 -extfile "$1.ext" >/dev/null 2>&1
}
mint good localhost
mint wrong other.test
cd "$repo_root"

mkdir -p "$work/tmp"
export TMPDIR="$work/tmp"
smoke="$repo_root/scripts/smoke-browser-node.sh"
# The fake node answers instantly, so an outage that does not show must fail at once.
export TELOS_SMOKE_OUTAGE_WAIT=0
commit="$(printf '%040d' 7)"
lock_digest_of() { python3 -c 'import hashlib,sys; print("sha256:"+hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1"; }

cat >"$work/manifest.json" <<'JSON'
{"schemaVersion":1,"chatChannel":"general","video":{"id":"v1"},"audio":{"id":"a1"},"ebook":{"id":"book-1","format":"epub"},"upload":{"filename":"smoke.txt","text":"fixture upload bytes"}}
JSON
owner_password="owner-canary-password-4821"
member_password="member-canary-password-9377"
bootstrap_token="fixture-bootstrap-token"
credentials() {
  # No argument: include the bootstrap token. An explicit argument (even empty) replaces it.
  local extra=",\"bootstrapToken\":\"$bootstrap_token\""
  if [[ $# -gt 0 ]]; then extra="$1"; fi
  printf '{"owner":{"username":"owner1","password":"%s"},"member":{"username":"member1","password":"%s"}%s}' \
    "$owner_password" "$member_password" "$extra"
}

start_server() { # [cert-name] [faults] [with-fifo]
  local cert="${1:-good}" faults="${2:-}" fifo="${3:-}"
  rm -f "$work/port"
  local args=("$work/$cert.pem" "$work/$cert.key" "$work/port")
  [[ -z "$faults" ]] || args+=(--faults "$faults")
  [[ -z "$fifo" ]] || args+=(--stage-fifo "$fifo")
  python3 "$repo_root/tests/helpers/fake_node_server.py" "${args[@]}" >/dev/null 2>"$work/server.err" &
  server_pid=$!
  pids+=("$server_pid")
  for _ in $(seq 1 100); do [[ -s "$work/port" ]] && break; sleep 0.05; done
  [[ -s "$work/port" ]] || fail "the fixture server did not start: $(cat "$work/server.err")"
  port="$(cat "$work/port")"
}
stop_server() { kill -KILL "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; }
new_fifo() { rm -f "$work/stage.in" "$work/stage.out"; mkfifo "$work/stage.in" "$work/stage.out"; }

run_smoke() { # RESULT_DIR [extra smoke args...]  (stdin: credentials)
  local out="$1"; shift
  rm -rf "$out"; mkdir -p "$out"
  SSL_CERT_FILE="${SMOKE_CA:-$work/ca.pem}" "$smoke" --public-url "https://localhost:$port" \
    --rehearsal --source-commit "$commit" --result-out "$out/result.json" \
    --fixture-manifest "$work/manifest.json" "$@"
}
result_field() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$1/result.json" "$2"; }
assertion_status() { python3 -c '
import json,sys
report=json.load(open(sys.argv[1]+"/report.json"))
print({a["id"]:a["status"] for a in report["assertions"]}.get(sys.argv[2],"absent"))' "$1" "$2"; }
valid_result() { python3 "$repo_root/scripts/lib/release_graph.py" validate --kind gate-result "$1/result.json" >/dev/null || fail "invalid result record in $1"
  python3 - "$repo_root" "$1" <<'PY'
import sys
sys.path.insert(0, sys.argv[1] + "/scripts/lib")
import release_graph as rg
rg.verify_result_directory(sys.argv[2], "result.json")
PY
}

# --- full journey with the outage controller ------------------------------------------------
new_fifo
start_server good "" "$work/stage"
out="$work/out-ok"
set +e
credentials | run_smoke "$out" --bootstrap-empty-node --stage-pipe "$work/stage" >"$work/ok.stdout" 2>"$work/ok.stderr"
status=$?
set -e
[[ $status -eq 0 ]] || fail "the clean journey exited $status: $(cat "$work/ok.stdout" "$work/ok.stderr")"
valid_result "$out"
[[ "$(result_field "$out" status)" == passed && "$(result_field "$out" recordKind)" == rehearsal ]] || fail "unexpected result record"
[[ "$(result_field "$out" candidateLockDigest)" == None ]] || fail "a source-only rehearsal must carry no lock digest"
for id in edge.client-root edge.client-health edge.api-live edge.unknown-api edge.module-routes auth.bootstrap auth.owner-login \
  auth.invite auth.accept chat.round-trip stream.video stream.audio library.read files.upload files.download commentary.file \
  files.delete outage.jellyfin outage.grimmory outage.restored; do
  [[ "$(assertion_status "$out" "$id")" == passed ]] || fail "assertion $id: $(assertion_status "$out" "$id")"
done
python3 - "$out/subject.json" <<'PY'
import json, sys
subject = json.load(open(sys.argv[1]))
assert subject["kind"] == "external-host" and subject["purpose"] == "clean-install", subject
PY
# Nothing sensitive reached any output, and the private jar/temp directory is gone.
for secret in "$owner_password" "$member_password" "$bootstrap_token"; do
  ! grep -rIq -- "$secret" "$out" "$work/ok.stdout" "$work/ok.stderr" || fail "a credential leaked into the outputs"
done
[[ -z "$(ls -A "$work/tmp")" ]] || fail "the smoke run left temporary files: $(ls -A "$work/tmp")"
stop_server

# --- the cookie jar is private -----------------------------------------------------------------
python3 - "$repo_root" "$work" <<'PY'
import os, ssl, stat, sys
sys.path.insert(0, sys.argv[1] + "/scripts/lib")
import browser_node_smoke as smoke
jar = os.path.join(sys.argv[2], "private.jar")
session = smoke.Session("https://localhost:1", ssl.create_default_context(), jar)
session._store([("Set-Cookie", "telos_session=abc; Path=/; HttpOnly; Secure")])
assert stat.S_IMODE(os.stat(jar).st_mode) == 0o600, "cookie jar must be mode 0600"
assert session.cookies == {"telos_session": "abc"}
PY

# --- usage errors never reach the network ------------------------------------------------------
expect_usage() {
  local label="$1"; shift
  set +e; credentials | run_smoke "$work/out-usage" "$@" >"$work/usage.out" 2>&1; status=$?; set -e
  [[ $status -eq 2 ]] || fail "$label: expected exit 2, got $status: $(cat "$work/usage.out")"
}
start_server
for bad_url in "http://localhost:$port" "https://127.0.0.1:$port" "https://localhost:$port/path" "https://user:pw@localhost:$port" "https://[::1]:$port"; do
  rm -rf "$work/out-url"; mkdir "$work/out-url"
  set +e
  credentials | SSL_CERT_FILE="$work/ca.pem" "$smoke" --public-url "$bad_url" --rehearsal --source-commit "$commit" \
    --result-out "$work/out-url/result.json" --fixture-manifest "$work/manifest.json" >"$work/url.out" 2>&1
  status=$?
  set -e
  [[ $status -eq 2 ]] || fail "$bad_url must be a usage error, got $status: $(cat "$work/url.out")"
  [[ ! -e "$work/out-url/result.json" ]] || fail "$bad_url produced a result"
done
expect_usage "candidate and rehearsal flags together" --candidate-lock "$work/none"
set +e
credentials | SSL_CERT_FILE="$work/ca.pem" "$smoke" --public-url "https://localhost:$port" --candidate-lock "$work/x" --source-commit "$commit" \
  --result-out "$work/out-usage/result.json" --fixture-manifest "$work/manifest.json" >"$work/usage.out" 2>&1
[[ $? -eq 2 ]] || fail "--source-commit with --candidate-lock must be a usage error"
printf '{}' | run_smoke "$work/out-usage" >"$work/usage.out" 2>&1; [[ $? -eq 2 ]] || fail "empty credentials must be a usage error"
printf '{"owner":{"username":"a","password":"b","extra":1},"member":{"username":"c","password":"d"}}' | run_smoke "$work/out-usage" >/dev/null 2>&1; [[ $? -eq 2 ]] || fail "extra credential keys must be a usage error"
credentials | run_smoke "$work/out-usage" >"$work/usage.out" 2>&1; [[ $? -eq 2 ]] || fail "a bootstrap token without --bootstrap-empty-node must be a usage error"
credentials '' | run_smoke "$work/out-usage" --bootstrap-empty-node >"$work/usage.out" 2>&1; [[ $? -eq 2 ]] || fail "--bootstrap-empty-node without a token must be a usage error"
echo '{"schemaVersion":1,"chatChannel":"general"}' >"$work/bad-manifest.json"
credentials | SSL_CERT_FILE="$work/ca.pem" "$smoke" --public-url "https://localhost:$port" --rehearsal --source-commit "$commit" \
  --result-out "$work/out-usage/result.json" --fixture-manifest "$work/bad-manifest.json" >"$work/usage.out" 2>&1; [[ $? -eq 2 ]] || fail "an incomplete fixture manifest must be a usage error"
set -e
stop_server

# --- TLS must verify ------------------------------------------------------------------------------
start_server wrong
set +e; credentials | run_smoke "$work/out-tls" --bootstrap-empty-node >"$work/tls.out" 2>&1; status=$?; set -e
[[ $status -eq 1 ]] || fail "a certificate for the wrong name must fail, got $status"
valid_result "$work/out-tls"
[[ "$(result_field "$work/out-tls" status)" == failed ]] || fail "wrong-name certificate was not recorded as failed"
grep -q 'TLS verification failed' "$work/out-tls/report.json" || fail "the TLS failure was not reported"
stop_server
start_server good
set +e; credentials | SMOKE_CA=/dev/null run_smoke "$work/out-untrusted" --bootstrap-empty-node >"$work/untrusted.out" 2>&1; status=$?; set -e
[[ $status -eq 1 ]] || fail "an untrusted CA must fail, got $status"
stop_server

# --- bootstrap rules ----------------------------------------------------------------------------------
start_server good
set +e
credentials '' | run_smoke "$work/out-nobootstrap" >"$work/nb.out" 2>&1; status=$?
set -e
[[ $status -eq 1 ]] || fail "an empty node without --bootstrap-empty-node must fail at sign-in, got $status"
[[ "$(assertion_status "$work/out-nobootstrap" auth.owner-login)" == failed ]] || fail "owner login should have failed"
[[ "$(assertion_status "$work/out-nobootstrap" journey.blocked)" == failed ]] || fail "later modules must not run without a session"
[[ "$(assertion_status "$work/out-nobootstrap" chat.round-trip)" == absent ]] || fail "Chat ran without a session"
set +e
credentials ',"bootstrapToken":"wrong-token"' | run_smoke "$work/out-badtoken" --bootstrap-empty-node >"$work/bt.out" 2>&1; status=$?
set -e
[[ $status -eq 1 && "$(assertion_status "$work/out-badtoken" auth.bootstrap)" == failed ]] || fail "a wrong bootstrap token must fail the bootstrap"
stop_server

# --- every failure mode has a failing counterpart -----------------------------------------------------
run_fault() { # FAULT ASSERTION_ID
  local fault="$1" expected="$2"
  new_fifo
  start_server good "$fault" "$work/stage"
  local out="$work/out-fault-$fault"
  set +e
  credentials | run_smoke "$out" --bootstrap-empty-node --stage-pipe "$work/stage" >"$work/fault.out" 2>&1
  local status=$?
  set -e
  [[ $status -eq 1 ]] || fail "fault $fault: expected exit 1, got $status: $(cat "$work/fault.out")"
  valid_result "$out"
  [[ "$(assertion_status "$out" "$expected")" == failed ]] || fail "fault $fault did not fail $expected (it was $(assertion_status "$out" "$expected"))"
  [[ "$(result_field "$out" status)" == failed && "$(result_field "$out" subjectFile)" == None ]] || fail "fault $fault produced a passing subject"
  stop_server
}
run_fault api-served-root edge.client-root
run_fault healthz-missing edge.client-health
run_fault unknown-api-html edge.unknown-api
run_fault missing-route edge.module-routes
run_fault no-channel chat.round-trip
run_fault chat-drops-message chat.round-trip
run_fault no-hls-redirect stream.video
run_fault bad-playlist stream.video
run_fault audio-wrong-type stream.audio
run_fault audio-200 stream.audio
run_fault bad-epub library.read
run_fault download-mismatch files.download
run_fault comment-lost commentary.file
run_fault delete-ignored files.delete
run_fault outage-breaks-chat outage.jellyfin
run_fault outage-html outage.jellyfin
run_fault never-recovers outage.restored

# --- no outage controller: the stages are not_run, never passed ---------------------------------------------
start_server
set +e; credentials | run_smoke "$work/out-nofifo" --bootstrap-empty-node >"$work/nf.out" 2>&1; status=$?; set -e
[[ $status -eq 3 ]] || fail "a journey without outage control must be not_run (3), got $status: $(cat "$work/nf.out")"
valid_result "$work/out-nofifo"
[[ "$(result_field "$work/out-nofifo" status)" == not_run && "$(result_field "$work/out-nofifo" subjectFile)" == None ]] || fail "missing outage control was not recorded as not_run"
for id in outage.jellyfin outage.grimmory outage.restored; do [[ "$(assertion_status "$work/out-nofifo" "$id")" == not_run ]] || fail "$id should be not_run"; done
stop_server

# --- candidate mode binds the lock and demands a host identity ---------------------------------------------------
printf '{"schemaVersion":1,"sourceCommit":"%s"}\n' "$commit" >"$work/lock.json"
digest="$(lock_digest_of "$work/lock.json")"
new_fifo; start_server good "" "$work/stage"
set +e
credentials | SSL_CERT_FILE="$work/ca.pem" "$smoke" --public-url "https://localhost:$port" --candidate-lock "$work/lock.json" \
  --result-out "$work/out-cand-noid/result.json" --fixture-manifest "$work/manifest.json" --bootstrap-empty-node --stage-pipe "$work/stage" >"$work/c1.out" 2>&1
set -e
mkdir -p "$work/out-cand-noid"
rm -rf "$work/out-cand-noid"; mkdir "$work/out-cand-noid"
set +e
credentials | SSL_CERT_FILE="$work/ca.pem" "$smoke" --public-url "https://localhost:$port" --candidate-lock "$work/lock.json" \
  --result-out "$work/out-cand-noid/result.json" --fixture-manifest "$work/manifest.json" --bootstrap-empty-node --stage-pipe "$work/stage" >"$work/c1.out" 2>&1
status=$?
set -e
[[ $status -eq 3 ]] || fail "candidate mode without a host identity must be not_run, got $status: $(cat "$work/c1.out")"
[[ "$(assertion_status "$work/out-cand-noid" identity.host)" == not_run ]] || fail "identity.host should be not_run"
[[ "$(result_field "$work/out-cand-noid" recordKind)" == candidate && "$(result_field "$work/out-cand-noid" candidateLockDigest)" == "$digest" ]] || fail "candidate result not bound to the lock"
stop_server

new_fifo; start_server good "" "$work/stage"
cat >"$work/host.json" <<JSON
[{"hostId":"sha256:$(printf 'a%.0s' $(seq 1 64))","identityMethod":"machine-id-sha256","os":"Ubuntu","osVersion":"24.04","arch":"x86_64"}]
JSON
rm -rf "$work/out-cand"; mkdir "$work/out-cand"
set +e
credentials | SSL_CERT_FILE="$work/ca.pem" "$smoke" --public-url "https://localhost:$port" --candidate-lock "$work/lock.json" \
  --result-out "$work/out-cand/result.json" --fixture-manifest "$work/manifest.json" --bootstrap-empty-node \
  --stage-pipe "$work/stage" --host-identity "$work/host.json" >"$work/c2.out" 2>&1
status=$?
set -e
[[ $status -eq 0 ]] || fail "a complete candidate-mode journey must pass, got $status: $(cat "$work/c2.out")"
valid_result "$work/out-cand"
python3 - "$work/out-cand/subject.json" "$digest" <<'PY'
import json, sys
subject = json.load(open(sys.argv[1]))
assert subject["candidateLockDigest"] == sys.argv[2] and subject["hosts"][0]["identityMethod"] == "machine-id-sha256", subject
PY
stop_server

echo "browser-node smoke: protocol, TLS, credentials, journey assertions, outage stages and result records verified"
