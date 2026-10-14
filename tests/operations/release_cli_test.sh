#!/usr/bin/env bash
set -euo pipefail

# A release generation (images.env beside docker-compose.release.yml) runs only the candidate's own
# images: the CLI layers the release overlay, exports the image variables from a strictly parsed
# images.env, and refuses to build. The source tree behaves exactly as before. A fake provider records
# what Compose would have been asked to do; nothing here needs containers.
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fail() { echo "release cli test: $*" >&2; exit 1; }

digest_a="sha256:$(printf 'a%.0s' $(seq 64))"
digest_b="sha256:$(printf 'b%.0s' $(seq 64))"
digest_c="sha256:$(printf 'c%.0s' $(seq 64))"

make_tree() {   # DIR [release]
  local tree="$1"
  mkdir -p "$tree/cli" "$tree/stub" "$tree/node"
  cp -r "$repo_root/cli/." "$tree/cli/"
  cp "$repo_root/telos" "$repo_root/docker-compose.yml" "$repo_root/docker-compose.release.yml" "$tree/"
  printf 'TELOS_ENV=development\n' >"$tree/node/node.env"
  cat >"$tree/stub/podman" <<'EOF'
#!/usr/bin/env bash
# Records the argv Compose receives, and the image variables in its environment.
{
  printf 'ARGV:'; printf ' %s' "$@"; printf '\n'
  printf 'ENV:core=%s client=%s egress=%s\n' "${TELOS_CORE_IMAGE:-}" "${TELOS_CLIENT_IMAGE:-}" "${TELOS_EGRESS_PROXY_IMAGE:-}"
} >>"${FAKE_PODMAN_LOG:?}"
EOF
  cp "$tree/stub/podman" "$tree/stub/podman-compose"
  chmod +x "$tree/stub/podman" "$tree/stub/podman-compose"
}

write_images_env() {   # DIR core client egress
  printf 'TELOS_CORE_IMAGE=%s\nTELOS_CLIENT_IMAGE=%s\nTELOS_EGRESS_PROXY_IMAGE=%s\n' "$2" "$3" "$4" >"$1/images.env"
}

run_in() {   # DIR snippet  -- sources the real helpers against that tree
  local tree="$1"; shift
  (
    export REPO_ROOT="$tree" CLI_DIR="$tree/cli" TELOS_NODE_PATH="$tree/node" TELOS_DEV=1
    export PATH="$tree/stub:$PATH" PODMAN_COMPOSE_PROVIDER="$tree/stub/podman-compose" FAKE_PODMAN_LOG="$tree/podman.log"
    # shellcheck disable=SC1091
    . "$tree/cli/lib/common.sh"
    # shellcheck disable=SC1091
    . "$tree/cli/lib/config.sh"
    eval "$*"
  )
}

# --- the source tree is not a release generation -------------------------------------------------
source_tree="$work/source"
make_tree "$source_tree"
run_in "$source_tree" 'is_release_generation' && fail "a source tree must not be a release generation"
run_in "$source_tree" 'compose_with -- config' >/dev/null 2>&1 || true
grep -q "docker-compose.release.yml" "$source_tree/podman.log" 2>/dev/null && fail "the source tree layered the release overlay"

# --- a release generation layers the overlay and exports the images ------------------------------
release="$work/release"
make_tree "$release"
write_images_env "$release" "localhost/telos-core@$digest_a" "localhost/telos-client@$digest_b" "localhost/telos-egress-proxy@$digest_c"
run_in "$release" 'is_release_generation' || fail "images.env plus the overlay must make a release generation"
run_in "$release" 'compose_with -- up -d' || fail "compose in a release generation failed"
log="$release/podman.log"
grep -q -- "-f $release/docker-compose.yml -f $release/docker-compose.release.yml" "$log" || fail "the release overlay was not layered right after the base model: $(cat "$log")"
grep -q "ENV:core=localhost/telos-core@$digest_a client=localhost/telos-client@$digest_b egress=localhost/telos-egress-proxy@$digest_c" "$log" \
  || fail "the image variables were not exported to Compose: $(cat "$log")"

# optional overlays still follow the release overlay, never precede it
: >"$log"
cp "$repo_root/docker-compose.private-ca.yml" "$release/"
run_in "$release" 'compose_with docker-compose.private-ca.yml -- up -d' || fail "compose with an extra overlay failed"
grep -q -- "docker-compose.release.yml -f $release/docker-compose.private-ca.yml" "$log" || fail "overlay order is wrong: $(cat "$log")"

# --- images.env is parsed, never sourced ---------------------------------------------------------
bad_env() {   # description content
  local tree="$work/bad-$RANDOM"
  make_tree "$tree"
  printf '%b' "$2" >"$tree/images.env"
  if run_in "$tree" 'load_release_images' >/dev/null 2>"$tree/err"; then fail "images.env accepted: $1"; fi
  [ ! -e "$tree/pwned" ] || fail "images.env executed code: $1"
}
bad_env "an unknown variable" "TELOS_CORE_IMAGE=localhost/c@$digest_a\nTELOS_CLIENT_IMAGE=localhost/d@$digest_b\nTELOS_EGRESS_PROXY_IMAGE=localhost/e@$digest_c\nPATH=/tmp\n"
bad_env "a tag instead of a digest" "TELOS_CORE_IMAGE=localhost/c:latest\nTELOS_CLIENT_IMAGE=localhost/d@$digest_b\nTELOS_EGRESS_PROXY_IMAGE=localhost/e@$digest_c\n"
bad_env "a missing variable" "TELOS_CORE_IMAGE=localhost/c@$digest_a\nTELOS_CLIENT_IMAGE=localhost/d@$digest_b\n"
bad_env "a short digest" "TELOS_CORE_IMAGE=localhost/c@sha256:abc\nTELOS_CLIENT_IMAGE=localhost/d@$digest_b\nTELOS_EGRESS_PROXY_IMAGE=localhost/e@$digest_c\n"
bad_env "command substitution" "TELOS_CORE_IMAGE=\$(touch \$REPO_ROOT/pwned)@$digest_a\nTELOS_CLIENT_IMAGE=localhost/d@$digest_b\nTELOS_EGRESS_PROXY_IMAGE=localhost/e@$digest_c\n"
bad_env "a quoted value" "TELOS_CORE_IMAGE=\"localhost/c@$digest_a\"\nTELOS_CLIENT_IMAGE=localhost/d@$digest_b\nTELOS_EGRESS_PROXY_IMAGE=localhost/e@$digest_c\n"

# --- nothing builds in a release generation -------------------------------------------------------
if run_in "$release" 'source "$CLI_DIR/commands/start.sh"; run --build' >"$work/build.out" 2>&1; then
  fail "telos start --build was accepted in a release generation"
fi
grep -q "refused in a release generation" "$work/build.out" || fail "the refusal does not say why: $(cat "$work/build.out")"

echo "release cli test: PASS"
