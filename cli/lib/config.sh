# shellcheck shell=bash
# Persistent node configuration: where it lives and whether it is sound.
#
# A node's secrets and signing keys live under TELOS_NODE_PATH, never inside the
# (replaceable) checkout or release generation:
#   production   /var/lib/telos        (root-owned; run as `sudo telos`)
#   development  $REPO_ROOT/.telos
#     node.env   non-sourced KEY=VALUE data, mode 0600
#     config/    cursor-keys.json + hls-key, mounted read-only at /run/telos
#     state/     operator journals, locks, install receipt (root, 0700)
#
# Nothing here ever `source`s node.env: all parsing, validation and rewriting is
# done by cli/lib/node_config.py, which treats the file as data.

NODE_CONFIG_PY="$CLI_DIR/lib/node_config.py"

node_config_py() { python3 -B "$NODE_CONFIG_PY" "$@"; }

# node_path — the resolved absolute node directory, or exit 2 if it is unsafe.
node_path() {
  local resolved
  if [ "${TELOS_DEV:-0}" = "1" ]; then
    resolved="$(node_config_py resolve --repo-root "$REPO_ROOT" --dev)" || die "cannot resolve the node path"
  else
    resolved="$(node_config_py resolve --repo-root "$REPO_ROOT")" || die "cannot resolve the node path"
  fi
  printf '%s' "$resolved"
}

node_env_path()   { printf '%s/node.env' "$(node_path)"; }
config_dir_path() { printf '%s/config' "$(node_path)"; }

# env_value FILE KEY — decoded value, empty output and status 1 when absent.
env_value() { node_config_py get "$1" "$2" 2>/dev/null; }

# node_mode — production unless the global --dev flag is set.
node_mode() { [ "${TELOS_DEV:-0}" = "1" ] && printf 'development' || printf 'production'; }

# _validate FILE MODE ONLY — print one defect per line on stderr; status 0 when none.
_validate() {
  local file="$1" mode="$2" only="$3" args=()
  # Ownership of the signing files can only be verified by root; a non-root
  # caller sees the permission checks but not the uid/gid ones.
  [ "$(id -u)" -eq 0 ] || args+=(--no-owner-check)
  node_config_py validate "$file" --mode "$mode" --repo-root "$REPO_ROOT" --only "$only" ${args+"${args[@]}"}
}

validate_signing_files() {
  local file="$1" mode
  mode="$(env_value "$file" TELOS_ENV)"
  [ "$mode" = "development" ] || mode=production
  _validate "$file" "$mode" signing
}

validate_integrations() { _validate "$1" production integrations; }

# validate_node_config FILE MODE — zero only when every check for MODE passes.
validate_node_config() {
  case "$2" in production|development) ;; *) die "validate_node_config: mode must be production or development" ;; esac
  _validate "$1" "$2" all
}

# require_node_env — the one place that says where a missing configuration is.
require_node_env() {
  local file; file="$(node_env_path)"
  [ -f "$file" ] || die "node.env not found at $file — run: telos init"
}
