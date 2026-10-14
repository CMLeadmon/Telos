# shellcheck shell=bash
# summary: Apply database migrations, or report the applied schema version

usage_migrate() {
  cat <<'EOF2'
telos migrate — apply database migrations, or report the applied schema version

Usage:
  telos migrate            run the one-shot migration service (telos-core migrate)
  telos migrate --status   print the highest applied migration number and exit

The migration service needs PostgreSQL running and healthy; it does not start the
rest of the stack. `telos start` runs it anyway, so you rarely need this directly --
upgrade tooling uses it to separate "the schema moved" from "the new release is serving".
EOF2
}

run() {
  case "${1:-}" in
    "")        ;;
    --status)  ;;
    -h|--help) usage_migrate; return 0 ;;
    *)         die "migrate: unknown option '$1'" ;;
  esac
  require_node_env
  local runtime; runtime="$(detect_runtime)"
  if [ "${1:-}" = "--status" ]; then
    local version
    version="$($runtime exec telos-postgres sh -c 'exec psql --no-psqlrc -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -c "SELECT COALESCE(max(version), 0) FROM schema_migrations"' 2>/dev/null)" \
      || { warn "could not read schema_migrations (is telos-postgres running?)"; return 1; }
    case "$version" in ''|*[!0-9]*) warn "unexpected schema version '$version'"; return 1 ;; esac
    printf '%s\n' "$version"
    return 0
  fi
  info "Running migrations..."
  compose_with -- run --rm --no-deps -T telos-migrate || { warn "migrations failed"; return 1; }
  info "Migrations applied."
}
