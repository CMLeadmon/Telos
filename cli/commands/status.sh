# shellcheck shell=bash
# summary: Report boot state for every service

# Classify a runtime status string into: <icon-class>\t<label>
# `name` matters because a one-shot service that exited 0 succeeded.
classify_state() {
  local raw="$1" name="$2"

  if [ -z "$raw" ]; then
    printf 'none\tnot created'
    return
  fi

  case "$raw" in
    Up*"(healthy)"*)   printf 'ok\thealthy' ;;
    Up*"(starting)"*)  printf 'warn\tstarting' ;;
    Up*"(unhealthy)"*) printf 'bad\tunhealthy' ;;
    Up*)               printf 'ok\trunning %s(no healthcheck)%s' "$C_DIM" "$C_OFF" ;;
    [Ee]xited\ \(0\)*)
      if is_oneshot "$name"; then printf 'ok\tcompleted'; else printf 'bad\tstopped'; fi
      ;;
    [Ee]xited*)
      local code
      code="$(printf '%s' "$raw" | sed -n 's/.*[Ee]xited (\([0-9]*\)).*/\1/p')"
      printf 'bad\texited (code %s)' "${code:-?}"
      ;;
    [Cc]reated*) printf 'warn\tcreated, never started' ;;
    [Pp]aused*)  printf 'warn\tpaused' ;;
    *)           printf 'warn\t%s' "$raw" ;;
  esac
}

# Best-effort gateway probe. Unreachable is a useful signal, not an error.
gateway_health() {
  local url="${TELOS_HEALTH_URL:-http://127.0.0.1:8080/api/v1/health}"
  command -v curl >/dev/null 2>&1 || return 1
  curl -fsS --max-time 3 "$url" 2>/dev/null
}

render_health() {
  python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(1)
icon = {"ok": "ok", "warn": "warn", "fail": "bad"}
print(d.get("status", "unknown"))
for c in d.get("checks", []):
    metric = c.get("metric", "")
    extra = c.get("detail", "") or (str(metric) if metric != "" else "")
    print("%s\t%s\t%s" % (icon.get(c.get("status"), "warn"), c.get("name", "?"), extra))
' 2>/dev/null
}

usage_status() {
  cat <<'EOF'
telos status — report boot state for every service

Usage:
  telos status [--json]

Shows two independent views:
  Containers            did each service start, per the container runtime
  Gateway dependencies  what telos-core can actually reach, when it is serving

These answer different questions: a container can be up while the gateway
still cannot authenticate to it.
EOF
}

run() {
  local json_out=0
  case "${1:-}" in
    --json) json_out=1 ;;
    -h|--help) usage_status; return 0 ;;
    "") ;;
    *) die "status: unknown option '$1'" ;;
  esac

  local runtime; runtime="$(detect_runtime)"

  # One call to the runtime; parse locally. `-a` so exited containers show up —
  # those are exactly the interesting failures.
  local ps_out
  ps_out="$($runtime ps -a --format '{{.Names}}|{{.Status}}' 2>/dev/null)"

  local names; names="$(expected_containers)"
  local total=0 healthy=0 problems=0 rows=""

  [ "$json_out" -eq 0 ] && printf '%sTelos stack%s  %s(%s)%s\n\n' \
    "$C_BOLD" "$C_OFF" "$C_DIM" "$runtime" "$C_OFF"

  while IFS= read -r name; do
    [ -n "$name" ] || continue
    total=$((total + 1))

    local raw result cls label
    raw="$(printf '%s\n' "$ps_out" | awk -F'|' -v n="$name" '$1 == n { print $2; exit }')"
    result="$(classify_state "$raw" "$name")"
    cls="${result%%$'\t'*}"
    label="${result#*$'\t'}"

    case "$cls" in
      ok)  healthy=$((healthy + 1)) ;;
      bad) problems=$((problems + 1)) ;;
    esac

    if [ "$json_out" -eq 1 ]; then
      rows="${rows}${rows:+,}{\"service\":\"$name\",\"state\":\"$cls\",\"detail\":\"$(printf '%s' "$label" | sed 's/\x1b\[[0-9;]*m//g')\"}"
    else
      printf '  %s  %-22s %s\n' "$(paint "$cls")" "$name" "$label"
    fi
  done <<< "$names"

  local health_raw="" health_parsed="" overall=""
  health_raw="$(gateway_health)"
  if [ -n "$health_raw" ]; then
    health_parsed="$(printf '%s' "$health_raw" | render_health)"
    overall="$(printf '%s\n' "$health_parsed" | head -1)"
  fi

  if [ "$json_out" -eq 1 ]; then
    printf '{"runtime":"%s","containers":[%s],"gateway":%s}\n' \
      "$runtime" "$rows" "${health_raw:-null}"
    [ "$healthy" -eq "$total" ] || return 1
    return 0
  fi

  if [ -n "$health_parsed" ]; then
    printf '\n%sGateway dependencies%s  %s(%s)%s\n\n' \
      "$C_BOLD" "$C_OFF" "$C_DIM" "$overall" "$C_OFF"
    printf '%s\n' "$health_parsed" | tail -n +2 | while IFS=$'\t' read -r cls cname extra; do
      [ -n "$cname" ] || continue
      printf '  %s  %-22s %s%s%s\n' "$(paint "$cls")" "$cname" "$C_DIM" "$extra" "$C_OFF"
    done
  else
    printf '\n%sGateway dependencies%s  %sunreachable — telos-core is not serving%s\n' \
      "$C_BOLD" "$C_OFF" "$C_DIM" "$C_OFF"
  fi

  printf '\n%s%d/%d services up%s' "$C_BOLD" "$healthy" "$total" "$C_OFF"
  if [ "$problems" -gt 0 ]; then
    printf '%s, %d with problems%s\n' "$C_BAD" "$problems" "$C_OFF"
    note "Inspect a failure with: telos logs <service>"
    return 1
  fi
  printf '\n'
  [ "$healthy" -eq "$total" ] || return 1
  return 0
}
