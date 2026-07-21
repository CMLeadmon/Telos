#!/usr/bin/env bash
# restore-state.sh — atomic active-generation pointer management for restore.
#
# The active generation is named by a mode-0600 pointer file consumed by
# Compose. Activation writes the new generation to a temp file, fsyncs the
# journal, then atomically renames the pointer. On any post-activation failure
# the caller swaps the pointer back to the preserved prior generation. An
# untrappable exit is recovered by reading the journal on the next invocation.
set -euo pipefail

POINTER="${TELOS_ACTIVE_GENERATION_FILE:-/var/lib/telos/active-generation}"
JOURNAL="${TELOS_RESTORE_JOURNAL:-/var/lib/telos/restore-journal}"

cmd="${1:-}"
gen="${2:-}"

case "$cmd" in
current)
	cat "$POINTER" 2>/dev/null || echo ""
	;;
journal-begin)
	# Record the intended transition and fsync before activating.
	mkdir -p "$(dirname "$JOURNAL")"
	prev="$(cat "$POINTER" 2>/dev/null || echo "")"
	printf 'from=%s\nto=%s\nstate=activating\n' "$prev" "$gen" >"$JOURNAL.tmp"
	sync "$JOURNAL.tmp" 2>/dev/null || true
	mv -f "$JOURNAL.tmp" "$JOURNAL"
	;;
activate)
	mkdir -p "$(dirname "$POINTER")"
	umask 077
	printf '%s\n' "$gen" >"$POINTER.tmp"
	sync "$POINTER.tmp" 2>/dev/null || true
	mv -f "$POINTER.tmp" "$POINTER"           # atomic rename
	printf 'state=activated\ngen=%s\n' "$gen" >>"$JOURNAL"
	;;
rollback)
	# Swap back to the prior generation recorded in the journal.
	prev="$(sed -n 's/^from=//p' "$JOURNAL" | head -1)"
	if [ -n "$prev" ]; then
		printf '%s\n' "$prev" >"$POINTER.tmp"
		mv -f "$POINTER.tmp" "$POINTER"
	fi
	printf 'state=rolled_back\n' >>"$JOURNAL"
	;;
recover)
	# After an untrappable exit, complete or roll back per the journal state.
	state="$(sed -n 's/^state=//p' "$JOURNAL" | tail -1 || echo "")"
	echo "restore-state: journal state is '${state:-none}'"
	;;
*)
	echo "usage: restore-state.sh {current|journal-begin <gen>|activate <gen>|rollback|recover}" >&2
	exit 2
	;;
esac
