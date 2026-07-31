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
  config    .env presence, permissions, unreplaced placeholders, storage tree
  node      container state and gateway reachability

Options:
  --fix   Apply the safe, reversible fixes only — creating missing storage
          directories and tightening .env permissions. It will never install
          system packages, edit credentials, or touch your data; anything
          outside that boundary is printed for you to run yourself.
EOF
}

# --- configuration layer ---------------------------------------------------
check_config() {
  local do_fix="$1"
  printf '\n%sConfiguration%s\n\n' "$C_BOLD" "$C_OFF"

  local env_file="$REPO_ROOT/.env"
  if [ ! -f "$env_file" ]; then
    dx_bad ".env is missing — this node is not initialized" "telos init"
    return
  fi
  dx_ok ".env present"

  # Mode. It holds every credential on the node.
  local mode; mode="$(stat -c '%a' "$env_file" 2>/dev/null || echo '???')"
  if [ "$mode" = "600" ] || [ "$mode" = "400" ]; then
    dx_ok ".env permissions ($mode)"
  elif [ "$do_fix" -eq 1 ]; then
    chmod 600 "$env_file" && dx_ok ".env permissions tightened to 600 (was $mode)"
  else
    dx_bad ".env is mode $mode — it holds every credential on this node" \
           "chmod 600 $env_file    (or: telos doctor --fix)"
  fi

  # Unreplaced placeholders are the single most common broken-node cause.
  local placeholders
  placeholders="$(grep -nE '=(change-me|generate-me)' "$env_file" 2>/dev/null | cut -d: -f2 | cut -d= -f1 | tr '\n' ' ')"
  if [ -n "$placeholders" ]; then
    dx_bad "unreplaced placeholder values: $placeholders" \
           "telos init --force   (regenerates secrets; only safe on a node with no data yet)"
  else
    dx_ok "no placeholder credentials"
  fi

  # Storage tree.
  local storage
  storage="$(grep -E '^STORAGE_PATH=' "$env_file" | head -1 | cut -d= -f2-)"
  if [ -z "$storage" ]; then
    dx_bad "STORAGE_PATH is not set in .env" "telos init --force"
  else
    local d missing=0
    for d in "" /media /books /bookdrop; do
      local path="${storage}${d}"
      if [ ! -d "$path" ]; then
        if [ "$do_fix" -eq 1 ] && mkdir -p "$path" 2>/dev/null; then
          dx_ok "created missing $path"
        else
          missing=1
          dx_bad "missing storage directory: $path" "mkdir -p $path    (or: telos doctor --fix)"
        fi
      fi
    done
    [ "$missing" -eq 0 ] && dx_ok "storage tree under $storage"
    if [ -d "$storage" ] && [ ! -w "$storage" ]; then
      dx_bad "$storage is not writable by $(id -un)" "sudo chown -R $(id -u):$(id -g) $storage"
    fi
  fi

  # A production node on loopback ports is almost certainly a mistake.
  local telos_env http_port
  telos_env="$(grep -E '^TELOS_ENV=' "$env_file" | head -1 | cut -d= -f2-)"
  http_port="$(grep -E '^TRAEFIK_HTTP_PORT=' "$env_file" | head -1 | cut -d= -f2-)"
  if [ "$telos_env" = "production" ] && [ -n "$http_port" ] && [ "$http_port" != "80" ]; then
    dx_warn "production node with TRAEFIK_HTTP_PORT=$http_port — Let's Encrypt HTTP-01 needs public :80"
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
