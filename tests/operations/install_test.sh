#!/usr/bin/env bash
set -euo pipefail

# Behavior of the host support policy (cli/lib/preflight.sh) against fixture
# os-release files, architectures, kernels, Podman/provider versions, listeners and
# tools. Nothing here inspects or changes the real host's packages.
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
trap 'rm -rf "${work:?}"' EXIT
fail() { echo "install test: $*" >&2; exit 1; }

# A PATH holding only the tools preflight needs, so "missing tool" is testable.
base_tools=(bash sh sed grep awk sort head tr cat uname stat id dirname readlink mktemp rm mkdir env basename wc cut date)
optional_tools=(openssl python3 flock curl)

make_bin() { # DIR [tools to include...]
  local dir="$1"; shift
  mkdir -p "$dir"
  local tool source
  for tool in "${base_tools[@]}" "$@"; do
    source="$(type -P "$tool" || true)"
    [[ -z "$source" ]] || ln -sf "$source" "$dir/$tool"
  done
}
stub() { # DIR NAME BODY
  printf '#!/usr/bin/env bash\n%s\n' "$3" >"$1/$2"
  chmod +x "$1/$2"
}
podman_stub() { # DIR VERSION
  stub "$1" podman "case \"\$1\" in --version) echo 'podman version $2' ;; info) echo false ;; esac"
}
os_release() { # NAME ID VERSION_ID [ID_LIKE] -> path of a fixture os-release
  local file="$work/os-$2-$3"
  { printf 'ID=%s\nVERSION_ID="%s"\nPRETTY_NAME="%s"\n' "$2" "$3" "$1"
    [[ -z "${4:-}" ]] || printf 'ID_LIKE="%s"\n' "$4"; } >"$file"
  printf '%s' "$file"
}

# A fully supported host: every required tool, Podman 5.0.0, the pinned provider.
good_bin="$work/good-bin"
make_bin "$good_bin" "${optional_tools[@]}"
stub "$good_bin" ss ':'
podman_stub "$good_bin" 5.0.0
stub "$good_bin" podman-compose 'echo "podman-compose version 1.6.0"'
provider="$good_bin/podman-compose"
echo "Linux version 6.8.0-generic (buildd@lcy02) #1 SMP" >"$work/proc-version"

# variant DIR [omit-tool] — a copy of the good host, optionally lacking one tool.
variant() {
  local dir="$1" omit="${2:-}" tool keep=()
  for tool in "${optional_tools[@]}"; do [[ "$tool" == "$omit" ]] || keep+=("$tool"); done
  make_bin "$dir" "${keep[@]}"
  [[ "$omit" == ss ]] || stub "$dir" ss ':'
  podman_stub "$dir" 5.0.0
  cp "$provider" "$dir/podman-compose"
}

run_preflight() { # BIN_DIR [VAR=value ...] -> prints output, returns the preflight status
  local bin="$1"; shift
  env -i HOME="$work" PATH="$bin" NO_COLOR=1 \
    REPO_ROOT="$repo_root" CLI_DIR="$repo_root/cli" \
    TELOS_OS_RELEASE_FILE="$(os_release "Ubuntu 24.04.1 LTS" ubuntu 24.04 debian)" TELOS_HOST_ARCH=x86_64 \
    TELOS_KERNEL_RELEASE=6.8.0-31-generic TELOS_PROC_VERSION="$work/proc-version" \
    PODMAN_COMPOSE_PROVIDER="$provider" "$@" \
    "$(type -P bash)" -c '. "$CLI_DIR/lib/common.sh"; . "$CLI_DIR/lib/preflight.sh"; run_preflight'
}
expect_pass() { # LABEL BIN [VAR=value ...]
  local label="$1"; shift
  local output
  output="$(run_preflight "$@" 2>&1)" || fail "$label was rejected:
$output"
  printf '%s' "$output" >"$work/last.out"
}
expect_fail() { # LABEL FRAGMENT BIN [VAR=value ...]
  local label="$1" fragment="$2"; shift 2
  local output status
  set +e; output="$(run_preflight "$@" 2>&1)"; status=$?; set -e
  [[ $status -ne 0 ]] || fail "$label was accepted:
$output"
  grep -Fq -- "$fragment" <<<"$output" || fail "$label did not report '$fragment':
$output"
  printf '%s' "$output" >"$work/last.out"
}
# observe LABEL BIN [VAR=value ...] — record the output regardless of status (development
# runs also report rootless extras that depend on the machine running the test).
observe() {
  shift
  set +e; run_preflight "$@" >"$work/last.out" 2>&1; set -e
}
last_has() { grep -Fq -- "$1" "$work/last.out"; }
last_lacks() { ! grep -Fq -- "$1" "$work/last.out"; }

# --- supported ----------------------------------------------------------------------------
expect_pass "Ubuntu 24.04 x86_64 Podman 5.0.0" "$good_bin"
last_has 'reference release' || fail "24.04 is not labeled as the reference release"
last_lacks 'not yet a verified combination' || fail "the reference release was labeled unverified"
expect_pass "Ubuntu 26.04 (newer: supported but unverified)" "$good_bin" \
  "TELOS_OS_RELEASE_FILE=$(os_release 'Ubuntu 26.04 LTS' ubuntu 26.04 debian)"
last_has 'not yet a verified combination' || fail "a newer Ubuntu release was not labeled unverified"
last_lacks 'reference release' || fail "a newer Ubuntu release was labeled the reference release"

# --- unsupported hosts: no best-effort path -------------------------------------------------
for version in 22.04 23.10 20.04 18.04; do
  expect_fail "Ubuntu $version" "unsupported Ubuntu release: $version" "$good_bin" \
    "TELOS_OS_RELEASE_FILE=$(os_release "Ubuntu $version" ubuntu "$version" debian)"
done
expect_fail "a malformed Ubuntu version" "unsupported Ubuntu release" "$good_bin" \
  "TELOS_OS_RELEASE_FILE=$(os_release 'Ubuntu weird' ubuntu latest debian)"
while IFS='|' read -r name id version like; do
  expect_fail "$name" "unsupported host" "$good_bin" "TELOS_OS_RELEASE_FILE=$(os_release "$name" "$id" "$version" "$like")"
  last_has 'no best-effort path' || fail "$name refusal does not say there is no best-effort path"
done <<'EOF'
Debian 12|debian|12|
Fedora Linux 40|fedora|40|
Arch Linux|arch||
Rocky Linux 9.4|rocky|9.4|rhel centos fedora
Alpine Linux 3.20|alpine|3.20|
Linux Mint 22|linuxmint|22|ubuntu debian
Pop!_OS 24.04|pop|24.04|ubuntu debian
elementary OS 8|elementary|8|ubuntu
Kali GNU/Linux|kali|2024.2|debian
EOF
expect_fail "a host without os-release (macOS-like)" "unsupported host" "$good_bin" "TELOS_OS_RELEASE_FILE=$work/does-not-exist"
echo "Linux version 5.15.0-microsoft-standard-WSL2" >"$work/wsl-version"
expect_fail "WSL" "WSL is not a supported server host" "$good_bin" "TELOS_PROC_VERSION=$work/wsl-version"
expect_fail "ARM (aarch64)" "unsupported architecture: aarch64" "$good_bin" TELOS_HOST_ARCH=aarch64
expect_fail "ARM (arm64)" "unsupported architecture: arm64" "$good_bin" TELOS_HOST_ARCH=arm64
expect_fail "an old kernel" "too old for openat2" "$good_bin" TELOS_KERNEL_RELEASE=5.4.0-100-generic
expect_pass "kernel 5.6" "$good_bin" TELOS_KERNEL_RELEASE=5.6.0-generic

# --- Podman ------------------------------------------------------------------------------------
for version in 4.9.3 4.0.0 3.4.4; do
  bin="$work/podman-$version"; variant "$bin"; podman_stub "$bin" "$version"
  expect_fail "Podman $version" "older than the required 5.0.0" "$bin"
done
for version in 5.0.0 5.7.0 6.1.2; do
  bin="$work/podman-$version"; variant "$bin"; podman_stub "$bin" "$version"
  expect_pass "Podman $version" "$bin"
done
unreadable="$work/podman-unreadable"; variant "$unreadable"; stub "$unreadable" podman 'echo "podman"'
expect_fail "a Podman whose version cannot be read" "version could not be read" "$unreadable"
no_podman="$work/no-podman"; variant "$no_podman"; unlink "$no_podman/podman"
expect_fail "no runtime" "Podman 5.0.0 or newer is required" "$no_podman"
last_has "'apt install podman' alone does not satisfy" || fail "the apt-version warning is missing"
docker_only="$work/docker-only"; variant "$docker_only"; unlink "$docker_only/podman"
stub "$docker_only" docker 'echo "Docker version 27.0.0"'
expect_fail "a Docker-only host" "Docker is present but is not a supported runtime" "$docker_only"
# Production operation is rootful: a non-root caller that requires root is refused.
if [[ $(id -u) -ne 0 ]]; then
  expect_fail "a non-root production caller" "not root" "$good_bin" PREFLIGHT_REQUIRE_ROOT=1
  observe "a non-root development caller" "$good_bin" TELOS_DEV=1 PREFLIGHT_REQUIRE_ROOT=1
  last_lacks "not root" || fail "a development node was held to the rootful requirement"
fi

# --- the pinned Compose provider -----------------------------------------------------------------
mkdir -p "$work/old-provider"; stub "$work/old-provider" podman-compose 'echo "podman-compose version 1.0.6"'
expect_fail "podman-compose 1.0.6 in production" "certified pin is 1.6.0" "$good_bin" "PODMAN_COMPOSE_PROVIDER=$work/old-provider/podman-compose"
last_has '1.0.6 on 24.04' || fail "the Ubuntu archive version is not explained"
observe "podman-compose 1.0.6 in development" "$good_bin" TELOS_DEV=1 "PODMAN_COMPOSE_PROVIDER=$work/old-provider/podman-compose"
last_has 'acceptable for development only' || fail "the development warning is missing"
last_lacks 'certified pin is 1.6.0' || fail "a development node was refused over the compose pin"
expect_fail "a relative provider" "must be an absolute path" "$good_bin" PODMAN_COMPOSE_PROVIDER=podman-compose
printf '#!/bin/sh\n' >"$work/not-executable"
expect_fail "a non-executable provider" "is not executable" "$good_bin" "PODMAN_COMPOSE_PROVIDER=$work/not-executable"
no_provider="$work/no-provider"; variant "$no_provider"; unlink "$no_provider/podman-compose"
expect_fail "no provider" "no podman-compose provider found" "$no_provider" PODMAN_COMPOSE_PROVIDER=

# --- public ports: no silent fallback ---------------------------------------------------------------------
busy="$work/busy"; variant "$busy"
stub "$busy" ss 'case "$*" in *"sport = :80"*) echo "LISTEN 0 511 0.0.0.0:80 0.0.0.0:*" ;; esac'
expect_fail "port 80 in use" "port 80 is already in use" "$busy"
last_has 'does not fall back to alternate public ports' || fail "the public-port remedy is missing"
last_has 'port 443 is free' || fail "port 443 was not reported independently"
own_traefik="$work/own-traefik"; variant "$own_traefik"
stub "$own_traefik" ss 'case "$*" in *"sport = :80"*|*"sport = :443"*) echo "LISTEN 0 511 0.0.0.0:80 0.0.0.0:*" ;; esac'
stub "$own_traefik" podman 'case "$1" in --version) echo "podman version 5.0.0" ;; info) echo false ;; ps) echo abc123def456 ;; esac'
expect_pass "ports held by the node's own running Traefik" "$own_traefik"
last_has 'held by the running telos-traefik' || fail "a running node's own Traefik was not recognized"
observe "port 80 busy on a development node" "$busy" TELOS_DEV=1
last_lacks 'port 80 is already in use' || fail "a development node was held to the public ports"

# --- required tools ----------------------------------------------------------------------------------------
for missing in openssl python3 flock ss; do
  bin="$work/no-$missing"; variant "$bin" "$missing"
  expect_fail "a host without $missing" "missing required tool: $missing" "$bin"
done
no_curl="$work/no-curl"; variant "$no_curl" curl
expect_pass "a host without curl (optional)" "$no_curl"
last_has 'missing optional tool: curl' || fail "the optional tool was not reported"

# --- the installer and doctor use the same policy ---------------------------------------------------------------------
installer() { # os-release-file [args...]
  local osfile="$1"; shift
  env -i HOME="$work" PATH="$good_bin" NO_COLOR=1 \
    TELOS_OS_RELEASE_FILE="$osfile" TELOS_HOST_ARCH=x86_64 TELOS_KERNEL_RELEASE=6.8.0 \
    TELOS_PROC_VERSION="$work/proc-version" PODMAN_COMPOSE_PROVIDER="$provider" \
    "$(type -P bash)" "$repo_root/install.sh" "$@"
}
set +e
installer_output="$(installer "$(os_release Fedora fedora 40)" --check 2>&1)"; installer_status=$?
set -e
[[ $installer_status -ne 0 ]] || fail "install.sh --check accepted Fedora"
grep -Fq 'unsupported host' <<<"$installer_output" || fail "install.sh did not apply the shared policy: $installer_output"
! grep -Eiq 'pacman|dnf install|zypper|yum install' <<<"$installer_output" || fail "the installer printed a package recipe for another distribution"
installer_output="$(installer "$(os_release 'Ubuntu 24.04.1 LTS' ubuntu 24.04 debian)" --check 2>&1)" || fail "install.sh --check rejected a supported host: $installer_output"
grep -Fq 'All prerequisites satisfied' <<<"$installer_output" || fail "install.sh --check did not report success: $installer_output"

grep -Fq 'run_preflight' "$repo_root/cli/commands/doctor.sh" || fail "doctor does not call the shared preflight"
grep -Fq 'run_preflight' "$repo_root/install.sh" || fail "install.sh does not call the shared preflight"
! grep -Eq 'distro_family|pacman|dnf |zypper' "$repo_root/cli/lib/preflight.sh" || fail "preflight still carries other-distribution package logic"

echo "install preflight: Ubuntu 24.04+, x86_64, Podman 5+, pinned provider, public ports and tools enforced; unsupported hosts refused"
