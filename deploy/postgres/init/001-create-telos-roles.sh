#!/bin/sh
# Clean-volume role bootstrap for the Telos PostgreSQL database.
#
# Runs once, on a fresh data volume, via the postgres image's
# /docker-entrypoint-initdb.d hook. It creates two roles:
#   - telos_owner:   owns every Telos schema object; used only by the one-shot
#                    telos-migrate service (DATABASE_OWNER_URL).
#   - telos_runtime: the gateway's least-privilege login (DATABASE_URL) — schema
#                    usage + table DML + sequence usage + migration-state read,
#                    but no DDL, ownership, grants, or migration writes.
#
# Passwords come from the environment so no secret is written into this file.
set -eu

: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${TELOS_OWNER_PASSWORD:?TELOS_OWNER_PASSWORD is required}"
: "${TELOS_RUNTIME_PASSWORD:?TELOS_RUNTIME_PASSWORD is required}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'telos_owner') THEN
    CREATE ROLE telos_owner LOGIN PASSWORD '${TELOS_OWNER_PASSWORD}';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'telos_runtime') THEN
    CREATE ROLE telos_runtime LOGIN PASSWORD '${TELOS_RUNTIME_PASSWORD}';
  END IF;
END
\$\$;

-- The owner owns the schema; the runtime may use it but not create in it.
ALTER SCHEMA public OWNER TO telos_owner;
-- Let the owner install trusted extensions (pg_trgm) during migration.
GRANT CREATE ON DATABASE :"POSTGRES_DB" TO telos_owner;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO telos_runtime;

-- Default privileges: whatever telos_owner creates, telos_runtime may DML and
-- use sequences on. No DDL/ownership/grant is delegated.
ALTER DEFAULT PRIVILEGES FOR ROLE telos_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO telos_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE telos_owner IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO telos_runtime;
SQL

echo "telos: created telos_owner and telos_runtime roles"
