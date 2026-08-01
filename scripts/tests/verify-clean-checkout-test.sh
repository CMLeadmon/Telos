#!/usr/bin/env bash
# Test harness for scripts/verify-clean-checkout.sh.
#
# Proves every documented exit code with disposable fixtures:
#   10 — a required release input (config/dynamic/routes.yaml) is missing,
#        and separately a migration gap;
#   20 — a forced build failure (corrupted frontend lockfile);
#   30 — a failed readiness probe (injected via TELOS_VERIFY_FAULT);
#   40 — a forced evidence-artifact integrity failure (injected via
#        TELOS_VERIFY_FAULT).
#
# The 30/40 cases run full mode against a buildable ref of the real
# repository — HEAD by default, or TELOS_VERIFY_SOURCE_REF when HEAD is not
# yet self-contained. Set TELOS_VERIFY_SKIP_FULL=1 to run only the fast
# inventory/build fixtures.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "FAIL: $1" >&2
	exit 1
}

make_fixture() {
	local fixture="$1"
	mkdir -p "$fixture"
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
}

expect_exit() {
	local expected="$1"
	shift
	local rc=0
	"$@" >/dev/null 2>&1 || rc=$?
	[ "$rc" -eq "$expected" ] || fail "expected exit $expected from '$*', got $rc"
}

set_traefik_ports() {
	local fixture="$1"
	local http_port="$2"
	local https_port="$3"
	local updated
	updated="$(mktemp "$fixture/docker-compose.yml.XXXXXX")"
	awk -v http_port="$http_port" -v https_port="$https_port" '
		/^  [a-zA-Z0-9_-]+:/ {
			service=$1
			sub(":", "", service)
		}
		service == "traefik" && /^    ports:/ {
			print
			getline
			print "      - \"" http_port "\""
			getline
			print "      - \"" https_port "\""
			next
		}
		{ print }
	' "$fixture/docker-compose.yml" >"$updated"
	mv "$updated" "$fixture/docker-compose.yml"
}

# --- exact Traefik production-port allowlist -------------------------------
ports_fixture="$tmp/ports-fixture"
make_fixture "$ports_fixture"

expect_exit 0 env -C "$ports_fixture" bash scripts/verify-clean-checkout.sh --inventory-only

set_traefik_ports "$ports_fixture" '80:80' '443:443'
expect_exit 0 env -C "$ports_fixture" bash scripts/verify-clean-checkout.sh --inventory-only

set_traefik_ports "$ports_fixture" '${WRONG_HTTP_PORT:-80}:80' '${TRAEFIK_HTTPS_PORT:-443}:443'
expect_exit 10 env -C "$ports_fixture" bash scripts/verify-clean-checkout.sh --inventory-only

set_traefik_ports "$ports_fixture" '${TRAEFIK_HTTP_PORT:-80}:8080' '${TRAEFIK_HTTPS_PORT:-443}:443'
expect_exit 10 env -C "$ports_fixture" bash scripts/verify-clean-checkout.sh --inventory-only

set_traefik_ports "$ports_fixture" '${TRAEFIK_HTTP_PORT:-8080}:80' '${TRAEFIK_HTTPS_PORT:-443}:443'
expect_exit 10 env -C "$ports_fixture" bash scripts/verify-clean-checkout.sh --inventory-only

set_traefik_ports "$ports_fixture" '8080:80' '8443:443'
expect_exit 10 env -C "$ports_fixture" bash scripts/verify-clean-checkout.sh --inventory-only

sed -i '/^    image: redis:7\.4\.8-alpine$/a\    ports:\n      - "6379:6379"' \
	"$ports_fixture/docker-compose.yml"
expect_exit 10 env -C "$ports_fixture" bash scripts/verify-clean-checkout.sh --inventory-only

# --- exit 10: missing required route provider -------------------------------
fixture="$tmp/fixture"
make_fixture "$fixture"

git -C "$fixture" rm -q config/dynamic/routes.yaml
expect_exit 10 env -C "$fixture" bash scripts/verify-clean-checkout.sh --inventory-only
git -C "$fixture" checkout -q HEAD -- config/dynamic/routes.yaml
expect_exit 0 env -C "$fixture" bash scripts/verify-clean-checkout.sh --inventory-only

# --- exit 10: migration gap --------------------------------------------------
git -C "$fixture" rm -q backend/db/migrations/0003_files_tables.sql
expect_exit 10 env -C "$fixture" bash scripts/verify-clean-checkout.sh --inventory-only
git -C "$fixture" checkout -q HEAD -- backend/db/migrations/0003_files_tables.sql

# --- exit 20: forced build failure -------------------------------------------
echo "this is not a valid lockfile" >"$fixture/frontend/package-lock.json"
git -C "$fixture" commit -qam "corrupt lockfile"
expect_exit 20 env -C "$fixture" bash scripts/verify-clean-checkout.sh \
	--source-ref HEAD --evidence-out "$tmp/fixture-evidence.json"

if [ "${TELOS_VERIFY_SKIP_FULL:-0}" = "1" ]; then
	echo "PASS: verify-clean-checkout harness (fast fixtures only; full-mode 30/40 skipped)"
	exit 0
fi

full_ref="${TELOS_VERIFY_SOURCE_REF:-HEAD}"

# --- exit 30: failed readiness probe ------------------------------------------
expect_exit 30 env -C "$repo_root" \
	TELOS_VERIFY_FAULT=readiness TELOS_VERIFY_PROBE_ATTEMPTS=3 \
	bash scripts/verify-clean-checkout.sh \
	--source-ref "$full_ref" --evidence-out "$tmp/evidence-30.json"

# --- exit 40: forced teardown/artifact-integrity failure ----------------------
expect_exit 40 env -C "$repo_root" TELOS_VERIFY_FAULT=teardown \
	bash scripts/verify-clean-checkout.sh \
	--source-ref "$full_ref" --evidence-out "$tmp/evidence-40.json"

echo "PASS: verify-clean-checkout harness (exit codes 10/20/30/40 proven)"
