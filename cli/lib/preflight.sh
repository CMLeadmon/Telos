# shellcheck shell=bash
# Host prerequisite detection, shared by install.sh and `telos doctor` so the two can
# never disagree about what a supported host looks like.
#
# Policy (documentation/product/platform-scope.md): the server runs on Ubuntu 24.04
# or newer, x86_64, with Podman 5.0.0 or newer. Anything else -- Windows, macOS,
# WSL/Docker Desktop, other distributions and Ubuntu derivatives, Docker-only hosts,
# ARM -- is unsupported with no best-effort path. The VERIFIED reference release is
# 26.04 (its archive ships Podman 5.7, and the install, journey and recovery checks
# run there); any other release that meets the policy is reported as not yet verified.
#
# Design rule: DETECT AND REPORT, NEVER AUTO-INSTALL. Every failure names what is
# wrong and what the operator must provide; this file never invents a package recipe.
#
# Test seams (tests/operations/install_test.sh): TELOS_OS_RELEASE_FILE, TELOS_HOST_ARCH,
# TELOS_KERNEL_RELEASE, TELOS_PROC_VERSION, TELOS_HOST_CPUS, TELOS_HOST_MEMORY_KIB and
# TELOS_STORAGE_FREE_KIB replace the host probes. Tools are probed
# through PATH, so a test supplies stubs.

TELOS_UBUNTU_MIN="24.04"
TELOS_UBUNTU_REFERENCE="26.04"   # the release the live checks are run on (see platform-scope.md)
TELOS_PODMAN_MIN="5.0.0"
TELOS_COMPOSE_PIN="1.6.0"
TELOS_KERNEL_MIN="5.6"   # openat2(2), which the storage layer's confinement requires
# The full profile runs 11 containers whose memory limits sum to about 14 GiB (see
# config/runtime-hardening.json); a smaller host is unsupported rather than overcommitted.
TELOS_MIN_CPUS=4
TELOS_MIN_MEMORY_GIB=16
TELOS_MIN_STORAGE_GIB=20

PREFLIGHT_FAILURES=0
PREFLIGHT_WARNINGS=0
PREFLIGHT_HINTS=()

# --- helpers ---------------------------------------------------------------
os_release_file() { printf '%s' "${TELOS_OS_RELEASE_FILE:-/etc/os-release}"; }
host_arch()       { printf '%s' "${TELOS_HOST_ARCH:-$(uname -m)}"; }
kernel_release()  { printf '%s' "${TELOS_KERNEL_RELEASE:-$(uname -r)}"; }

# version_ge A B — true when A >= B for dotted numeric versions.
version_ge() {
  [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1)" = "$2" ]
}

# first_version TEXT — the first dotted numeric version in TEXT.
first_version() {
  printf '%s' "$1" | grep -oE '[0-9]+(\.[0-9]+)+' | head -n1
}

# os_field KEY — a field from the os-release file, read without sourcing it into
# this shell (a hostile file cannot run code here).
os_field() {
  local file; file="$(os_release_file)"
  [ -r "$file" ] || return 1
  sed -n "s/^$1=//p" "$file" | head -n1 | sed -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//"
}

detect_distro() { os_field ID || printf 'unknown'; }

# --- reporting -------------------------------------------------------------
pf_ok()   { printf '  %s  %s\n' "$(paint ok)" "$1"; }
pf_warn() { printf '  %s  %s\n' "$(paint warn)" "$1"; PREFLIGHT_WARNINGS=$((PREFLIGHT_WARNINGS + 1)); }
pf_fail() {
  printf '  %s  %s\n' "$(paint bad)" "$1"
  PREFLIGHT_FAILURES=$((PREFLIGHT_FAILURES + 1))
  [ -n "${2:-}" ] && PREFLIGHT_HINTS+=("$2")
  return 0
}

preflight_is_production() { [ "${TELOS_DEV:-0}" != "1" ]; }

# --- the support policy ----------------------------------------------------
check_host_support() {
  local id version like pretty arch kernel proc
  id="$(os_field ID || true)"
  version="$(os_field VERSION_ID || true)"
  like="$(os_field ID_LIKE || true)"
  pretty="$(os_field PRETTY_NAME || true)"
  proc="${TELOS_PROC_VERSION:-/proc/version}"

  if [ -r "$proc" ] && grep -qiE 'microsoft|wsl' "$proc" 2>/dev/null; then
    pf_fail "WSL is not a supported server host" \
            "Run the Telos server on an Ubuntu 24.04+ machine or VM (a hypervisor host does not become a server platform)."
    return
  fi
  if [ "$id" != "ubuntu" ]; then
    pf_fail "unsupported host: ${pretty:-${id:-unknown}} (${like:+derived from $like; }Telos servers run on Ubuntu 24.04 or newer only)" \
            "Use Ubuntu 24.04 or newer. Windows, macOS, WSL/Docker Desktop and other Linux distributions -- including Ubuntu derivatives -- are not supported, and there is no best-effort path."
    return
  fi
  if [ -z "$version" ] || ! printf '%s' "$version" | grep -qE '^[0-9]+\.[0-9]+$' || ! version_ge "$version" "$TELOS_UBUNTU_MIN"; then
    pf_fail "unsupported Ubuntu release: ${version:-unknown} (24.04 or newer required)" \
            "Upgrade to Ubuntu $TELOS_UBUNTU_MIN or newer."
    return
  fi
  if [ "$version" = "$TELOS_UBUNTU_REFERENCE" ]; then
    pf_ok "host: Ubuntu $version (reference release)"
  else
    pf_warn "host: Ubuntu $version meets the support policy but is not yet a verified combination (the verified reference is Ubuntu $TELOS_UBUNTU_REFERENCE); clean-install, storage, upgrade and recovery checks must be repeated on it"
  fi

  arch="$(host_arch)"
  if [ "$arch" = "x86_64" ]; then
    pf_ok "architecture: x86_64"
  else
    pf_fail "unsupported architecture: $arch (x86_64 required)" "Use an x86_64 host."
  fi

  kernel="$(first_version "$(kernel_release)")"
  if [ -n "$kernel" ] && version_ge "$kernel" "$TELOS_KERNEL_MIN"; then
    pf_ok "kernel: $kernel (openat2 available for storage confinement)"
  else
    pf_fail "kernel ${kernel:-unknown} is too old for openat2 storage confinement ($TELOS_KERNEL_MIN or newer required)" \
            "Boot a newer Ubuntu kernel."
  fi
}

check_container_runtime() {
  if ! command -v podman >/dev/null 2>&1; then
    if command -v docker >/dev/null 2>&1; then
      pf_fail "Docker is present but is not a supported runtime (Podman $TELOS_PODMAN_MIN or newer is required)" \
              "Install a verified Podman $TELOS_PODMAN_MIN+ (see documentation/operations/install.md); Docker is never used as a fallback."
    else
      pf_fail "no container runtime: Podman $TELOS_PODMAN_MIN or newer is required" \
              "Provide a verified Podman $TELOS_PODMAN_MIN+ (see documentation/operations/install.md). Ubuntu 24.04's default archive ships an older Podman, so 'apt install podman' alone does not satisfy this."
    fi
    return
  fi
  local version; version="$(first_version "$(podman --version 2>/dev/null)")"
  if [ -z "$version" ]; then
    pf_fail "podman is installed but its version could not be read" "Run: podman --version"
  elif version_ge "$version" "$TELOS_PODMAN_MIN"; then
    pf_ok "container runtime: podman $version"
  else
    pf_fail "podman $version is older than the required $TELOS_PODMAN_MIN" \
            "Install Podman $TELOS_PODMAN_MIN or newer from a verified source (see documentation/operations/install.md); do not mix distribution repositories."
  fi
  if preflight_is_production && [ "${PREFLIGHT_REQUIRE_ROOT:-0}" = "1" ]; then
    if [ "$(id -u)" -ne 0 ]; then
      pf_fail "production operation runs rootful Podman; this command is not root" "Re-run with: sudo telos ..."
    elif [ "$(podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null)" != "false" ]; then
      pf_fail "podman reports rootless operation; production uses rootful Podman" "Run telos through sudo so Podman uses the system socket and storage."
    else
      pf_ok "podman: rootful"
    fi
  fi
}

check_compose() {
  local provider="${PODMAN_COMPOSE_PROVIDER:-}"
  [ -n "$provider" ] || provider="$(command -v podman-compose 2>/dev/null || true)"
  if [ -z "$provider" ]; then
    pf_fail "no podman-compose provider found (the pinned provider is podman-compose $TELOS_COMPOSE_PIN)" \
            "Install podman-compose $TELOS_COMPOSE_PIN and set PODMAN_COMPOSE_PROVIDER to its absolute path (see documentation/operations/install.md for the pinned artifact)."
    return
  fi
  case "$provider" in
    /*) ;;
    *) pf_fail "PODMAN_COMPOSE_PROVIDER must be an absolute path (got '$provider')" "export PODMAN_COMPOSE_PROVIDER=\$(command -v podman-compose)"; return ;;
  esac
  if [ ! -x "$provider" ]; then
    pf_fail "the compose provider $provider is not executable" "chmod +x $provider"
    return
  fi
  # The real provider prints Podman's own version first ("podman version 5.7.0"),
  # then "podman-compose version 1.6.0"; read the provider's line, not the first.
  local version; version="$(first_version "$("$provider" --version 2>/dev/null | grep -i 'podman-compose' | head -n1)")"
  if [ "$version" = "$TELOS_COMPOSE_PIN" ]; then
    pf_ok "compose provider: podman-compose $version ($provider)"
  elif preflight_is_production; then
    pf_fail "compose provider is podman-compose ${version:-unknown}; the certified pin is $TELOS_COMPOSE_PIN" \
            "Install podman-compose $TELOS_COMPOSE_PIN and point PODMAN_COMPOSE_PROVIDER at it. The Ubuntu archive's podman-compose (1.0.6 on 24.04, 1.5.0 on 26.04) is not accepted."
  else
    pf_warn "compose provider is podman-compose ${version:-unknown}, not the certified pin $TELOS_COMPOSE_PIN (acceptable for development only)"
  fi
}

# Production needs public 80/443. If anything already listens there, say what, and
# do not "fix" it by silently moving Traefik to alternate ports.
check_public_ports() {
  preflight_is_production || return 0
  command -v ss >/dev/null 2>&1 || return 0
  local port listeners
  # On a running node the listener is our own Traefik; that is not a conflict.
  if command -v podman >/dev/null 2>&1 \
     && [ -n "$(podman ps --filter name='^telos-traefik$' --filter status=running --format '{{.ID}}' 2>/dev/null)" ]; then
    pf_ok "public ports 80/443 are held by the running telos-traefik"
    return 0
  fi
  for port in 80 443; do
    listeners="$(ss -Hltn "sport = :$port" 2>/dev/null | awk '{print $4}' | sort -u | tr '\n' ' ')"
    if [ -n "$listeners" ]; then
      pf_fail "port $port is already in use ($listeners); Traefik must own the public $port" \
              "Stop whatever listens on :$port (find it with: sudo ss -ltnp 'sport = :$port'). Telos does not fall back to alternate public ports in production."
    else
      pf_ok "port $port is free for Traefik"
    fi
  done
}

# Production sizing: at least 4 CPU cores, 16 GiB of memory and 20 GiB free where the
# shared storage lives. TELOS_ALLOW_UNDERSIZED_HOST=1 turns the failure into a warning
# so a rehearsal VM smaller than the product minimum can still run the install; it is
# never set by the installer and a node that needs it is not a supported node.
check_host_resources() {
  preflight_is_production || return 0
  local cpus mem_kib path free_kib problems=()
  cpus="${TELOS_HOST_CPUS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo 0)}"
  mem_kib="${TELOS_HOST_MEMORY_KIB:-$(awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null || echo 0)}"
  path="${STORAGE_PATH:-/mnt/storage/shared}"
  while [ ! -d "$path" ] && [ "$path" != "/" ]; do path="$(dirname "$path")"; done
  free_kib="${TELOS_STORAGE_FREE_KIB:-$(df -Pk "$path" 2>/dev/null | awk 'NR==2 {print $4}')}"
  [ "${cpus:-0}" -ge "$TELOS_MIN_CPUS" ] 2>/dev/null || problems+=("$cpus CPU core(s), need $TELOS_MIN_CPUS")
  [ "${mem_kib:-0}" -ge $((TELOS_MIN_MEMORY_GIB * 1000000)) ] 2>/dev/null || problems+=("$((${mem_kib:-0} / 1048576)) GiB of memory, need $TELOS_MIN_MEMORY_GIB")
  [ "${free_kib:-0}" -ge $((TELOS_MIN_STORAGE_GIB * 1048576)) ] 2>/dev/null || problems+=("$((${free_kib:-0} / 1048576)) GiB free for shared storage, need $TELOS_MIN_STORAGE_GIB")
  if [ "${#problems[@]}" -eq 0 ]; then
    pf_ok "resources: $cpus CPU cores, $((mem_kib / 1048576)) GiB memory, $((free_kib / 1048576)) GiB free storage"
  elif [ "${TELOS_ALLOW_UNDERSIZED_HOST:-0}" = "1" ]; then
    pf_warn "undersized host (rehearsal only): ${problems[*]}"
  else
    pf_fail "host too small for the full profile: ${problems[*]}" \
            "use a host with at least $TELOS_MIN_CPUS cores, $TELOS_MIN_MEMORY_GIB GiB of memory and $TELOS_MIN_STORAGE_GIB GiB free at the storage path; smaller hosts are unsupported rather than overcommitted"
  fi
}

# --- rootless development extras ---------------------------------------------------
# Rootless podman on ext4/xfs cannot use kernel overlay and needs fuse-overlayfs.
check_overlay_driver() {
  command -v podman >/dev/null 2>&1 || return 0
  [ "$(id -u)" -eq 0 ] && return 0
  local fstype; fstype="$(stat -f -c %T "$HOME" 2>/dev/null || echo unknown)"
  case "$fstype" in btrfs|zfs|overlayfs) pf_ok "storage driver: native overlay ok on $fstype"; return 0 ;; esac
  if command -v fuse-overlayfs >/dev/null 2>&1; then
    pf_ok "storage driver: fuse-overlayfs present (needed on $fstype)"
  else
    pf_fail "fuse-overlayfs missing -- rootless podman cannot use overlay on $fstype" "sudo apt install fuse-overlayfs"
  fi
}

check_tun_module() {
  command -v podman >/dev/null 2>&1 || return 0
  [ "$(id -u)" -eq 0 ] && return 0
  if [ -c /dev/net/tun ]; then
    pf_ok "kernel: /dev/net/tun present"
  else
    pf_fail "/dev/net/tun missing -- rootless container networking will fail" \
            "sudo modprobe tun    # persist: echo tun | sudo tee /etc/modules-load.d/tun.conf"
  fi
}

check_registries() {
  command -v podman >/dev/null 2>&1 || return 0
  local found=0 f
  for f in "$HOME/.config/containers/registries.conf" /etc/containers/registries.conf; do
    [ -r "$f" ] || continue
    grep -qE '^[[:space:]]*unqualified-search-registries' "$f" 2>/dev/null && { found=1; break; }
  done
  if [ "$found" -eq 1 ]; then
    pf_ok "podman: unqualified-search-registries configured"
  else
    pf_fail "podman cannot resolve short image names (no unqualified-search-registries)" \
            "mkdir -p ~/.config/containers && printf 'unqualified-search-registries = [\"docker.io\"]\\n' > ~/.config/containers/registries.conf"
  fi
}

check_privileged_ports() {
  [ "$(id -u)" -eq 0 ] && return 0
  command -v podman >/dev/null 2>&1 || return 0
  local start; start="$(sysctl -n net.ipv4.ip_unprivileged_port_start 2>/dev/null || echo 1024)"
  if [ "$start" -le 80 ] 2>/dev/null; then
    pf_ok "ports: unprivileged binding allowed from $start"
  else
    pf_warn "rootless runtime cannot bind :80/:443 (unprivileged start = $start); development uses alternate ports"
  fi
}

check_tool() {
  local tool="$1" pkg="${2:-$1}" required="${3:-required}"
  if command -v "$tool" >/dev/null 2>&1; then
    pf_ok "tool: $tool"
  elif [ "$required" = "required" ]; then
    pf_fail "missing required tool: $tool" "sudo apt install $pkg"
  else
    pf_warn "missing optional tool: $tool"
  fi
}

# run_preflight — all host checks. Returns non-zero if any hard failure.
run_preflight() {
  printf '%sHost prerequisites%s  %s(%s)%s\n\n' \
    "$C_BOLD" "$C_OFF" "$C_DIM" "$(detect_distro)" "$C_OFF"

  check_host_support
  check_container_runtime
  check_compose
  check_public_ports
  check_host_resources
  if ! preflight_is_production; then
    check_overlay_driver
    check_tun_module
    check_registries
    check_privileged_ports
  fi
  check_tool openssl openssl required
  check_tool python3 python3 required
  check_tool flock util-linux required
  check_tool ss iproute2 required
  check_tool curl curl optional

  printf '\n'
  if [ "$PREFLIGHT_FAILURES" -gt 0 ]; then
    printf '%s%d prerequisite(s) not met.%s What to do:\n\n' "$C_BAD" "$PREFLIGHT_FAILURES" "$C_OFF"
    local h
    for h in "${PREFLIGHT_HINTS[@]}"; do printf '    %s\n' "$h"; done
    printf '\n'
    return 1
  fi
  return 0
}
