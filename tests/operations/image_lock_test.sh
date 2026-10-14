#!/usr/bin/env bash
set -euo pipefail

# Mutation tests for the image lock: the verifier must reject each defect below and
# accept only a tree where every production/build reference maps exactly once to a
# reviewed, non-fabricated digest. Each case copies the real inputs into a scratch
# root and breaks exactly one thing.
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
trap 'rm -rf "${work:?}"' EXIT
verify() { python3 -I "$repo_root/scripts/verify-image-lock.py" --source --root "$1"; }
fail() { echo "image lock test: $*" >&2; exit 1; }

copy_tree() { # NAME -> prints the scratch root
  local root="$work/$1"
  mkdir -p "$root/release" "$root/backend" "$root/frontend" "$root/deploy/egress-proxy"
  cp "$repo_root/release/images.lock" "$root/release/"
  cp "$repo_root/backend/Dockerfile" "$root/backend/"
  cp "$repo_root/frontend/Dockerfile" "$root/frontend/"
  cp "$repo_root/deploy/egress-proxy/Dockerfile" "$root/deploy/egress-proxy/"
  cp "$repo_root/docker-compose.yml" "$repo_root/docker-compose.test.yml" "$root/"
  printf '%s' "$root"
}
expect_rejected() { # NAME FRAGMENT [python mutation of the scratch root]
  local name="$1" fragment="$2" root output status
  root="$(copy_tree "$name")"
  python3 - "$root" "${3:-}" <<'PY'
import json, re, sys
root, mutation = sys.argv[1], sys.argv[2]
exec(mutation)
PY
  set +e; output="$(verify "$root" 2>&1)"; status=$?; set -e
  [[ $status -ne 0 ]] || fail "$name was accepted"
  grep -Fq -- "$fragment" <<<"$output" || fail "$name did not report '$fragment': $output"
}

clean="$(copy_tree clean)"
verify "$clean" >/dev/null || fail "the committed tree is rejected: $(verify "$clean" 2>&1)"

HEADER='
def lock():
    return json.load(open(root + "/release/images.lock"))
def save(doc):
    json.dump(doc, open(root + "/release/images.lock", "w"), indent=2)
def edit(path, old, new, count=1):
    text = open(root + "/" + path).read()
    assert old in text, (path, old)
    open(root + "/" + path, "w").write(text.replace(old, new, count))
'
case_py() { printf '%s\n%s' "$HEADER" "$1"; }

expect_rejected "fabricated-repeating-digest" "fabricated placeholder digest" "$(case_py '
d = lock(); d["images"]["redis"]["manifestDigest"] = "sha256:" + "4" * 64; save(d)')"
expect_rejected "fabricated-two-character-digest" "fabricated placeholder digest" "$(case_py '
d = lock(); d["images"]["redis"]["platforms"]["linux/amd64"] = "sha256:" + "ab" * 32; save(d)')"
expect_rejected "tag-only-compose-image" "is not pinned by digest" "$(case_py '
edit("docker-compose.yml", "docker.io/library/redis:7.4.11-alpine@sha256:", "docker.io/library/redis:7.4.11-alpine#", 1)
text = open(root + "/docker-compose.yml").read()
text = re.sub(r"redis:7.4.11-alpine#[0-9a-f]{64}", "redis:7.4.11-alpine", text)
open(root + "/docker-compose.yml", "w").write(text)')"
expect_rejected "tag-only-dockerfile-from" "is not pinned by digest" "$(case_py '
text = open(root + "/frontend/Dockerfile").read()
text = re.sub(r"(nginx-unprivileged:1.31.7-alpine)@sha256:[0-9a-f]{64}", r"\1", text)
open(root + "/frontend/Dockerfile", "w").write(text)')"
expect_rejected "digest-differs-from-lock" "digest differs from the lock" "$(case_py '
text = open(root + "/docker-compose.yml").read()
text = re.sub(r"(traefik:v3.7.14@sha256:)[0-9a-f]{64}", r"\g<1>" + "0123456789abcdef" * 4, text)
open(root + "/docker-compose.yml", "w").write(text)')"
expect_rejected "unreviewed-extra-image" "is not in the reviewed lock" "$(case_py '
edit("docker-compose.yml", "    container_name: telos-redis\n", "    container_name: telos-redis\n", 1)
text = open(root + "/docker-compose.yml").read()
text = text.replace("services:\n", "services:\n  sidecar:\n    image: docker.io/library/busybox:1.37@sha256:" + "9f" * 32 + "\n", 1)
open(root + "/docker-compose.yml", "w").write(text)')"
expect_rejected "mutable-latest-in-lock" "mutable tag 'latest'" "$(case_py '
d = lock(); d["images"]["jellyfin"]["source"] = "docker.io/jellyfin/jellyfin:latest"; save(d)')"
expect_rejected "unknown-lock-key" "violates its schema" "$(case_py '
d = lock(); d["images"]["redis"]["extra"] = 1; save(d)')"
expect_rejected "wrong-platform" "violates its schema" "$(case_py '
d = lock(); d["images"]["redis"]["platforms"] = {"linux/arm64": d["images"]["redis"]["platforms"]["linux/amd64"]}; save(d)')"
expect_rejected "duplicate-digest-under-unrelated-source" "shares a digest with unrelated" "$(case_py '
d = lock(); d["images"]["redis"]["manifestDigest"] = d["images"]["postgres"]["manifestDigest"]; save(d)')"
expect_rejected "locked-key-nothing-uses" "locked but no Dockerfile or Compose service uses it" "$(case_py '
d = lock(); d["images"]["spare"] = dict(d["images"]["redis"], source="docker.io/library/spare:1.0", manifestDigest="sha256:" + "12345678" * 8); save(d)')"
expect_rejected "missing-production-reference" "is not in the reviewed lock" "$(case_py '
d = lock(); del d["images"]["mariadb"]; save(d)')"
expect_rejected "test-image-satisfying-production" "cannot satisfy a production reference" "$(case_py '
d = lock(); e = d["images"].pop("redis"); d["images"]["redis-moved"] = None; del d["images"]["redis-moved"]; save(d)')"
expect_rejected "dynamic-registry-reference" "dynamic reference" "$(case_py '
edit("backend/Dockerfile", "FROM builder-${EMBED_FRONTEND} AS builder", "FROM ${REGISTRY}/library/builder AS builder")')"
expect_rejected "undeclared-stage-selection" "does not select a declared stage" "$(case_py '
edit("backend/Dockerfile", "FROM builder-${EMBED_FRONTEND} AS builder", "FROM nonexistent-${EMBED_FRONTEND} AS builder")')"
expect_rejected "compose-service-without-image-or-build" "has neither image nor build" "$(case_py '
text = open(root + "/docker-compose.yml").read()
text = text.replace("services:\n", "services:\n  empty:\n    restart: no\n", 1)
open(root + "/docker-compose.yml", "w").write(text)')"
expect_rejected "dynamic-compose-image" "is dynamic" "$(case_py '
edit("docker-compose.yml", "image: docker.io/library/redis:7.4.11-alpine@sha256:", "image: ${REDIS_IMAGE:-docker.io/library/redis:7.4.11-alpine}@sha256:", 1)')"

# The accepted shapes: local stage aliases and declared ARG selection are not registry references.
grep -q 'FROM builder-base AS builder-false' "$clean/backend/Dockerfile" || fail "the fixture lost its local stage alias"
grep -q 'FROM builder-${EMBED_FRONTEND}' "$clean/backend/Dockerfile" || fail "the fixture lost its declared stage selection"

# The wrapper is strict, not an existence check.
bash "$repo_root/scripts/verify-release-pins.sh" >/dev/null || fail "verify-release-pins.sh rejected the committed tree"

# The refresher refuses a mutable source before touching the network.
if python3 "$repo_root/scripts/refresh-image-lock.py" --section images --key x --source docker.io/library/redis:latest --output "$work/out.lock" 2>"$work/refresh.err"; then
  fail "refresh-image-lock accepted a 'latest' source"
fi
grep -Fq "mutable 'latest'" "$work/refresh.err" || fail "refresh-image-lock did not explain the refusal"
[[ ! -e "$work/out.lock" ]] || fail "refresh-image-lock wrote a lock for a refused source"

echo "image lock: fabricated, tag-only, drifted, unreviewed, test-only and dynamic references are rejected; the committed tree maps exactly"
