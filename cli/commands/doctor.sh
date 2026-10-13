# shellcheck shell=bash
# summary: Diagnose host, config, and node problems

# shellcheck source=../lib/preflight.sh
. "$CLI_DIR/lib/preflight.sh"

DOCTOR_FIXES=()
DOCTOR_PROBLEMS=0

dx_ok()   { printf '  %s  %s\n' "$(paint ok)" "$1"; }
dx_warn() { printf '  %s  %s\n' "$(paint warn)" "$1"; }
dx_bad()  {
  printf '  %s  %s\n' "$(paint bad)" "$1"
  DOCTOR_PROBLEMS=$((DOCTOR_PROBLEMS + 1))
  [ -n "${2:-}" ] && DOCTOR_FIXES+=("$2")
  return 0
}

usage_doctor() {
  cat <<'EOF'
telos doctor — diagnose host, configuration, and node problems

Usage:
  telos doctor [--fix]

Checks three layers:
  host      container runtime, compose driver, kernel and storage support
  config    node.env presence/mode/syntax, signing files, upstream
            integrations, storage tree
  node      container state and gateway reachability

Options:
  --fix   Apply the safe, reversible fixes only — tightening permissions on
          node.env and the signing files and creating missing, empty storage
          directories. It never generates or edits credentials or keys, never
          installs packages and never touches your data; everything else is
          printed as one exact command for you to run yourself.
EOF
}

# dx_defects LABEL REPAIR_FALLBACK — read defect lines on stdin; each becomes one
# problem with a repair taken from the message itself when it carries one.
dx_defects() {
  local label="$1" fallback="$2" line repair
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    repair="$fallback"
    case "$line" in
      *"(chmod "*|*"(chown "*) repair="${line##*(}"; repair="${repair%)}" ;;
      *"never regenerate"*|*"never regenerated"*) repair="restore the file from your node backup (keys are never regenerated)" ;;
    esac
    dx_bad "$label: $line" "$repair"
  done
}

# --- configuration layer ---------------------------------------------------
check_config() {
  local do_fix="$1"
  printf '\n%sConfiguration%s\n\n' "$C_BOLD" "$C_OFF"

  local node env_file config mode_name
  node="$(node_path)" || return
  env_file="$node/node.env"
  config="$node/config"
  mode_name="$(node_mode)"

  if [ -e "$REPO_ROOT/.env" ] && [ ! -e "$env_file" ]; then
    dx_bad "a checkout .env exists but this node has no node.env" \
           "telos init --import-env $REPO_ROOT/.env    (copies your credentials unchanged; never regenerate them)"
    return
  fi
  if [ ! -f "$env_file" ]; then
    dx_bad "node.env is missing at $env_file — this node is not initialized" "telos init"
    return
  fi
  dx_ok "node.env present ($env_file)"

  # Mode and syntax. It holds every credential on the node.
  local mode; mode="$(stat -c '%a' "$env_file" 2>/dev/null || echo '???')"
  if [ "$mode" = "600" ] || [ "$mode" = "400" ]; then
    dx_ok "node.env permissions ($mode)"
  elif [ "$do_fix" -eq 1 ]; then
    chmod 600 "$env_file" && dx_ok "node.env permissions tightened to 600 (was $mode)"
  else
    dx_bad "node.env is mode $mode — it holds every credential on this node" \
           "chmod 600 $env_file    (or: telos doctor --fix)"
  fi
  local parse_err; parse_err="$(mktemp)"
  if ! node_config_py parse "$env_file" 2>"$parse_err"; then
    dx_bad "node.env does not parse: $(cat "$parse_err")" "edit $env_file by hand (values are single-quoted or plain; no \$ or backticks)"
    rm -f "$parse_err"
    return
  fi
  rm -f "$parse_err"
  dx_ok "node.env parses"

  # Signing directory and files, reported one by one.
  if [ "$do_fix" -eq 1 ] && [ -d "$config" ]; then
    local fixed=0 path
    if [ "$(stat -c '%a' "$config")" != "700" ]; then chmod 700 "$config" && fixed=1; fi
    for path in "$config/cursor-keys.json" "$config/hls-key"; do
      [ -f "$path" ] && [ ! -L "$path" ] && [ "$(stat -c '%a' "$path")" != "600" ] && chmod 600 "$path" && fixed=1
    done
    [ "$fixed" -eq 0 ] || dx_ok "signing directory and file modes tightened (contents untouched)"
  fi
  local signing_out signing_status
  signing_out="$(validate_signing_files "$env_file" 2>&1)"; signing_status=$?
  if [ "$signing_status" -eq 0 ]; then
    dx_ok "signing files: directory 0700, two distinct keys, readable"
  else
    printf '%s\n' "$signing_out" | dx_defects "signing" "inspect $config; keys are never regenerated on an existing node"
  fi
  if [ "$(id -u)" -ne 0 ] && [ "$mode_name" = "production" ]; then
    dx_warn "signing-file ownership is only verifiable as root; re-run with sudo"
  fi

  # Upstream integrations (production completeness).
  if [ "$mode_name" = "production" ]; then
    local integration_out integration_status
    integration_out="$(validate_integrations "$env_file" 2>&1)"; integration_status=$?
    if [ "$integration_status" -eq 0 ]; then
      dx_ok "credentials, upstream integrations and library IDs are complete"
    else
      printf '%s\n' "$integration_out" | dx_defects "integrations" "telos configure integrations --setup"
    fi
  else
    note "      development node: upstream credentials may be blank (the gateway reports them degraded)"
  fi

  # Storage tree.
  local storage
  storage="$(env_value "$env_file" STORAGE_PATH)"
  if [ -z "$storage" ]; then
    dx_bad "STORAGE_PATH is not set in node.env" "set STORAGE_PATH to an absolute host path in $env_file"
  else
    local d missing=0
    for d in "" /media /books /bookdrop; do
      local path="${storage}${d}"
      if [ ! -d "$path" ]; then
        if [ "$do_fix" -eq 1 ] && mkdir -p "$path" 2>/dev/null; then
          dx_ok "created missing $path (empty)"
        else
          missing=1
          dx_bad "missing storage directory: $path" "mkdir -p $path    (or: telos doctor --fix)"
        fi
      fi
    done
    [ "$missing" -eq 0 ] && dx_ok "storage tree under $storage"
    if [ -d "$storage" ] && [ "$mode_name" = "development" ] && [ ! -w "$storage" ]; then
      dx_bad "$storage is not writable by $(id -un)" "chmod u+w $storage"
    fi
  fi

  # The compose model must mount exactly this configuration directory.
  local mounted
  mounted="$(env_value "$env_file" TELOS_CONFIG_PATH)"
  if [ -n "$mounted" ] && [ "$mounted" != "$config" ]; then
    dx_bad "TELOS_CONFIG_PATH ($mounted) is not this node's config directory ($config)" \
           "set TELOS_CONFIG_PATH=$config in $env_file"
  else
    dx_ok "Compose mounts $config read-only at /run/telos"
  fi

  # A production node on alternate ports cannot obtain certificates.
  local telos_env http_port
  telos_env="$(env_value "$env_file" TELOS_ENV)"
  http_port="$(env_value "$env_file" TRAEFIK_HTTP_PORT)"
  if [ "$telos_env" = "production" ] && [ -n "$http_port" ] && [ "$http_port" != "80" ]; then
    dx_bad "production node with TRAEFIK_HTTP_PORT=$http_port — Let's Encrypt HTTP-01 needs public :80" \
           "set TRAEFIK_HTTP_PORT=80 and TRAEFIK_HTTPS_PORT=443 in $env_file and free those ports"
  fi
}

# --- node layer ------------------------------------------------------------
check_node() {
  printf '\n%sNode%s\n\n' "$C_BOLD" "$C_OFF"

  local runtime; runtime="$(detect_runtime)"
  local ps_out
  ps_out="$($runtime ps -a --format '{{.Names}}|{{.Status}}' 2>/dev/null)"

  local total=0 up=0 down=0 name raw
  while IFS= read -r name; do
    [ -n "$name" ] || continue
    total=$((total + 1))
    raw="$(printf '%s\n' "$ps_out" | awk -F'|' -v n="$name" '$1 == n { print $2; exit }')"
    case "$raw" in
      Up*)              up=$((up + 1)) ;;
      [Ee]xited\ \(0\)*) is_oneshot "$name" && up=$((up + 1)) || down=$((down + 1)) ;;
      "")               down=$((down + 1)) ;;
      *)                down=$((down + 1)) ;;
    esac
  done <<< "$(expected_containers)"

  if [ "$up" -eq 0 ]; then
    dx_warn "stack is not running ($total services defined)"
    note "      Start it with: telos start"
  elif [ "$down" -gt 0 ]; then
    dx_bad "$down of $total services are not up" "telos status    # per-service detail"
  else
    dx_ok "all $total services up"
  fi

  # Gateway reachability is independent of container state — a container can
  # be up while the gateway cannot serve.
  local url="${TELOS_HEALTH_URL:-http://127.0.0.1:8080/api/v1/health}"
  if command -v curl >/dev/null 2>&1; then
    if curl -fsS --max-time 3 "$url" >/dev/null 2>&1; then
      dx_ok "gateway responding at $url"
    elif [ "$up" -gt 0 ]; then
      dx_warn "gateway not responding at $url"
      note "      Expected if you started without --dev (no host port binding)."
    fi
  fi
}

run() {
  local do_fix=0
  case "${1:-}" in
    --fix)     do_fix=1 ;;
    -h|--help) usage_doctor; return 0 ;;
    "") ;;
    *) die "doctor: unknown option '$1'" ;;
  esac

  printf '%sTelos doctor%s\n\n' "$C_BOLD" "$C_OFF"

  # Host layer, reusing the installer's checks verbatim.
  run_preflight || true

  check_config "$do_fix"
  check_node

  # --- summary -------------------------------------------------------------
  local total_problems=$((DOCTOR_PROBLEMS + PREFLIGHT_FAILURES))
  printf '\n'
  if [ "$total_problems" -eq 0 ]; then
    printf '%s%s No problems found%s\n' "$C_OK" "$I_OK" "$C_OFF"
    return 0
  fi

  printf '%s%d problem(s) found%s\n' "$C_BAD" "$total_problems" "$C_OFF"
  if [ ${#DOCTOR_FIXES[@]} -gt 0 ] || [ ${#PREFLIGHT_HINTS[@]} -gt 0 ]; then
    printf '\n%sSuggested fixes:%s\n\n' "$C_BOLD" "$C_OFF"
    local f
    for f in ${PREFLIGHT_HINTS+"${PREFLIGHT_HINTS[@]}"}; do printf '    %s\n' "$f"; done
    for f in ${DOCTOR_FIXES+"${DOCTOR_FIXES[@]}"}; do printf '    %s\n' "$f"; done
    printf '\n'
  fi
  return 1
}
