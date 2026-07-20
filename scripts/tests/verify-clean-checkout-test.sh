#!/usr/bin/env bash
# Test harness for scripts/verify-clean-checkout.sh.
#
# Builds a disposable Git fixture containing every required release input,
# proves that removing config/dynamic/routes.yaml makes --inventory-only
# exit 10, then restores the path and proves the fixture passes.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fixture="$tmp/fixture"
mkdir -p "$fixture"

fail() {
	echo "FAIL: $1" >&2
	exit 1
}

# Copy the required inputs plus the full migrations directory into the fixture.
while IFS= read -r p; do
	mkdir -p "$fixture/$(dirname "$p")"
	cp "$repo_root/$p" "$fixture/$p"
done < <(bash "$repo_root/scripts/verify-clean-checkout.sh" --list-required)
mkdir -p "$fixture/backend/db/migrations"
cp "$repo_root"/backend/db/migrations/*.sql "$fixture/backend/db/migrations/"

git -C "$fixture" init -q
git -C "$fixture" config user.email fixture@example.invalid
git -C "$fixture" config user.name "Fixture"
git -C "$fixture" add -A
git -C "$fixture" commit -qm fixture

# Failure case: a missing required route provider must exit 10.
git -C "$fixture" rm -q config/dynamic/routes.yaml
rc=0
(cd "$fixture" && bash scripts/verify-clean-checkout.sh --inventory-only >/dev/null 2>&1) || rc=$?
[ "$rc" -eq 10 ] || fail "expected exit 10 for missing routes.yaml, got $rc"

# Restore the required path; the fixture must now pass.
git -C "$fixture" checkout -q HEAD -- config/dynamic/routes.yaml
rc=0
(cd "$fixture" && bash scripts/verify-clean-checkout.sh --inventory-only >/dev/null) || rc=$?
[ "$rc" -eq 0 ] || fail "expected exit 0 after restoring routes.yaml, got $rc"

# Failure case: a migration gap must exit 10.
git -C "$fixture" rm -q backend/db/migrations/0003_files_tables.sql
rc=0
(cd "$fixture" && bash scripts/verify-clean-checkout.sh --inventory-only >/dev/null 2>&1) || rc=$?
[ "$rc" -eq 10 ] || fail "expected exit 10 for migration gap, got $rc"

echo "PASS: verify-clean-checkout inventory harness"
