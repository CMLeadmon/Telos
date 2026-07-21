#!/bin/sh
# Clean-volume provisioning for safe query statistics.
#
# Creates the pg_stat_statements extension (owned by the superuser/owner),
# a NOLOGIN telos_observer role that receives only pg_read_all_stats + CONNECT +
# read access to the statistics views, and revokes that access plus the reset
# function from PUBLIC and telos_runtime. An operator login is attached
# separately (DATABASE_OBSERVER_URL_FILE) and is never mounted into any
# application container.
set -eu

: "${POSTGRES_DB:?POSTGRES_DB is required}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'SQL'
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'telos_observer') THEN
    CREATE ROLE telos_observer NOLOGIN;
  END IF;
END
$$;

GRANT pg_read_all_stats TO telos_observer;
GRANT CONNECT ON DATABASE :"POSTGRES_DB" TO telos_observer;
GRANT USAGE ON SCHEMA public TO telos_observer;
GRANT SELECT ON pg_stat_statements TO telos_observer;

-- Deny statistics text and the reset function to PUBLIC and the runtime role.
REVOKE ALL ON pg_stat_statements FROM PUBLIC;
REVOKE ALL ON pg_stat_statements FROM telos_runtime;
REVOKE EXECUTE ON FUNCTION pg_stat_statements_reset() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION pg_stat_statements_reset() FROM telos_runtime;
SQL

echo "telos: created telos_observer role and pg_stat_statements"
