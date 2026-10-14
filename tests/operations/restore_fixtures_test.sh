#!/usr/bin/env bash
set -euo pipefail

# The restore fixture suite: pre-flight refusals and ordering against a fake runtime, the
# archive/manifest/host/candidate/schema checks, the recovery drill's judgement, and the real
# command lines against two real PostgreSQL clusters. The PostgreSQL part exits 3 (not_run) when
# no PostgreSQL 16 client/server binaries are available, and that status is preserved: a restore
# that was never replayed against a real database is not verified.
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
bash scripts/tests/restore_test.sh
python3 -I tests/operations/restore_check_test.py
python3 -I tests/operations/recovery_drill_test.py
status=0
bash tests/operations/postgres_restore_roles_test.sh || status=$?
if [[ $status -ne 0 ]]; then
  echo "restore fixtures: the PostgreSQL replay did not pass (exit $status)" >&2
  exit "$status"
fi
echo "restore fixtures: refusals, ordering, sanitization contract, drill judgement and a real database replay verified"
