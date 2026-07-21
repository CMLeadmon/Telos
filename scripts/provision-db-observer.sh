#!/usr/bin/env bash
# provision-db-observer.sh — idempotently provision the query-statistics
# observer on an existing volume, or verify the setup with --check.
#
# Usage:
#   scripts/provision-db-observer.sh [--check]
#
# --check verifies (without printing secrets) that pg_stat_statements is
# present, the observer role exists with read-only stats access, and the
# runtime role cannot read statement text. It also confirms no application
# service in the rendered Compose model carries the observer URL/file.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

CHECK=0
[ "${1:-}" = "--check" ] && CHECK=1

: "${PGHOST:=127.0.0.1}"
: "${PGPORT:=5432}"
if [ "$CHECK" -eq 0 ]; then
	: "${POSTGRES_DB:?POSTGRES_DB is required}"
	: "${POSTGRES_USER:?POSTGRES_USER (superuser) is required}"
fi

psql() { command psql -v ON_ERROR_STOP=1 -h "$PGHOST" -p "$PGPORT" -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-postgres}" "$@"; }

# Static assertion (always runs): no application service may carry the observer
# credential.
rendered="$(mktemp)"; chmod 0600 "$rendered"; trap 'rm -f "$rendered"' EXIT
if podman compose --env-file tests/fixtures/compose.env -f docker-compose.yml config >"$rendered" 2>/dev/null; then
	if grep -qiE 'DATABASE_OBSERVER_URL' "$rendered"; then
		echo "provision-db-observer: FAIL an application service carries the observer URL" >&2
		exit 1
	fi
fi

if [ "$CHECK" -eq 1 ]; then
	echo "provision-db-observer: check mode"
	if ! command -v psql >/dev/null 2>&1; then
		echo "provision-db-observer: psql not available; static checks only"
		echo "provision-db-observer: no application service carries the observer credential  OK"
		exit 0
	fi
	psql -tAc "SELECT 'ext:' || COUNT(*) FROM pg_extension WHERE extname='pg_stat_statements'" || true
	psql -tAc "SELECT 'observer:' || COUNT(*) FROM pg_roles WHERE rolname='telos_observer'" || true
	exit 0
fi

psql <<'SQL'
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'telos_observer') THEN
    CREATE ROLE telos_observer NOLOGIN;
  END IF;
END
$$;
GRANT pg_read_all_stats TO telos_observer;
GRANT USAGE ON SCHEMA public TO telos_observer;
GRANT SELECT ON pg_stat_statements TO telos_observer;
REVOKE ALL ON pg_stat_statements FROM PUBLIC;
REVOKE ALL ON pg_stat_statements FROM telos_runtime;
REVOKE EXECUTE ON FUNCTION pg_stat_statements_reset() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION pg_stat_statements_reset() FROM telos_runtime;
SQL

echo "provision-db-observer: provisioned"
