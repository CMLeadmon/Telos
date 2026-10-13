# shellcheck shell=bash
# summary: Validate Jellyfin and Grimmory and record their credentials and libraries

usage_configure() {
  cat <<'EOF'
telos configure — guided node configuration

Usage:
  telos configure integrations [--setup]

integrations
  Validates the Jellyfin API key and the Grimmory gateway account over the
  container networks, shows the libraries each service reports, asks which to
  expose, validates again, and records the choices in node.env in one atomic
  write. Credentials are read without echo, travel only over pipes (never
  argv, the environment or a file), and are never printed.

  --setup   Start only Jellyfin, Grimmory and their dependencies with their
            administration UIs published on 127.0.0.1 (ports 8096 and 6060 by
            default), so you can create the accounts and libraries first. The
            command prints an SSH tunnel for remote hosts, and removes those
            containers (volumes are kept) and verifies both ports are closed
            before it returns -- also on failure, Ctrl-C and SIGTERM.
            Refuses to run while the production stack is up.

Without --setup the upstream containers must already be running.

Run it interactively: it needs a terminal for the hidden prompts.
EOF
}

CFG_SETUP_SERVICES=(jellyfin grimmory-db grimmory telos-egress-proxy telos-setup-proxy)
CFG_SETUP_CONTAINERS=(telos-jellyfin telos-grimmory-db telos-grimmory telos-egress-proxy telos-setup-proxy)
CFG_STARTED_SETUP=0
CFG_ECHO_RESTORE=0

cfg_restore_tty() {
  if [ "$CFG_ECHO_RESTORE" -eq 1 ]; then stty echo 2>/dev/null || true; CFG_ECHO_RESTORE=0; fi
}

# cfg_port_open PORT — true when something accepts connections on 127.0.0.1:PORT.
cfg_port_open() {
  (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

# cfg_teardown — remove exactly the containers this invocation started and prove
# the loopback ports are closed. Returns 1, naming the exact command, on failure.
cfg_teardown() {
  [ "$CFG_STARTED_SETUP" -eq 1 ] || return 0
  CFG_STARTED_SETUP=0
  local status=0 runtime
  runtime="$(detect_runtime)"
  # Directly through the runtime, by the exact container names this command
  # started: podman-compose 1.6.0 has no `rm` subcommand, so a compose-level removal
  # silently left the loopback ports open. Volumes are never touched.
  if ! "$runtime" rm -f --ignore -t 10 "${CFG_SETUP_CONTAINERS[@]}" >/dev/null 2>&1; then
    status=1
    warn "could not remove the setup containers; run: podman rm -f ${CFG_SETUP_CONTAINERS[*]}"
  fi
  "$runtime" network rm -f telos-setup >/dev/null 2>&1 || true
  local port
  for port in "$CFG_JELLYFIN_PORT" "$CFG_GRIMMORY_PORT"; do
    if cfg_port_open "$port"; then
      status=1
      warn "setup port 127.0.0.1:$port is STILL OPEN; remove the container publishing it: podman ps --filter publish=$port"
    fi
  done
  return "$status"
}

cfg_cleanup_and_exit() {
  local code="$1"
  cfg_restore_tty
  if ! cfg_teardown; then code=1; fi
  release_config_lock
  trap - EXIT INT TERM HUP
  exit "$code"
}

# cfg_encode_request — read "token\0user\0password\0" on stdin, print the JSON
# request. Python reads an inherited pipe: no argv, no environment, no file.
cfg_encode_request() {
  python3 -c '
import json, sys
token, user, password = sys.stdin.buffer.read().split(b"\0")[:3]
sys.stdout.write(json.dumps({"jellyfinToken": token.decode(), "grimmoryUser": user.decode(), "grimmoryPassword": password.decode()}))
'
}

# cfg_discover TOKEN USER PASSWORD [--set-jellyfin-base-url] — run
# validate-integrations in a throwaway gateway container on the production
# networks; JSON response on stdout. The optional flag is passed only by the
# final validation: it saves Jellyfin's /jellyfin Base URL, after which Jellyfin
# answers nothing until it restarts, so no further discovery may follow.
cfg_discover() {
  local overlay=() flag=()
  [ "$CFG_STARTED_SETUP" -eq 1 ] && overlay=(docker-compose.setup.yml)
  [ -n "${4:-}" ] && flag=("$4")
  printf '%s\0%s\0%s\0' "$1" "$2" "$3" | cfg_encode_request \
    | compose_with ${overlay+"${overlay[@]}"} -- run --rm -T --no-deps telos-core ./telos-core validate-integrations ${flag+"${flag[@]}"}
}

# cfg_choose JSON KEY LABEL — print a numbered list (non-secret) and read a
# comma-separated selection. Echoes the chosen stable IDs, comma-joined.
cfg_choose() {
  local json="$1" key="$2" label="$3" listing selection
  listing="$(KEY="$key" python3 -c '
import json, os, sys
libraries = json.loads(sys.stdin.read())[os.environ["KEY"]]
for index, library in enumerate(libraries, 1):
    print("%d\t%s\t%s" % (index, library["id"], library["name"]))
' <<<"$json")" || return 1
  [ -n "$listing" ] || { warn "$label reported no libraries; create one first"; return 1; }
  {
    printf '\n%s libraries:\n' "$label"
    while IFS=$'\t' read -r index id name; do printf '  %s) %s  (id %s)\n' "$index" "$name" "$id"; done <<<"$listing"
  } >&2
  while true; do
    printf 'Expose which %s libraries? Comma-separated numbers: ' "$label" >&2
    IFS= read -r selection || return 1
    if chosen="$(SELECTION="$selection" python3 -c '
import os, sys
rows = [line.rstrip("\n").split("\t") for line in sys.stdin if line.strip()]
ids = {row[0]: row[1] for row in rows}
chosen = set()
for part in os.environ["SELECTION"].split(","):
    part = part.strip()
    if not part:
        continue
    if part not in ids:
        raise SystemExit(1)
    chosen.add(int(part))
if not chosen:
    raise SystemExit(1)
# Duplicates collapse and the result follows the order the libraries were shown in.
print(",".join(ids[str(index)] for index in sorted(chosen)))
' <<<"$listing")"; then
      printf '%s' "$chosen"
      return 0
    fi
    warn "choose one or more of the listed numbers" >&2
  done
}

# cfg_all_present JSON KEY IDS — every selected ID still exists in the response.
cfg_all_present() {
  KEY="$2" IDS="$3" python3 -c '
import json, os, sys
present = {str(lib["id"]) for lib in json.loads(sys.stdin.read())[os.environ["KEY"]]}
missing = [i for i in os.environ["IDS"].split(",") if i not in present]
raise SystemExit(1 if missing else 0)
' <<<"$1"
}

run() {
  local sub="${1:-}"
  case "$sub" in
    ""|-h|--help|help) usage_configure; return 0 ;;
    integrations) shift ;;
    *) die "configure: unknown subcommand '$sub' (try: telos configure integrations)" ;;
  esac
  local setup=0
  while [ $# -gt 0 ]; do
    case "$1" in
      --setup)   setup=1 ;;
      -h|--help) usage_configure; return 0 ;;
      *)         die "configure integrations: unknown option '$1'" ;;
    esac
    shift
  done
  [ -t 0 ] || die "configure integrations needs a terminal: it prompts for credentials without echo"

  require_node_env
  local env_file mode
  env_file="$(node_env_path)"
  mode="$(node_mode)"
  # Signing files must already be sound; completeness of the integrations is exactly
  # what this command repairs, so only the signing/syntax layer is required here.
  validate_signing_files "$env_file" || die "node.env or its signing files are not valid (run: telos doctor)"

  if ! acquire_config_lock; then
    die "another telos command is changing this node's configuration; wait for it to finish"
  fi
  CFG_JELLYFIN_PORT="${JELLYFIN_SETUP_PORT:-$(env_value "$env_file" JELLYFIN_SETUP_PORT)}"
  CFG_JELLYFIN_PORT="${CFG_JELLYFIN_PORT:-8096}"
  CFG_GRIMMORY_PORT="${GRIMMORY_SETUP_PORT:-$(env_value "$env_file" GRIMMORY_SETUP_PORT)}"
  CFG_GRIMMORY_PORT="${CFG_GRIMMORY_PORT:-6060}"
  # Registered before anything is started: teardown must run on every exit path.
  trap 'cfg_cleanup_and_exit "$?"' EXIT
  trap 'cfg_cleanup_and_exit 130' INT
  trap 'cfg_cleanup_and_exit 143' TERM HUP

  local runtime; runtime="$(detect_runtime)"
  local running; running="$($runtime ps --format '{{.Names}}' 2>/dev/null || true)"

  # The gateway image validates credentials; build it before first use.
  info "Building the gateway validation image..."
  compose_with -- build telos-core >/dev/null || { warn "could not build the telos-core image"; return 1; }

  if [ "$setup" -eq 1 ]; then
    local name
    for name in telos-traefik telos-core "${CFG_SETUP_CONTAINERS[@]}"; do
      if grep -qx "$name" <<<"$running"; then
        die "$name is already running; --setup refuses to touch a running stack. Run: telos stop"
      fi
    done
    if [ -n "$($runtime ps -a --format '{{.Names}}' 2>/dev/null | grep -Fx -f <(printf '%s\n' "${CFG_SETUP_CONTAINERS[@]}") || true)" ]; then
      die "setup containers already exist from an earlier run; remove them first (podman rm -f ${CFG_SETUP_CONTAINERS[*]}) so teardown cannot remove somebody else's service"
    fi
    CFG_STARTED_SETUP=1
    info "Starting Jellyfin and Grimmory on 127.0.0.1 only..."
    compose_with docker-compose.setup.yml -- up -d "${CFG_SETUP_SERVICES[@]}" >/dev/null || { warn "could not start the setup services"; return 1; }
    cat <<EOF

  Jellyfin  http://127.0.0.1:$CFG_JELLYFIN_PORT
  Grimmory  http://127.0.0.1:$CFG_GRIMMORY_PORT

  From another machine, forward them over SSH:
    ssh -L $CFG_JELLYFIN_PORT:127.0.0.1:$CFG_JELLYFIN_PORT -L $CFG_GRIMMORY_PORT:127.0.0.1:$CFG_GRIMMORY_PORT <user>@<this-host>

  In Jellyfin: finish the first-run wizard, add your libraries, then create an
  API key (Dashboard -> API Keys). In Grimmory: create the gateway admin
  account and your libraries. These ports are closed again when this command ends.

EOF
    printf 'Press Enter when both services are set up (Ctrl-C aborts and cleans up): '
    IFS= read -r _ || return 1
  else
    local name
    for name in telos-jellyfin telos-grimmory; do
      grep -qx "$name" <<<"$running" || die "$name is not running; start the upstream services or use --setup"
    done
  fi

  # --- credentials: hidden prompts, memory only ---------------------------------
  local token user password current_user
  current_user="$(env_value "$env_file" GRIMMORY_ADMIN_USER)"
  CFG_ECHO_RESTORE=1
  printf 'Jellyfin API key: '
  IFS= read -r -s token || return 1
  printf '\n'
  printf 'Grimmory gateway account [%s]: ' "${current_user:-telos-gateway}"
  IFS= read -r user || return 1
  user="${user:-${current_user:-telos-gateway}}"
  printf 'Grimmory password (blank keeps the stored one): '
  IFS= read -r -s password || return 1
  printf '\n'
  cfg_restore_tty
  local replace_password=1
  if [ -z "$password" ]; then
    replace_password=0
    password="$(env_value "$env_file" GRIMMORY_ADMIN_PASSWORD)"
  fi
  if [ -z "$token" ] || [ -z "$user" ] || [ -z "$password" ]; then
    unset token user password
    die "the Jellyfin key, Grimmory account and Grimmory password are all required"
  fi

  # --- first discovery, selection, second discovery -----------------------------
  local first second jellyfin_ids grimmory_ids error_file
  error_file="$(mktemp)"
  if ! first="$(cfg_discover "$token" "$user" "$password" 2>"$error_file")"; then
    # The compose provider prints a banner first; show the tail, where the reason is.
    warn "validation failed: $(sed 's/>>>> [^<]*<<<<//g' "$error_file" | tr -d '\n' | tail -c 300)"
    rm -f "$error_file"
    unset token user password
    return 1
  fi
  rm -f "$error_file"
  jellyfin_ids="$(cfg_choose "$first" jellyfin Jellyfin)" || { unset token user password; return 1; }
  grimmory_ids="$(cfg_choose "$first" grimmory Grimmory)" || { unset token user password; return 1; }

  info ""
  info "Re-validating the selection..."
  if ! second="$(cfg_discover "$token" "$user" "$password" --set-jellyfin-base-url 2>/dev/null)" \
     || ! cfg_all_present "$second" jellyfin "$jellyfin_ids" \
     || ! cfg_all_present "$second" grimmory "$grimmory_ids"; then
    unset token user password
    warn "the second validation failed or a selected library disappeared; node.env was not changed"
    return 1
  fi

  # --- one atomic write ----------------------------------------------------------
  local assigns=(
    --assign JELLYFIN_ADMIN_TOKEN=fd:4
    --assign JELLYFIN_LIBRARY_IDS=fd:5
    --assign GRIMMORY_ADMIN_USER=fd:6
    --assign GRIMMORY_LIBRARY_IDS=fd:7
  )
  [ "$replace_password" -eq 1 ] && assigns+=(--assign GRIMMORY_ADMIN_PASSWORD=fd:8)
  if [ "$replace_password" -eq 1 ]; then
    node_config_py set-many "$env_file" "${assigns[@]}" \
      4< <(printf '%s' "$token") 5< <(printf '%s' "$jellyfin_ids") 6< <(printf '%s' "$user") \
      7< <(printf '%s' "$grimmory_ids") 8< <(printf '%s' "$password")
  else
    node_config_py set-many "$env_file" "${assigns[@]}" \
      4< <(printf '%s' "$token") 5< <(printf '%s' "$jellyfin_ids") 6< <(printf '%s' "$user") \
      7< <(printf '%s' "$grimmory_ids")
  fi
  local write_status=$?
  unset token user password first second
  if [ "$write_status" -ne 0 ]; then
    warn "could not update node.env; it is unchanged"
    return 1
  fi
  printf '\n%s%s Recorded Jellyfin libraries %s and Grimmory libraries %s%s\n' "$C_OK" "$I_OK" "$jellyfin_ids" "$grimmory_ids" "$C_OFF"
  note "Credentials were written to $env_file (mode 0600) and never printed."
  printf '\nNext:\n    %stelos start\n' "$([ "$(node_mode)" = production ] && printf 'sudo ')"
  return 0
}
