# shellcheck shell=bash
# Host prerequisite detection, shared by install.sh and `telos doctor` so the
# two can never disagree about what a working host looks like.
#
# Design rule: DETECT AND REPORT, NEVER AUTO-INSTALL. Installing system
# packages means owning a distro matrix (pacman/apt/dnf/zypper) and running
# privileged package operations the user did not ask for. Instead every failure
# carries the exact command for the detected distro, and the user runs it.

PREFLIGHT_FAILURES=0
PREFLIGHT_WARNINGS=0
PREFLIGHT_HINTS=()

# --- distro detection ------------------------------------------------------
detect_distro() {
  if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    printf '%s' "${ID:-unknown}"
  else
    printf 'unknown'
  fi
}

distro_family() {
  local id; id="$(detect_distro)"
  case "$id" in
    arch|manjaro|endeavouros|cachyos)      echo arch ;;
    debian|ubuntu|linuxmint|pop|raspbian)  echo debian ;;
    fedora|rhel|centos|rocky|almalinux)    echo fedora ;;
    opensuse*|sles)                        echo suse ;;
    *)
      # Fall back to ID_LIKE for derivatives we do not know by name.
      if [ -r /etc/os-release ]; then
        # shellcheck disable=SC1091
        . /etc/os-release
        case "${ID_LIKE:-}" in
          *arch*)   echo arch ;;
          *debian*|*ubuntu*) echo debian ;;
          *fedora*|*rhel*)   echo fedora ;;
          *suse*)   echo suse ;;
          *)        echo unknown ;;
        esac
      else
        echo unknown
      fi
      ;;
  esac
}

# install_hint <package-keyword> — the command to install a thing on this host.
install_hint() {
  local pkg="$1"
  case "$(distro_family)" in
    arch)   echo "sudo pacman -S --needed $pkg" ;;
    debian) echo "sudo apt install $pkg" ;;
    fedora) echo "sudo dnf install $pkg" ;;
    suse)   echo "sudo zypper install $pkg" ;;
    *)      echo "install '$pkg' with your distribution's package manager" ;;
  esac
}

# --- reporting -------------------------------------------------------------
pf_ok()   { printf '  %s  %s\n' "$(paint ok)" "$1"; }
pf_warn() { printf '  %s  %s\n' "$(paint warn)" "$1"; PREFLIGHT_WARNINGS=$((PREFLIGHT_WARNINGS + 1)); }
pf_fail() {
  printf '  %s  %s\n' "$(paint bad)" "$1"
  PREFLIGHT_FAILURES=$((PREFLIGHT_FAILURES + 1))
  [ -n "${2:-}" ] && PREFLIGHT_HINTS+=("$2")
  return 0
}

# --- individual checks -----------------------------------------------------
check_container_runtime() {
  if command -v podman >/dev/null 2>&1; then
    pf_ok "container runtime: podman $(podman --version 2>/dev/null | awk '{print $3}')"
    return 0
  elif command -v docker >/dev/null 2>&1; then
    pf_ok "container runtime: docker $(docker --version 2>/dev/null | awk '{print $3}' | tr -d ,)"
    return 0
  fi
  pf_fail "no container runtime (need podman or docker)" "$(install_hint podman)"
}

check_compose() {
  if command -v podman-compose >/dev/null 2>&1; then
    pf_ok "compose driver: podman-compose"
  elif docker compose version >/dev/null 2>&1; then
    pf_ok "compose driver: docker compose"
  elif command -v docker-compose >/dev/null 2>&1; then
    pf_ok "compose driver: docker-compose"
  else
    pf_fail "no compose driver" "$(install_hint podman-compose)"
  fi
}

# Rootless podman on an ext4/xfs filesystem cannot use the kernel overlay
# driver and needs fuse-overlayfs. This is the single most common first-run
# failure on distros that do not ship it by default.
check_overlay_driver() {
  command -v podman >/dev/null 2>&1 || return 0
  [ "$(id -u)" -eq 0 ] && return 0

  local fstype
  fstype="$(stat -f -c %T "$HOME" 2>/dev/null || echo unknown)"
  case "$fstype" in
    btrfs|zfs|overlayfs)
      pf_ok "storage driver: native overlay ok on $fstype"
      return 0
      ;;
  esac

  if command -v fuse-overlayfs >/dev/null 2>&1; then
    pf_ok "storage driver: fuse-overlayfs present (needed on $fstype)"
  else
    pf_fail "fuse-overlayfs missing — rootless podman cannot use overlay on $fstype" \
            "$(install_hint fuse-overlayfs)"
  fi
}

# Rootless podman networking (pasta/slirp4netns) needs /dev/net/tun.
check_tun_module() {
  command -v podman >/dev/null 2>&1 || return 0
  [ "$(id -u)" -eq 0 ] && return 0

  if [ -c /dev/net/tun ]; then
    pf_ok "kernel: /dev/net/tun present"
  else
    pf_fail "/dev/net/tun missing — rootless container networking will fail" \
            "sudo modprobe tun    # persist: echo tun | sudo tee /etc/modules-load.d/tun.conf"
  fi
}

# Some distros (notably Arch) ship registries.conf fully commented out, so
# short image names like "postgres:16-alpine" do not resolve.
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
            "mkdir -p ~/.config/containers && printf 'unqualified-search-registries = [\"docker.io\"]\n' > ~/.config/containers/registries.conf"
  fi
}

# Privileged ports under a rootless runtime — the Traefik binding problem.
check_privileged_ports() {
  [ "$(id -u)" -eq 0 ] && return 0
  command -v podman >/dev/null 2>&1 || return 0

  local start
  start="$(sysctl -n net.ipv4.ip_unprivileged_port_start 2>/dev/null || echo 1024)"
  if [ "$start" -le 80 ] 2>/dev/null; then
    pf_ok "ports: unprivileged binding allowed from $start"
  else
    pf_warn "rootless runtime cannot bind :80/:443 (unprivileged start = $start)"
    PREFLIGHT_HINTS+=("Set TRAEFIK_HTTP_PORT / TRAEFIK_HTTPS_PORT in .env (telos init does this), or: sudo sysctl -w net.ipv4.ip_unprivileged_port_start=80")
  fi
}

check_tool() {
  local tool="$1" pkg="${2:-$1}" required="${3:-required}"
  if command -v "$tool" >/dev/null 2>&1; then
    pf_ok "tool: $tool"
  elif [ "$required" = "required" ]; then
    pf_fail "missing required tool: $tool" "$(install_hint "$pkg")"
  else
    pf_warn "missing optional tool: $tool"
  fi
}

# run_preflight — all host checks. Returns non-zero if any hard failure.
run_preflight() {
  printf '%sHost prerequisites%s  %s(%s)%s\n\n' \
    "$C_BOLD" "$C_OFF" "$C_DIM" "$(detect_distro)" "$C_OFF"

  check_container_runtime
  check_compose
  check_overlay_driver
  check_tun_module
  check_registries
  check_privileged_ports
  check_tool openssl openssl required
  check_tool python3 python required
  check_tool curl curl optional

  printf '\n'
  if [ "$PREFLIGHT_FAILURES" -gt 0 ]; then
    printf '%s%d prerequisite(s) missing.%s Run these, then try again:\n\n' \
      "$C_BAD" "$PREFLIGHT_FAILURES" "$C_OFF"
    local h
    for h in "${PREFLIGHT_HINTS[@]}"; do printf '    %s\n' "$h"; done
    printf '\n'
    return 1
  fi

  if [ "$PREFLIGHT_WARNINGS" -gt 0 ] && [ ${#PREFLIGHT_HINTS[@]} -gt 0 ]; then
    printf '%sNotes:%s\n' "$C_DIM" "$C_OFF"
    local h
    for h in "${PREFLIGHT_HINTS[@]}"; do printf '    %s\n' "$h"; done
    printf '\n'
  fi
  return 0
}
