# shellcheck shell=bash
# summary: Generate .env and create the storage tree

# Variables that must receive an independently-random secret. Every one of
# these is "change-me" in .env.example; leaving any at the placeholder is a
# production credential leak, so they are generated, never prompted.
INIT_SECRETS=(
  POSTGRES_PASSWORD
  TELOS_OWNER_PASSWORD
  TELOS_RUNTIME_PASSWORD
  REDIS_PASSWORD
  GRIMMORY_ADMIN_PASSWORD
  GRIMMORY_DB_PASSWORD
  MARIADB_ROOT_PASSWORD
  TELOS_BOOTSTRAP_TOKEN
)

usage_init() {
  cat <<'EOF'
telos init — prepare this node

Usage:
  telos init [--dev] [--domain HOST] [--storage PATH] [--force]

Creates .env from .env.example with freshly generated secrets, and creates the
storage tree. Idempotent in the sense that it refuses to overwrite an existing
.env unless you pass --force.

Options:
  --dev            Configure for local development: TELOS_ENV=development,
                   domain localhost, unprivileged Traefik ports.
  --domain HOST    Public domain (production).
  --storage PATH   Host path backing media/books/files.
  --force          Overwrite an existing .env. The old one is backed up first.

Not generated, because they come from provisioning the upstream services after
first boot: JELLYFIN_ADMIN_TOKEN, JELLYFIN_LIBRARY_IDS, GRIMMORY_LIBRARY_IDS.
The gateway reports those as degraded until you fill them in — by design; it
never fabricates catalog records.
EOF
}

gen_secret() {
  command -v openssl >/dev/null 2>&1 || die "openssl is required to generate secrets"
  openssl rand -hex 24
}

# set_env_var <file> <key> <value> — replace in place, or append if absent.
set_env_var() {
  local file="$1" key="$2" value="$3"
  if grep -qE "^${key}=" "$file"; then
    # Value goes through a temp file rather than sed -i with the secret on the
    # command line, so it never appears in the process table.
    local tmp; tmp="$(mktemp)"
    KEY="$key" VALUE="$value" python3 -c '
import os, sys
key, value = os.environ["KEY"], os.environ["VALUE"]
for line in sys.stdin:
    if line.startswith(key + "="):
        sys.stdout.write("%s=%s\n" % (key, value))
    else:
        sys.stdout.write(line)
' < "$file" > "$tmp" && mv "$tmp" "$file"
  else
    printf '%s=%s\n' "$key" "$value" >> "$file"
  fi
}

run() {
  local dev=0 force=0 domain="" storage=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --dev)     dev=1 ;;
      --force)   force=1 ;;
      --domain)  [ $# -ge 2 ] || die "init: --domain needs a value"; domain="$2"; shift ;;
      --storage) [ $# -ge 2 ] || die "init: --storage needs a value"; storage="$2"; shift ;;
      -h|--help) usage_init; return 0 ;;
      *) die "init: unknown option '$1'" ;;
    esac
    shift
  done

  # TELOS_DEV is the global flag; --dev here means the same thing.
  [ "${TELOS_DEV:-0}" = "1" ] && dev=1

  local env_file="$REPO_ROOT/.env"
  local example="$REPO_ROOT/.env.example"
  [ -f "$example" ] || die "cannot find $example"

  printf '%sInitializing this Telos node%s\n\n' "$C_BOLD" "$C_OFF"

  # --- guard an existing node ---------------------------------------------
  if [ -f "$env_file" ] && [ "$force" -eq 0 ]; then
    printf '  %s  .env already exists\n\n' "$(paint warn)"
    printf 'Regenerating it would issue new database passwords that no longer\n'
    printf 'match the credentials already stored in your Postgres volume, and the\n'
    printf 'stack would fail to start.\n\n'
    printf 'If this node is already set up, you are done — run: telos start\n'
    printf 'To deliberately start over: telos init --force\n'
    return 1
  fi

  if [ -f "$env_file" ] && [ "$force" -eq 1 ]; then
    local backup="$env_file.bak.$(date +%Y%m%d%H%M%S)"
    cp -p "$env_file" "$backup" || die "could not back up existing .env"
    printf '  %s  backed up existing .env to %s\n' "$(paint ok)" "$(basename "$backup")"
  fi

  # --- resolve settings ----------------------------------------------------
  if [ "$dev" -eq 1 ]; then
    domain="${domain:-localhost}"
  elif [ -z "$domain" ]; then
    printf 'Public domain for this node (e.g. library.example.com): '
    read -r domain
    [ -n "$domain" ] || die "a domain is required for a production node (or use --dev)"
  fi

  if [ -z "$storage" ]; then
    local default_storage="$HOME/telos-storage"
    printf 'Storage path for media and books [%s]: ' "$default_storage"
    read -r storage
    storage="${storage:-$default_storage}"
  fi

  # --- write .env ----------------------------------------------------------
  # Built in a temp file and moved into place only once every value is set. An
  # interrupted init (Ctrl-C, closed pipe, failed openssl) must never leave a
  # half-written .env behind: it would look initialized while still holding
  # placeholder passwords, which is far worse than having no .env at all.
  umask 077
  # Sweep any staging file orphaned by a previously killed run.
  rm -f "${env_file}".staging.* 2>/dev/null

  local staged; staged="$(mktemp "${env_file}.staging.XXXXXX")" \
    || die "could not create a staging file next to $env_file"
  # RETURN covers a normal early return; the signal traps cover being killed
  # mid-write (Ctrl-C, SIGPIPE from a closed pipe, SIGTERM).
  # shellcheck disable=SC2064
  trap "rm -f '$staged'" RETURN EXIT INT TERM HUP PIPE
  chmod 600 "$staged"
  cat "$example" > "$staged" || die "could not read $example"

  # Everything below writes to $staged; $env_file is untouched until the end.
  local env_file_final="$env_file"
  env_file="$staged"

  printf '\n%sGenerating secrets%s\n' "$C_BOLD" "$C_OFF"
  local key
  for key in "${INIT_SECRETS[@]}"; do
    set_env_var "$env_file" "$key" "$(gen_secret)"
    printf '  %s  %s\n' "$(paint ok)" "$key"
  done

  printf '\n%sConfiguring%s\n' "$C_BOLD" "$C_OFF"
  set_env_var "$env_file" TELOS_DOMAIN "$domain"
  set_env_var "$env_file" STORAGE_PATH "$storage"
  set_env_var "$env_file" APP_UID "$(id -u)"
  set_env_var "$env_file" APP_GID "$(id -g)"

  local tz; tz="$(timedatectl show -p Timezone --value 2>/dev/null || echo Etc/UTC)"
  set_env_var "$env_file" TZ "$tz"

  if [ "$dev" -eq 1 ]; then
    set_env_var "$env_file" TELOS_ENV development
    set_env_var "$env_file" TELOS_PUBLIC_ORIGIN "http://localhost:8080"
    set_env_var "$env_file" TELOS_DEV_ORIGINS "http://localhost:3000,http://127.0.0.1:3000"
    set_env_var "$env_file" ACME_EMAIL "dev@localhost"
    # Upstream services are not provisioned yet; blank is honest, "change-me"
    # is not. The gateway reports these as degraded rather than faking data.
    set_env_var "$env_file" JELLYFIN_ADMIN_TOKEN ""
    printf '  %s  development mode (localhost, loopback origins)\n' "$(paint ok)"
  else
    set_env_var "$env_file" TELOS_ENV production
    set_env_var "$env_file" TELOS_PUBLIC_ORIGIN "https://$domain"
    if [ -z "${ACME_EMAIL:-}" ]; then
      printf 'Email for Let'\''s Encrypt certificate notices: '
      read -r acme
      [ -n "$acme" ] && set_env_var "$env_file" ACME_EMAIL "$acme"
    fi
    set_env_var "$env_file" JELLYFIN_ADMIN_TOKEN ""
    printf '  %s  production mode (%s)\n' "$(paint ok)" "$domain"
  fi

  # Rootless runtimes cannot bind :80/:443; pick reachable ports instead of
  # leaving a stack that cannot start.
  local port_start
  port_start="$(sysctl -n net.ipv4.ip_unprivileged_port_start 2>/dev/null || echo 1024)"
  if [ "$(id -u)" -ne 0 ] && [ "$port_start" -gt 80 ] 2>/dev/null; then
    set_env_var "$env_file" TRAEFIK_HTTP_PORT 8081
    set_env_var "$env_file" TRAEFIK_HTTPS_PORT 8443
    printf '  %s  Traefik on 8081/8443 (rootless cannot bind 80/443)\n' "$(paint warn)"
  fi

  # Commit: every value is set, so the staged file becomes the real .env in one
  # atomic rename. Past this point an interruption leaves a complete .env.
  mv "$staged" "$env_file_final" || die "could not install $env_file_final"
  trap - RETURN EXIT INT TERM HUP PIPE
  env_file="$env_file_final"
  printf '  %s  wrote .env (mode 0600)\n' "$(paint ok)"

  # --- storage tree --------------------------------------------------------
  printf '\n%sStorage%s\n' "$C_BOLD" "$C_OFF"
  local d
  for d in "" /media /books /bookdrop; do
    local path="${storage}${d}"
    if [ -d "$path" ]; then
      printf '  %s  %s %s(exists)%s\n' "$(paint ok)" "$path" "$C_DIM" "$C_OFF"
    elif mkdir -p "$path" 2>/dev/null; then
      printf '  %s  %s %s(created)%s\n' "$(paint ok)" "$path" "$C_DIM" "$C_OFF"
    else
      printf '  %s  cannot create %s\n' "$(paint bad)" "$path"
      note "Create it yourself and re-run: mkdir -p $path"
      return 1
    fi
  done

  if [ ! -w "$storage" ]; then
    printf '  %s  %s is not writable by you\n' "$(paint bad)" "$storage"
    return 1
  fi

  # --- done ----------------------------------------------------------------
  printf '\n%s%s Node initialized%s\n\n' "$C_OK" "$I_OK" "$C_OFF"
  note "Secrets were generated into .env. Back that file up — the database"
  note "volume is encrypted against those exact credentials."
  printf '\n%sNext:%s\n' "$C_BOLD" "$C_OFF"
  if [ "$dev" -eq 1 ]; then
    printf '    telos start --dev\n'
  else
    printf '    telos start\n'
  fi
  printf '    telos status\n\n'
  note "Jellyfin and Grimmory report 'degraded' until you provision their"
  note "tokens and library IDs. That is expected on a fresh node."
  return 0
}
