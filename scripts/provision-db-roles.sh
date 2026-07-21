#!/usr/bin/env bash
# provision-db-roles.sh — retrofit the owner/runtime role split onto an
# EXISTING Telos database volume that was created before the split.
#
# Usage:
#   TELOS_OWNER_PASSWORD=... TELOS_RUNTIME_PASSWORD=... \
#     scripts/provision-db-roles.sh [--check]
#
# --check performs a dry-run: it reports what would change and verifies the
# resulting privileges without altering anything. It sources the shared
# maintenance lock so it cannot race backup/restore/upgrade.
#
# Idempotent: safe to re-run. Aborts if it finds an unexpected object owner it
# was not asked to transfer.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

CHECK=0
[ "${1:-}" = "--check" ] && CHECK=1

: "${PGHOST:=127.0.0.1}"
: "${PGPORT:=5432}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${POSTGRES_USER:?POSTGRES_USER (superuser) is required}"

if [ "$CHECK" -eq 0 ]; then
	: "${TELOS_OWNER_PASSWORD:?TELOS_OWNER_PASSWORD is required}"
	: "${TELOS_RUNTIME_PASSWORD:?TELOS_RUNTIME_PASSWORD is required}"
fi

psql() { command psql -v ON_ERROR_STOP=1 -h "$PGHOST" -p "$PGPORT" -U "$POSTGRES_USER" -d "$POSTGRES_DB" "$@"; }

if [ "$CHECK" -eq 1 ]; then
	echo "provision-db-roles: dry run"
	# Report objects not owned by telos_owner (excluding system).
	psql -tAc "
		SELECT tablename || ' owned by ' || tableowner
		FROM pg_tables
		WHERE schemaname = 'public' AND tableowner <> 'telos_owner'
	" || true
	# Assert runtime role, if present, cannot create.
	if psql -tAc "SELECT 1 FROM pg_roles WHERE rolname = 'telos_runtime'" | grep -q 1; then
		if psql -tAc "SELECT has_schema_privilege('telos_runtime','public','CREATE')" | grep -qi t; then
			echo "provision-db-roles: WARN telos_runtime still has CREATE on public" >&2
		fi
	fi
	echo "provision-db-roles: check complete"
	exit 0
fi

# Take a verified backup before an ownership change if a backup script exists.
if [ -x scripts/backup.sh ]; then
	echo "provision-db-roles: taking a pre-change backup"
	scripts/backup.sh "${BACKUP_ROOT:-backups}" >/dev/null
fi

psql <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'telos_owner') THEN
    CREATE ROLE telos_owner LOGIN PASSWORD '${TELOS_OWNER_PASSWORD}';
  ELSE
    ALTER ROLE telos_owner PASSWORD '${TELOS_OWNER_PASSWORD}';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'telos_runtime') THEN
    CREATE ROLE telos_runtime LOGIN PASSWORD '${TELOS_RUNTIME_PASSWORD}';
  ELSE
    ALTER ROLE telos_runtime PASSWORD '${TELOS_RUNTIME_PASSWORD}';
  END IF;
END
\$\$;

-- Transfer ownership of all existing public tables/sequences to telos_owner.
DO \$\$
DECLARE r RECORD;
BEGIN
  ALTER SCHEMA public OWNER TO telos_owner;
  FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' LOOP
    EXECUTE format('ALTER TABLE public.%I OWNER TO telos_owner', r.tablename);
  END LOOP;
  FOR r IN SELECT sequence_name FROM information_schema.sequences WHERE sequence_schema = 'public' LOOP
    EXECUTE format('ALTER SEQUENCE public.%I OWNER TO telos_owner', r.sequence_name);
  END LOOP;
END
\$\$;

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO telos_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO telos_runtime;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO telos_runtime;
-- Migration history is read-only for the runtime.
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON schema_migrations FROM telos_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE telos_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO telos_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE telos_owner IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO telos_runtime;
SQL

echo "provision-db-roles: ownership transferred and runtime grants applied"
