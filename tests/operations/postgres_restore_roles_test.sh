#!/usr/bin/env bash
set -euo pipefail

# The dump/restore command lines in backup.sh and restore.sh, run against two real PostgreSQL
# clusters initialised by the shipped init scripts. A fresh restore target already holds the
# roles, the schema owner and the pg_stat_statements extension; restoring over it must leave
# every object owned by telos_owner, the extension owned by the superuser, telos_runtime
# unable to do DDL, and the data intact. (A fake-runtime test cannot see any of this: the first
# live restore failed on "must be owner of extension pg_stat_statements".)
#
# Needs initdb/pg_ctl/pg_dump/pg_restore/psql; set TELOS_PG_BIN (and LD_LIBRARY_PATH for a
# user-local install) or have them on PATH. Without them the test reports SKIPPED and exits 0.
repo_root="$(git rev-parse --show-toplevel)"
[[ -n "${TELOS_PG_BIN:-}" ]] && PATH="$TELOS_PG_BIN:$PATH"
for tool in initdb pg_ctl pg_dump pg_restore psql; do
  command -v "$tool" >/dev/null 2>&1 || { echo "postgres_restore_roles: SKIPPED (no $tool; set TELOS_PG_BIN)"; exit 0; }
done

work="$(mktemp -d)"
ports=(55601 55602)
stop_clusters() {
  local n
  for n in src dst; do pg_ctl -D "$work/$n" -m immediate stop >/dev/null 2>&1 || true; done
  rm -rf "$work"
}
trap stop_clusters EXIT

export POSTGRES_USER=postgres POSTGRES_DB=telos TELOS_OWNER_PASSWORD=owner-pw TELOS_RUNTIME_PASSWORD=runtime-pw
export PGHOST="$work"   # unix sockets live in the work directory

make_cluster() {   # name port
  initdb -D "$work/$1" -U postgres --auth=trust -E UTF8 >/dev/null
  pg_ctl -D "$work/$1" -o "-p $2 -c listen_addresses= -c unix_socket_directories=$work" -l "$work/$1.log" -w start >/dev/null
  PGPORT="$2" createdb -U postgres telos
  local script
  for script in "$repo_root"/deploy/postgres/init/*.sh; do PGPORT="$2" sh "$script" >/dev/null; done
}
make_cluster src "${ports[0]}"
make_cluster dst "${ports[1]}"

# The source: every shipped migration, applied by telos_owner as the migrate service does.
for migration in "$repo_root"/backend/db/migrations/*.sql; do
  PGPORT="${ports[0]}" psql -q -v ON_ERROR_STOP=1 -U telos_owner -d telos -f "$migration" >/dev/null
done
PGPORT="${ports[0]}" psql -q -v ON_ERROR_STOP=1 -U telos_owner -d telos \
  -c "CREATE TABLE restore_probe (id int PRIMARY KEY, note text); INSERT INTO restore_probe VALUES (1, 'kept'), (2, 'also kept');"

# The exact command lines the scripts run inside the postgres container.
dump_command="$(grep -o "pg_dump --format=custom[^']*" "$repo_root/scripts/backup.sh" | head -n1)"
restore_command="$(grep -o "pg_restore --clean[^']*" "$repo_root/scripts/restore.sh" | head -n1)"
[[ -n "$dump_command" && -n "$restore_command" ]] || { echo "postgres_restore_roles: FAIL cannot find the dump/restore command lines" >&2; exit 1; }

PGPORT="${ports[0]}" sh -c "exec $dump_command" >"$work/postgres.dump"
PGPORT="${ports[1]}" sh -c "exec $restore_command" <"$work/postgres.dump"

q() { PGPORT="${ports[1]}" psql -qtAX -v ON_ERROR_STOP=1 -U postgres -d telos -c "$1"; }
fail() { echo "postgres_restore_roles: FAIL $1" >&2; exit 1; }

[[ "$(q "SELECT count(*) FROM pg_tables WHERE schemaname='public'")" -gt 10 ]] || fail "the schema did not come back"
[[ "$(q "SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tableowner <> 'telos_owner'")" == 0 ]] \
  || fail "a restored table is not owned by telos_owner: $(q "SELECT string_agg(tablename||'='||tableowner, ',') FROM pg_tables WHERE schemaname='public' AND tableowner <> 'telos_owner'")"
[[ "$(q "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('S','v','m') AND pg_get_userbyid(c.relowner) <> 'telos_owner' AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid=c.oid AND d.deptype='e')")" == 0 ]] \
  || fail "a restored sequence or view is not owned by telos_owner"
[[ "$(q "SELECT pg_get_userbyid(extowner) FROM pg_extension WHERE extname='pg_stat_statements'")" == postgres ]] \
  || fail "pg_stat_statements is missing or not owned by the superuser"
[[ "$(q "SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname='public'")" == telos_owner ]] || fail "the public schema is not owned by telos_owner"
[[ "$(q "SELECT count(*) FROM restore_probe")" == 2 ]] || fail "data did not survive"

# The role split must still hold: the runtime role does DML but no DDL; the owner does DDL.
rq() { PGPORT="${ports[1]}" psql -qtAX -U telos_runtime -d telos -c "$1" 2>&1; }
[[ "$(rq "SELECT count(*) FROM restore_probe")" == 2 ]] || fail "telos_runtime cannot read a restored table"
rq "UPDATE restore_probe SET note = 'runtime' WHERE id = 1" >/dev/null || fail "telos_runtime cannot write a restored table"
ddl_result="$(rq "CREATE TABLE runtime_ddl (id int)" || true)"
[[ "$ddl_result" == *"permission denied"* ]] || fail "telos_runtime can run DDL after a restore (got: $ddl_result)"
PGPORT="${ports[1]}" psql -qtAX -v ON_ERROR_STOP=1 -U telos_owner -d telos -c "ALTER TABLE restore_probe ADD COLUMN owner_ddl int" >/dev/null \
  || fail "telos_owner cannot alter a restored table"
# New tables created after the restore still reach the runtime role through the default privileges.
PGPORT="${ports[1]}" psql -qtAX -v ON_ERROR_STOP=1 -U telos_owner -d telos -c "CREATE TABLE after_restore (id int); INSERT INTO after_restore VALUES (1)" >/dev/null
[[ "$(rq "SELECT count(*) FROM after_restore")" == 1 ]] || fail "default privileges for telos_runtime did not survive the restore"
# The observer role keeps its read-only window on the statistics view.
[[ "$(q "SELECT has_table_privilege('telos_observer', 'pg_stat_statements', 'SELECT')")" == t ]] || fail "telos_observer lost access to pg_stat_statements"
[[ "$(q "SELECT has_table_privilege('telos_runtime', 'pg_stat_statements', 'SELECT')")" == f ]] || fail "telos_runtime gained access to pg_stat_statements"

echo "postgres_restore_roles: ownership, role split, extension and data survive a restore over an initialised cluster"
