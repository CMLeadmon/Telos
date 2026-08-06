# shellcheck shell=bash
# summary: Shut the stack down

usage_stop() {
  cat <<'EOF'
telos stop — shut the stack down

Usage:
  telos stop [--dev] [--volumes]

Stops and removes the containers. Volumes are preserved by default, so the
database, migrated schema, and media survive; the next `telos start` picks up
where this left off.

Options:
  --volumes  ALSO DELETE EVERY VOLUME. This destroys the database, all
             configuration, and Jellyfin/Grimmory state. Requires typed
             confirmation.
EOF
}

run() {
  local drop_volumes=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --volumes) drop_volumes=1 ;;
      -h|--help) usage_stop; return 0 ;;
      *)         die "stop: unknown option '$1'" ;;
    esac
    shift
  done

  local args=(down)

  if [ "$drop_volumes" -eq 1 ]; then
    # Irreversible and easy to type by accident, so make it deliberate.
    printf '%sThis deletes every Telos volume: the database, all schema and\n' "$C_BAD"
    printf 'migration state, and Jellyfin/Grimmory configuration.%s\n' "$C_OFF"
    printf 'This cannot be undone.\n\n'
    printf "Type 'delete my data' to continue: "
    local reply; read -r reply
    if [ "$reply" != "delete my data" ]; then
      info "Aborted; nothing was removed."
      return 1
    fi
    args+=(--volumes)
  fi

  info "Stopping Telos stack..."
  if ! compose "${args[@]}"; then
    warn "compose reported an error while shutting down"
    return 1
  fi

  if [ "$drop_volumes" -eq 1 ]; then
    info "Stack stopped and all volumes deleted."
  else
    info "Stack stopped. Volumes preserved — 'telos start' resumes from here."
  fi
  return 0
}
