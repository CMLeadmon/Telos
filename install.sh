#!/usr/bin/env bash
# install.sh — put the telos CLI on this machine.
#
# This is the ONLY entry point that exists outside the telos CLI, because you
# cannot run `telos install` before telos exists. Everything after this is a
# telos subcommand.
#
# Scope is deliberately narrow. It installs the CLI; it does NOT set up a node.
# Generating secrets, writing .env, and creating the storage tree are `telos
# init`, which needs no privileges and is safely re-runnable. Keeping the two
# apart means the sudo step happens once and never again.
#
#   ./install.sh                 install to ~/.local/share/telos, link into /usr/local/bin
#   ./install.sh --dir /srv/telos
#   ./install.sh --link-only     run in place from this directory (development)
#   ./install.sh --user          link into ~/.local/bin instead (no sudo)
#   ./install.sh --check         run prerequisite checks only, change nothing
set -uo pipefail

SOURCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$SOURCE_DIR"

# shellcheck source=cli/lib/common.sh
. "$SOURCE_DIR/cli/lib/common.sh"
# shellcheck source=cli/lib/preflight.sh
. "$SOURCE_DIR/cli/lib/preflight.sh"

DEFAULT_DIR="$HOME/.local/share/telos"
[ "$(id -u)" -eq 0 ] && DEFAULT_DIR="/opt/telos"

INSTALL_DIR="$DEFAULT_DIR"
LINK_DIR="/usr/local/bin"
USER_MODE=0
CHECK_ONLY=0
ASSUME_YES=0
LINK_ONLY=0

usage() {
  cat <<EOF
install.sh — install the telos CLI

Usage:
  ./install.sh [options]

Options:
  --dir DIR     Install the application tree here (default: $DEFAULT_DIR)
  --link-only   Do not copy anything; make 'telos' global from THIS directory.
                For a git checkout you are developing in — edits take effect
                immediately, with no reinstall step.
  --user        Link into ~/.local/bin instead of /usr/local/bin (no sudo)
  --check       Run prerequisite checks only; change nothing
  --yes         Do not prompt for confirmation
  -h, --help    Show this help

What it does:
  1. Verifies host prerequisites, printing the exact fix command for your
     distribution if anything is missing. It never installs system packages
     for you.
  2. Copies the application tree to a stable directory, so the download you
     extracted stays disposable. (Skipped with --link-only.)
  3. Symlinks 'telos' into $LINK_DIR, which is on the PATH of every user on
     this machine — so 'telos' works from any directory.

It does not create .env, generate secrets, or touch your data. That is
'telos init', which you run next.
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --dir)   [ $# -ge 2 ] || die "--dir needs a path"; INSTALL_DIR="$2"; shift ;;
    --link-only) LINK_ONLY=1 ;;
    --user)  USER_MODE=1; LINK_DIR="$HOME/.local/bin" ;;
    --check) CHECK_ONLY=1 ;;
    --yes|-y) ASSUME_YES=1 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option '$1' (try --help)" ;;
  esac
  shift
done

printf '%sTelos installer%s\n\n' "$C_BOLD" "$C_OFF"

# --- 1. prerequisites ------------------------------------------------------
if ! run_preflight; then
  exit 1
fi

if [ "$CHECK_ONLY" -eq 1 ]; then
  info "All prerequisites satisfied. Re-run without --check to install."
  exit 0
fi

# --- 2. sanity on the source tree -----------------------------------------
for required in telos cli/lib/common.sh docker-compose.yml .env.example; do
  [ -e "$SOURCE_DIR/$required" ] \
    || die "this does not look like a Telos tree (missing $required)"
done

# Installing a tree onto itself is what --link-only is for; point at it rather
# than failing on something the user plainly meant.
if [ "$LINK_ONLY" -eq 0 ] \
   && [ "$(cd "$INSTALL_DIR" 2>/dev/null && pwd)" = "$SOURCE_DIR" ]; then
  die "source and install directory are the same ($SOURCE_DIR) — use --link-only"
fi

[ "$LINK_ONLY" -eq 1 ] && INSTALL_DIR="$SOURCE_DIR"

# --- 3. confirm ------------------------------------------------------------
UPGRADE=0
[ -d "$INSTALL_DIR" ] && [ "$LINK_ONLY" -eq 0 ] && UPGRADE=1

printf '%sPlan%s\n' "$C_BOLD" "$C_OFF"
if [ "$LINK_ONLY" -eq 1 ]; then
  printf '  run from     %s %s(in place — nothing copied)%s\n' \
    "$SOURCE_DIR" "$C_DIM" "$C_OFF"
elif [ "$UPGRADE" -eq 1 ]; then
  printf '  source       %s\n' "$SOURCE_DIR"
  printf '  install to   %s %s(exists — app files replaced, .env and data kept)%s\n' \
    "$INSTALL_DIR" "$C_WARN" "$C_OFF"
else
  printf '  source       %s\n' "$SOURCE_DIR"
  printf '  install to   %s\n' "$INSTALL_DIR"
fi
printf '  link         %s/telos %s(global — on every user'\''s PATH)%s\n' \
  "$LINK_DIR" "$C_DIM" "$C_OFF"
[ "$USER_MODE" -eq 0 ] && printf '  %ssudo required for the link step%s\n' "$C_DIM" "$C_OFF"
printf '\n'

if [ "$ASSUME_YES" -eq 0 ]; then
  printf 'Proceed? [y/N] '
  read -r reply
  case "$reply" in [yY]|[yY][eE][sS]) ;; *) info "Aborted."; exit 1 ;; esac
  printf '\n'
fi

# --- 4. copy the tree ------------------------------------------------------
if [ "$LINK_ONLY" -eq 1 ]; then
  info "Link-only: running from $SOURCE_DIR, nothing copied."
else
info "Installing application tree..."
mkdir -p "$INSTALL_DIR" || die "cannot create $INSTALL_DIR"

# Exclusions: build output and VCS metadata are regenerated or irrelevant, and
# .env must never be copied from one node to another — that would clone another
# node's credentials. Note the patterns are deliberately specific: a blanket
# './.env.*' would also drop .env.example, which `telos init` reads.
tar -C "$SOURCE_DIR" \
    --exclude='./.git' \
    --exclude='./.env' \
    --exclude='./.env.local' \
    --exclude='./.env.bak.*' \
    --exclude='./node_modules' \
    --exclude='./frontend/node_modules' \
    --exclude='./frontend/out' \
    --exclude='./backend/out' \
    --exclude='./frontend/.next' \
    -cf - . 2>/dev/null | tar -C "$INSTALL_DIR" -xf - \
  || die "failed to copy the application tree to $INSTALL_DIR"

fi

chmod +x "$INSTALL_DIR/telos" "$INSTALL_DIR/install.sh" 2>/dev/null
[ -f "$INSTALL_DIR/uninstall.sh" ] && chmod +x "$INSTALL_DIR/uninstall.sh"

# --- 5. link onto PATH -----------------------------------------------------
info "Linking telos into $LINK_DIR..."
LINK_PATH="$LINK_DIR/telos"

link_cmd() {
  if [ "$USER_MODE" -eq 1 ] || [ -w "$LINK_DIR" ] || [ "$(id -u)" -eq 0 ]; then
    mkdir -p "$LINK_DIR" && ln -sfn "$INSTALL_DIR/telos" "$LINK_PATH"
  else
    command -v sudo >/dev/null 2>&1 || return 1
    # Say why the password prompt is about to appear; an unexplained sudo
    # prompt from an installer is exactly what users should be wary of.
    note "  $LINK_DIR needs root to write; sudo will prompt for the symlink only."
    sudo mkdir -p "$LINK_DIR" && sudo ln -sfn "$INSTALL_DIR/telos" "$LINK_PATH"
  fi
}

if ! link_cmd; then
  warn "could not create $LINK_PATH"
  printf '\nThe application is installed at %s but is not on your PATH.\n' "$INSTALL_DIR"
  printf 'Link it yourself with:\n\n    sudo ln -sfn %s/telos %s\n\n' "$INSTALL_DIR" "$LINK_PATH"
  exit 1
fi

# --- 6. verify -------------------------------------------------------------
if ! "$LINK_PATH" --help >/dev/null 2>&1; then
  die "installed telos at $LINK_PATH but it does not run"
fi

printf '\n%s%s telos installed%s\n\n' "$C_OK" "$I_OK" "$C_OFF"
printf '  application  %s\n' "$INSTALL_DIR"
printf '  command      %s\n\n' "$LINK_PATH"

# PATH sanity — a link nobody can reach is not an install.
case ":$PATH:" in
  *":$LINK_DIR:"*) ;;
  *)
    warn "$LINK_DIR is not on your PATH"
    printf 'Add it by appending this to ~/.bashrc, then open a new terminal:\n\n'
    printf '    export PATH="%s:$PATH"\n\n' "$LINK_DIR"
    ;;
esac

printf '%sNext:%s\n' "$C_BOLD" "$C_OFF"
if [ -f "$INSTALL_DIR/.env" ]; then
  printf '    telos doctor   %sthis node is already initialized — check it%s\n' "$C_DIM" "$C_OFF"
  printf '    telos start    %sbring the stack up%s\n\n' "$C_DIM" "$C_OFF"
else
  printf '    telos init     %sgenerate .env and create the storage tree%s\n' "$C_DIM" "$C_OFF"
  printf '    telos start    %sbring the stack up%s\n' "$C_DIM" "$C_OFF"
  printf '    telos status   %scheck what came up%s\n\n' "$C_DIM" "$C_OFF"
fi

if [ "$LINK_ONLY" -eq 1 ]; then
  note "Link-only: telos runs straight from $INSTALL_DIR, so edits there take"
  note "effect immediately. Moving or deleting that directory breaks the command."
else
  note "The directory you extracted can now be deleted."
fi
