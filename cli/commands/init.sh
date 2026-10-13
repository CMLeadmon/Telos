# shellcheck shell=bash
# summary: Create this node's configuration and signing keys (never overwrites)

usage_init() {
  cat <<'EOF'
telos init — create a new node's configuration

Usage:
  sudo telos init [--domain HOST] [--storage PATH] [--acme-email ADDR]
  telos init --dev [--storage PATH]
  sudo telos init --import-env PATH [--storage PATH]

Creates, under the node path (production /var/lib/telos, development ./.telos):

  node.env                 generated credentials and settings, mode 0600
  config/cursor-keys.json  distinct cursor + HLS signing roots, mode 0600,
  config/hls-key           owned by the gateway identity, mounted read-only
  state/                   operator journals and locks (root, mode 0700)

and, only where missing, the storage tree. init is one-way: it refuses to run if
node.env, the configuration directory or a checkout .env already exists, and it
has no force option. Regenerating credentials would desynchronize them from the
existing database volumes.

Options:
  --dev                Development node (TELOS_ENV=development, localhost).
  --domain HOST        Public domain (production).
  --storage PATH       Host path for media, books and files.
  --acme-email ADDR    Contact address for certificate notices (production).
  --import-env PATH    Adopt an existing checkout .env: its credentials are
                       copied unchanged into node.env and only the new signing
                       keys are generated. The source file is left in place.

Not generated, because they come from the upstream services after first boot:
JELLYFIN_ADMIN_TOKEN, JELLYFIN_LIBRARY_IDS, GRIMMORY_LIBRARY_IDS. Run
`telos configure integrations` to validate and record them.
EOF
}

# create_storage_tree PATH UID GID PRODUCTION — create only what is missing.
# Existing directories are never chowned or chmodded: they may hold a member
# community's media. Newly created ones are group-writable for the gateway,
# which joins group GID, so uploads and ingestion work without recursive chowns.
create_storage_tree() {
  local storage="$1" uid="$2" gid="$3" production="$4" suffix path
  for suffix in "" /media /books /bookdrop; do
    path="${storage}${suffix}"
    if [ -d "$path" ]; then
      printf '  %s  %s %s(exists, ownership untouched)%s\n' "$(paint ok)" "$path" "$C_DIM" "$C_OFF"
      continue
    fi
    if ! mkdir -p "$path" 2>/dev/null; then
      printf '  %s  cannot create %s\n' "$(paint bad)" "$path"
      note "Create it yourself and re-run `telos doctor`: mkdir -p $path"
      return 1
    fi
    if [ "$production" = "1" ]; then
      chown "$uid:$gid" "$path" && chmod 2770 "$path" \
        || { printf '  %s  cannot set ownership on %s\n' "$(paint bad)" "$path"; return 1; }
    else
      chmod 0775 "$path" 2>/dev/null || true
    fi
    printf '  %s  %s %s(created)%s\n' "$(paint ok)" "$path" "$C_DIM" "$C_OFF"
  done
}

run() {
  local dev=0 domain="" storage="" acme="" import=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --dev)         dev=1 ;;
      --domain)      [ $# -ge 2 ] || die "init: --domain needs a value"; domain="$2"; shift ;;
      --storage)     [ $# -ge 2 ] || die "init: --storage needs a value"; storage="$2"; shift ;;
      --acme-email)  [ $# -ge 2 ] || die "init: --acme-email needs a value"; acme="$2"; shift ;;
      --import-env)  [ $# -ge 2 ] || die "init: --import-env needs a value"; import="$2"; shift ;;
      -h|--help)     usage_init; return 0 ;;
      *)             die "init: unknown option '$1'" ;;
    esac
    shift
  done

  # TELOS_DEV is the global flag; --dev here means the same thing.
  [ "${TELOS_DEV:-0}" = "1" ] && dev=1
  [ "$dev" -eq 1 ] && export TELOS_DEV=1

  local mode=production production=1
  if [ "$dev" -eq 1 ]; then mode=development; production=0; fi
  [ "$production" -eq 0 ] || [ "$(id -u)" -eq 0 ] \
    || die "init: a production node must be initialized as root: sudo telos init"

  local template="$REPO_ROOT/.env.example"
  [ -f "$template" ] || die "cannot find $template"
  command -v openssl >/dev/null 2>&1 || die "openssl is required to generate secrets"

  local node; node="$(node_path)" || return 2
  printf '%sInitializing this Telos node%s\n\n' "$C_BOLD" "$C_OFF"
  printf '  node path  %s\n' "$node"

  # --- settings ------------------------------------------------------------
  if [ "$dev" -eq 1 ]; then
    domain="${domain:-localhost}"
    storage="${storage:-$HOME/telos-storage}"
    acme="${acme:-dev@localhost}"
  else
    if [ -z "$import" ]; then
      if [ -z "$domain" ]; then
        [ -t 0 ] || die "init: --domain is required when no terminal is available"
        printf 'Public domain for this node (e.g. library.example.com): '
        read -r domain
        [ -n "$domain" ] || die "a domain is required for a production node (or use --dev)"
      fi
      if [ -z "$acme" ]; then
        [ -t 0 ] || die "init: --acme-email is required when no terminal is available"
        printf "Email for Let's Encrypt certificate notices: "
        read -r acme
        [ -n "$acme" ] || die "an ACME contact email is required for a production node"
      fi
    fi
    if [ -z "$storage" ]; then
      if [ -t 0 ]; then
        printf 'Storage path for media and books [/mnt/storage/shared]: '
        read -r storage
      fi
      storage="${storage:-/mnt/storage/shared}"
    fi
  fi
  case "$storage" in /*) ;; *) die "init: --storage must be an absolute path" ;; esac

  # Service identity: never root. Under sudo, the invoking operator owns media.
  local app_uid app_gid
  if [ "$production" -eq 1 ]; then
    app_uid="${SUDO_UID:-1000}"; app_gid="${SUDO_GID:-1000}"
    [ "$app_uid" -ne 0 ] || app_uid=1000
    [ "$app_gid" -ne 0 ] || app_gid=1000
  else
    app_uid="$(id -u)"; app_gid="$(id -g)"
  fi
  local tz; tz="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
  tz="${tz:-Etc/UTC}"

  local assignments=(
    --set "STORAGE_PATH=$storage"
    --set "APP_UID=$app_uid" --set "APP_GID=$app_gid" --set "TZ=$tz"
    --set "JELLYFIN_ADMIN_TOKEN="
  )
  if [ -z "$import" ]; then
    assignments+=(--set "TELOS_DOMAIN=$domain" --set "ACME_EMAIL=$acme")
  fi
  if [ "$dev" -eq 1 ]; then
    assignments+=(
      --set "TELOS_PUBLIC_ORIGIN=http://localhost:8080"
      --set "TELOS_DEV_ORIGINS=http://localhost:3000,http://127.0.0.1:3000"
    )
    # Rootless runtimes cannot bind :80/:443. Development may use alternate
    # ports; a production node never silently does (see the port preflight).
    local port_start
    port_start="$(sysctl -n net.ipv4.ip_unprivileged_port_start 2>/dev/null || echo 1024)"
    if [ "$(id -u)" -ne 0 ] && [ "$port_start" -gt 80 ] 2>/dev/null; then
      assignments+=(--set "TRAEFIK_HTTP_PORT=8081" --set "TRAEFIK_HTTPS_PORT=8443")
      printf '  %s  development Traefik on 8081/8443 (rootless cannot bind 80/443)\n' "$(paint warn)"
    fi
  else
    [ -n "$import" ] || assignments+=(--set "TELOS_PUBLIC_ORIGIN=https://$domain")
  fi

  local import_args=()
  [ -z "$import" ] || import_args=(--import-env "$import")
  printf '\n%sGenerating configuration%s\n' "$C_BOLD" "$C_OFF"
  local created
  created="$(node_config_py init --mode "$mode" --repo-root "$REPO_ROOT" --template "$template" \
    --legacy-env "$REPO_ROOT/.env" "${import_args[@]}" "${assignments[@]}")" || {
    printf '\n'
    note "Nothing was overwritten. If a previous attempt was interrupted, read the message above."
    return 1
  }
  printf '  %s  wrote %s (mode 0600)\n' "$(paint ok)" "$created"
  printf '  %s  wrote %s/config (cursor + HLS signing keys, mode 0700/0600)\n' "$(paint ok)" "$node"
  printf '  %s  created %s/state\n' "$(paint ok)" "$node"

  # --- storage tree --------------------------------------------------------
  printf '\n%sStorage%s\n' "$C_BOLD" "$C_OFF"
  create_storage_tree "$storage" "$app_uid" "$app_gid" "$production" || return 1

  # --- done ----------------------------------------------------------------
  printf '\n%s%s Node initialized%s\n\n' "$C_OK" "$I_OK" "$C_OFF"
  note "Back up $node: the database volumes are bound to the credentials in node.env"
  note "and sessions/cursors to the signing keys. Neither is ever regenerated."
  printf '\n%sNext:%s\n' "$C_BOLD" "$C_OFF"
  if [ "$dev" -eq 1 ]; then
    printf '    telos start --dev\n'
  else
    printf '    sudo telos configure integrations --setup\n'
    printf '    sudo telos start\n'
  fi
  printf '    telos status\n\n'
  note "Jellyfin and Grimmory report 'degraded' until their tokens and library IDs"
  note "are recorded. A production start is refused until they are."
  return 0
}
