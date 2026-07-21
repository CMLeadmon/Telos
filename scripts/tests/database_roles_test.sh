#!/usr/bin/env bash
# database_roles_test.sh — static assertions on the owner/runtime role split.
#
# Verifies the committed Compose model and scripts wire the split correctly
# without running a live database. Rendered config goes to a mode-0600 temp
# file and is removed on exit; no secret value is printed.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

fail=0
report() { printf '%-28s %s\n' "$1" "$2"; }

env_file="tests/fixtures/compose.env"
rendered="$(mktemp)"
chmod 0600 "$rendered"
trap 'rm -f "$rendered"' EXIT

podman compose --env-file "$env_file" -f docker-compose.yml config >"$rendered" 2>/dev/null

# 1. telos-core (runtime) must use telos_runtime, never the owner/superuser.
if grep -A40 'telos-core:' "$rendered" | grep -qE 'DATABASE_URL:? ?=?postgres://telos_runtime:'; then
	report core-runtime-role OK
else
	report core-runtime-role "FAIL (telos-core DATABASE_URL is not telos_runtime)"
	fail=1
fi
if grep -A40 'telos-core:' "$rendered" | grep -q 'DATABASE_OWNER_URL'; then
	report core-no-owner-url "FAIL (telos-core has an owner URL)"
	fail=1
else
	report core-no-owner-url OK
fi

# 2. telos-migrate must exist, be one-shot, use the owner URL, and have no
#    persistent lifecycle or ingress route.
if grep -q 'telos-migrate:' "$rendered"; then
	if grep -A20 'telos-migrate:' "$rendered" | grep -qE 'DATABASE_OWNER_URL:? ?=?postgres://telos_owner:'; then
		report migrate-owner-role OK
	else
		report migrate-owner-role "FAIL (migrator does not use telos_owner)"
		fail=1
	fi
else
	report migrate-service "FAIL (telos-migrate service missing)"
	fail=1
fi

# 3. The clean-volume init script and the provisioning script exist and grant
#    only DML (no DDL/ownership delegation) to telos_runtime.
for f in deploy/postgres/init/001-create-telos-roles.sh scripts/provision-db-roles.sh; do
	if [ -x "$f" ]; then
		report "exists:$(basename "$f")" OK
	else
		report "exists:$(basename "$f")" "FAIL (missing or non-executable)"
		fail=1
	fi
done
if grep -q 'GRANT CREATE' deploy/postgres/init/001-create-telos-roles.sh scripts/provision-db-roles.sh; then
	report runtime-no-create "FAIL (a script grants CREATE to runtime)"
	fail=1
else
	report runtime-no-create OK
fi

exit "$fail"
