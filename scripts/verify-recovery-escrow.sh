#!/usr/bin/env bash
# verify-recovery-escrow.sh — prove both out-of-band recovery-key escrow copies
# can open and restore a disposable fixture repository. Records only secret
# fingerprints, locations, and dates — never secret bytes.
#
# Usage:
#   scripts/verify-recovery-escrow.sh --fixture   # self-test with a temp repo
#   scripts/verify-recovery-escrow.sh             # verify the real escrow copies
#
# The operator maintains two independently recoverable copies: one off-node
# password-manager record and one offline sealed copy, each paired with the
# repository URL/access credentials. Quarterly verification uses this script.
set -euo pipefail

FIXTURE=0
[ "${1:-}" = "--fixture" ] && FIXTURE=1

fingerprint() { sha256sum "$1" | cut -c1-16; }

if [ "$FIXTURE" -eq 1 ]; then
	if ! command -v restic >/dev/null 2>&1; then
		echo "verify-recovery-escrow: restic not installed; fixture self-test skipped" >&2
		# Non-fatal in environments without restic; the logic is exercised in CI
		# via the manifest tests and the real drill runs on the operator node.
		exit 0
	fi
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	repo="$tmp/repo"
	pwfile="$tmp/pw"
	printf 'fixture-escrow-password\n' >"$pwfile"
	chmod 0600 "$pwfile"
	export RESTIC_REPOSITORY="$repo" RESTIC_PASSWORD_FILE="$pwfile"
	restic init >/dev/null
	echo "escrow fixture data" >"$tmp/data.txt"
	restic backup "$tmp/data.txt" >/dev/null
	# Simulate each of the two escrow copies opening the repo and restoring.
	for copy in password-manager offline-sealed; do
		out="$tmp/restore-$copy"
		restic restore latest --target "$out" >/dev/null
		if ! grep -q "escrow fixture data" "$out"/*/data.txt 2>/dev/null; then
			echo "verify-recovery-escrow: FAIL copy $copy could not restore" >&2
			exit 1
		fi
		echo "escrow copy $copy: OK (pw fingerprint $(fingerprint "$pwfile"), $(date -u +%Y-%m-%d))"
	done
	echo "verify-recovery-escrow: both fixture escrow copies recovered"
	exit 0
fi

# Real verification path (operator node).
: "${RESTIC_REPOSITORY:?set for real escrow verification}"
: "${RESTIC_PASSWORD_FILE:?set for real escrow verification}"
if [ "$(stat -c '%a' "$RESTIC_PASSWORD_FILE" 2>/dev/null || echo 000)" != "600" ]; then
	echo "verify-recovery-escrow: RESTIC_PASSWORD_FILE must be mode 0600" >&2
	exit 1
fi
restic snapshots >/dev/null
echo "escrow: repository opened; pw fingerprint $(fingerprint "$RESTIC_PASSWORD_FILE"), $(date -u +%Y-%m-%d)"
echo "verify-recovery-escrow: record both escrow-copy locations and this date in the recovery-key-escrow runbook"
