#!/usr/bin/env bash
# uninstall.sh — remove what install.sh added, and nothing else.
#
# Removes the PATH symlink and, with --purge, the application tree.
#
# Your storage directory and your container volumes are NEVER touched here:
# those are node data, not installed files. Removing them is a separate,
# explicit act (`telos stop --volumes`, and deleting STORAGE_PATH by hand).
#
# .env is different, and this used to claim otherwise. It lives INSIDE the
# application tree, so --purge does delete it along with everything else. That
# step always asks first, and it asks even under --yes, because losing a node's
# credentials desynchronizes them from the existing Postgres and MariaDB
# volumes and there is no way back.
set -uo pipefail

SOURCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$SOURCE_DIR"
# shellcheck source=cli/lib/common.sh
. "$SOURCE_DIR/cli/lib/common.sh"

LINK_CANDIDATES=("/usr/local/bin/telos" "$HOME/.local/bin/telos" "/usr/bin/telos")
PURGE=0
ASSUME_YES=0

usage() {
  cat <<'EOF'
uninstall.sh — remove the telos CLI

Usage:
  ./uninstall.sh [--purge] [--yes]

Options:
  --purge   Also delete the installed application directory
  --yes     Do not prompt

Never removed by this script:
  Your STORAGE_PATH data and container volumes. Those are node data.
  To destroy the database and volumes:  telos stop --volumes

Note on .env:
  It lives inside the application directory, so --purge deletes it too. That
  one step always prompts, even with --yes, and refuses to proceed when there
  is no terminal to prompt on.
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --purge)  PURGE=1 ;;
    --yes|-y) ASSUME_YES=1 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option '$1'" ;;
  esac
  shift
done

printf '%sTelos uninstaller%s\n\n' "$C_BOLD" "$C_OFF"

# Find links that actually point into a Telos tree; leave unrelated files alone.
FOUND=()
for link in "${LINK_CANDIDATES[@]}"; do
  [ -L "$link" ] || continue
  target="$(readlink -f "$link" 2>/dev/null)"
  case "$target" in
    */telos) FOUND+=("$link") ;;
  esac
done

if [ ${#FOUND[@]} -eq 0 ]; then
  info "No telos symlink found in ${LINK_CANDIDATES[*]}"
else
  printf 'Will remove:\n'
  for link in "${FOUND[@]}"; do printf '  %s -> %s\n' "$link" "$(readlink "$link")"; done
fi

if [ "$PURGE" -eq 1 ]; then
  printf '  %s%s (entire directory)%s\n' "$C_BAD" "$SOURCE_DIR" "$C_OFF"
fi
printf '\n'

if [ "$ASSUME_YES" -eq 0 ]; then
  printf 'Proceed? [y/N] '
  read -r reply
  case "$reply" in [yY]|[yY][eE][sS]) ;; *) info "Aborted."; exit 1 ;; esac
fi

for link in "${FOUND[@]}"; do
  if [ -w "$(dirname "$link")" ]; then rm -f "$link"; else sudo rm -f "$link"; fi
  info "removed $link"
done

if [ "$PURGE" -eq 1 ]; then
  if [ -f "$SOURCE_DIR/.env" ]; then
    warn "$SOURCE_DIR/.env exists — it holds this node's credentials."
    printf 'Purge deletes it. Copy it somewhere safe first if you want to keep it.\n'
    # Deliberately NOT covered by --yes. These credentials are paired with the
    # existing Postgres and MariaDB volumes; regenerating them later leaves a
    # node that cannot read its own data.
    #
    # Without a terminal, `read` returns immediately on EOF and the empty reply
    # used to fall through to "Kept", exiting 0 — so a scripted
    # `--purge --yes` reported success while doing nothing. Fail loudly instead.
    if [ ! -t 0 ]; then
      die "refusing to delete $SOURCE_DIR/.env with no terminal to confirm on — re-run interactively, or move .env aside first"
    fi
    printf 'Delete anyway? [y/N] '
    read -r reply
    case "$reply" in [yY]|[yY][eE][sS]) ;; *) info "Kept $SOURCE_DIR."; exit 0 ;; esac
  fi
  cd / || exit 1
  rm -rf "$SOURCE_DIR" && info "removed $SOURCE_DIR"
fi

printf '\n%s%s telos uninstalled%s\n' "$C_OK" "$I_OK" "$C_OFF"
note "Container volumes were not touched. Remove them with: podman volume ls"
