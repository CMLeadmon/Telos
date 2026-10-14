# shellcheck shell=bash
# Shared helpers for every telos subcommand. Sourced by the dispatcher before
# the command file, so anything defined here is available to all commands.
#
# Deliberately dependency-free: a container runtime and (optionally) curl. The
# CLI must stay usable when the stack itself is broken, so nothing here may
# require the gateway, a build step, or a display server.

# --- presentation ----------------------------------------------------------
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  C_OK=$'\033[32m'; C_BAD=$'\033[31m'; C_WARN=$'\033[33m'
  C_DIM=$'\033[90m'; C_BOLD=$'\033[1m'; C_OFF=$'\033[0m'
else
  C_OK=""; C_BAD=""; C_WARN=""; C_DIM=""; C_BOLD=""; C_OFF=""
fi

I_OK="✔"; I_BAD="✘"; I_WARN="!"; I_NONE="·"

# paint <ok|bad|warn|*> — status icon in the matching color.
paint() {
  case "$1" in
    ok)   printf '%s%s%s' "$C_OK" "$I_OK" "$C_OFF" ;;
    bad)  printf '%s%s%s' "$C_BAD" "$I_BAD" "$C_OFF" ;;
    warn) printf '%s%s%s' "$C_WARN" "$I_WARN" "$C_OFF" ;;
    *)    printf '%s%s%s' "$C_DIM" "$I_NONE" "$C_OFF" ;;
  esac
}

info() { printf '%s\n' "$*"; }
note() { printf '%s%s%s\n' "$C_DIM" "$*" "$C_OFF"; }
warn() { printf '%s%s%s\n' "$C_WARN" "$*" "$C_OFF" >&2; }

# die <message> — exit 2, reserved for "the CLI itself could not run".
die() { printf '%s\n' "telos: $*" >&2; exit 2; }

# --- container runtime -----------------------------------------------------
# Podman is the only supported runtime (Ubuntu 24.04+ server, Podman 5+). Docker
# is never used as a fallback: its Compose semantics are not what the node is
# certified against.
detect_runtime() {
  command -v podman >/dev/null 2>&1 || die "podman not found on PATH (Podman 5+ is required)"
  printf 'podman'
}

# compose_prepare — verify the runtime contract and set COMPOSE_CMD.
#   * production runs rootful (`sudo telos ...`); development may be rootless;
#   * the Compose provider is an absolute, operator-verified podman-compose
#     (PODMAN_COMPOSE_PROVIDER) so `podman compose` cannot fall through to Docker.
compose_prepare() {
  command -v podman >/dev/null 2>&1 || die "podman not found on PATH (Podman 5+ is required)"
  local provider="${PODMAN_COMPOSE_PROVIDER:-}"
  [ -n "$provider" ] || provider="$(command -v podman-compose 2>/dev/null || true)"
  [ -n "$provider" ] || die "no podman-compose provider found; install the pinned provider and set PODMAN_COMPOSE_PROVIDER to its absolute path"
  case "$provider" in
    /*) ;;
    *)  die "PODMAN_COMPOSE_PROVIDER must be an absolute path (got '$provider')" ;;
  esac
  [ -x "$provider" ] || die "PODMAN_COMPOSE_PROVIDER is not executable: $provider"
  export PODMAN_COMPOSE_PROVIDER="$provider"
  if [ "${TELOS_DEV:-0}" != "1" ]; then
    [ "$(id -u)" -eq 0 ] || die "production operation uses rootful Podman; run with sudo (or add --dev for a rootless development node)"
    [ "$(podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null)" = "false" ] \
      || die "podman is running rootless; production uses rootful Podman (sudo telos ...)"
  fi
  COMPOSE_CMD=(podman compose)
}

# compose_with [OVERLAY...] -- ARG... — run Compose with the base model exactly
# once plus the named repository-relative overlays, always against this node's
# node.env and a stable project name so releases can be swapped under it.
compose_with() {
  local overlays=() overlay
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do overlays+=("$1"); shift; done
  [ "${1:-}" = "--" ] || die "compose_with: overlays must be followed by --"
  shift
  compose_prepare
  require_node_env
  local files=(-f "$REPO_ROOT/docker-compose.yml")
  for overlay in ${overlays+"${overlays[@]}"}; do
    case "$overlay" in
      ""|/*|*..*) die "compose_with: overlay must be a repository-relative path (got '$overlay')" ;;
    esac
    [ -f "$REPO_ROOT/$overlay" ] || die "compose_with: overlay not found: $overlay"
    files+=(-f "$REPO_ROOT/$overlay")
  done
  # podman-compose 1.6.0 has no --project-directory: the project directory is the
  # directory of the first -f file, which is the release root here, so relative bind
  # mounts (./config, ./deploy) resolve against it exactly as they would elsewhere.
  "${COMPOSE_CMD[@]}" --env-file "$(node_env_path)" -p telos "${files[@]}" "$@"
}

# compose [args...] — compose_with plus the development overlay when --dev.
compose() {
  local overlays=()
  if [ "${TELOS_DEV:-0}" = "1" ]; then
    [ -f "$REPO_ROOT/docker-compose.dev.yml" ] || die "TELOS_DEV=1 but docker-compose.dev.yml is missing"
    overlays+=(docker-compose.dev.yml)
  fi
  # A node served by an operator-managed private CA says so in node.env; every
  # command (start, status, stop) then layers the same overlay, so none of them can
  # disagree about how Traefik gets its certificate.
  if [ "$(env_value "$(node_env_path)" TELOS_PRIVATE_CA 2>/dev/null || true)" = "1" ]; then
    overlays+=(docker-compose.private-ca.yml)
  fi
  compose_with ${overlays+"${overlays[@]}"} -- "$@"
}

# --- compose introspection -------------------------------------------------
# Expected container names come straight from the compose file so commands
# cannot drift out of sync as services are added or removed.
expected_containers() {
  local file="$REPO_ROOT/docker-compose.yml"
  [ -f "$file" ] || die "cannot find $file"
  sed -n 's/^[[:space:]]*container_name:[[:space:]]*\([^[:space:]]*\).*/\1/p' "$file"
}

# Services that run once and exit; a clean exit 0 is success, not failure.
TELOS_ONESHOT="telos-migrate"
is_oneshot() {
  case " $TELOS_ONESHOT " in *" $1 "*) return 0 ;; *) return 1 ;; esac
}

# require_env_file — retained for callers that predate node.env; it now means
# "this node has a node.env".
require_env_file() { require_node_env; }

# prepare_volume_ownership — give a named volume the owner its non-root container runs
# as, for the volumes whose mount point does not exist in the image (so Podman has no
# ownership to copy into them). Compose applies a volume's driver options only when the
# volume is created, so a volume made by an earlier release keeps its old owner and a
# non-root Traefik or Grimmory could not write to it. Only the volume's own directory
# tree is touched, only by root, and only when the top-level owner differs.
prepare_volume_ownership() {
  [ "$(id -u)" -eq 0 ] || return 0
  [ "${TELOS_DEV:-0}" = "1" ] && return 0
  local runtime env_file app_uid app_gid spec volume owner mountpoint current
  runtime="$(detect_runtime)"
  env_file="$(node_env_path)"
  app_uid="$(env_value "$env_file" APP_UID 2>/dev/null)" || app_uid=""
  app_gid="$(env_value "$env_file" APP_GID 2>/dev/null)" || app_gid=""
  for spec in "traefik_acme 65532:65532" "grimmory_config ${app_uid:-1000}:${app_gid:-1000}"; do
    volume="telos_${spec%% *}"
    owner="${spec#* }"
    mountpoint="$($runtime volume inspect "$volume" --format '{{.Mountpoint}}' 2>/dev/null)" || continue
    [ -n "$mountpoint" ] && [ -d "$mountpoint" ] || continue
    current="$(stat -c '%u:%g' "$mountpoint" 2>/dev/null)" || continue
    if [ "$current" != "$owner" ]; then
      note "giving volume ${spec%% *} to $owner (was $current)"
      chown -R "$owner" "$mountpoint" || warn "could not change the owner of volume ${spec%% *}"
    fi
  done
}

# private_ca_unreadable — print the first private-CA path that Traefik's unprivileged
# user (UID 65532) cannot read, and succeed; fail (print nothing) when there is nothing
# wrong or nothing to check. Traefik no longer runs as root, so a key that is 0600 root,
# or a directory that is 0700, leaves it without a certificate: HTTPS then fails with
# "unrecognized name" while every container reports healthy.
private_ca_unreadable() {
  [ "$(id -u)" -eq 0 ] || return 1
  command -v setpriv >/dev/null 2>&1 || return 1
  local env_file dir path test_flag
  env_file="$(node_env_path)"
  [ "$(env_value "$env_file" TELOS_PRIVATE_CA 2>/dev/null || true)" = "1" ] || return 1
  dir="$(env_value "$env_file" TELOS_TLS_PATH 2>/dev/null || true)"
  [ -n "$dir" ] || return 1
  for path in "$dir" "$dir/server.crt" "$dir/server.key"; do
    test_flag="-r"; [ "$path" = "$dir" ] && test_flag="-x"
    if ! setpriv --reuid=65532 --regid=65532 --clear-groups test "$test_flag" "$path" 2>/dev/null; then
      printf '%s' "$path"
      return 0
    fi
  done
  return 1
}
