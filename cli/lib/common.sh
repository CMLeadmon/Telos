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
# Resolved once and cached; every command shares the same answer.
detect_runtime() {
  if [ -n "${TELOS_RUNTIME:-}" ]; then printf '%s' "$TELOS_RUNTIME"; return; fi
  if command -v podman >/dev/null 2>&1; then TELOS_RUNTIME=podman
  elif command -v docker >/dev/null 2>&1; then TELOS_RUNTIME=docker
  else die "neither podman nor docker found on PATH"
  fi
  printf '%s' "$TELOS_RUNTIME"
}

# detect_compose — echoes the compose driver as a runnable command string.
detect_compose() {
  if command -v podman-compose >/dev/null 2>&1; then echo "podman-compose"
  elif docker compose version >/dev/null 2>&1; then echo "docker compose"
  elif command -v docker-compose >/dev/null 2>&1; then echo "docker-compose"
  else die "no compose driver found (tried podman-compose, docker compose, docker-compose)"
  fi
}

# compose [args...] — run compose with the right file set. Layers the dev
# override when TELOS_DEV=1, which is what binds host ports for local work.
compose() {
  local driver; driver="$(detect_compose)"
  local files=(-f "$REPO_ROOT/docker-compose.yml")
  if [ "${TELOS_DEV:-0}" = "1" ]; then
    [ -f "$REPO_ROOT/docker-compose.dev.yml" ] \
      || die "TELOS_DEV=1 but docker-compose.dev.yml is missing"
    files+=(-f "$REPO_ROOT/docker-compose.dev.yml")
  fi
  # shellcheck disable=SC2086
  $driver "${files[@]}" "$@"
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

# require_env_file — most commands are useless without .env, since every
# credential and path comes from it.
require_env_file() {
  [ -f "$REPO_ROOT/.env" ] || die ".env not found — copy .env.example and fill it in"
}
