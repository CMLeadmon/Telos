# shellcheck shell=bash
# summary: Bring the stack up

usage_start() {
  cat <<'EOF'
telos start — bring the stack up

Usage:
  telos start [--dev] [--build]

Options:
  --dev     Layer docker-compose.dev.yml, which binds host ports for local
            work (gateway on 127.0.0.1:8080).
  --build   Rebuild images before starting.

Starts detached, then waits for every container to leave its "starting"
health state before reporting. Exits non-zero if anything failed to come up.
EOF
}

run() {
  local build=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --build)   build=1 ;;
      -h|--help) usage_start; return 0 ;;
      *)         die "start: unknown option '$1'" ;;
    esac
    shift
  done

  require_env_file

  local args=(up -d)
  [ "$build" -eq 1 ] && args+=(--build)

  [ "${TELOS_DEV:-0}" = "1" ] && note "using docker-compose.dev.yml"
  info "Starting Telos stack..."

  if ! compose "${args[@]}"; then
    warn "compose failed to bring the stack up"
    return 1
  fi

  # Healthchecks have start periods; report only once they have settled, so
  # "started" means something. Bounded so a wedged service cannot hang us.
  local runtime; runtime="$(detect_runtime)"
  local deadline=$((SECONDS + 120))
  while [ "$SECONDS" -lt "$deadline" ]; do
    local starting=0
    while IFS= read -r name; do
      [ -n "$name" ] || continue
      case "$($runtime ps -a --format '{{.Names}}|{{.Status}}' 2>/dev/null \
              | awk -F'|' -v n="$name" '$1 == n { print $2; exit }')" in
        *"(starting)"*) starting=1 ;;
      esac
    done <<< "$(expected_containers)"
    [ "$starting" -eq 0 ] && break
    sleep 3
  done

  printf '\n'
  # Delegate the report so start and status can never disagree.
  . "$COMMAND_DIR/status.sh"
  run
}
