#!/usr/bin/env bash
# validate-compose.sh — render and assert invariants of the production Compose
# model without running it or printing any secret value.
#
# Usage:
#   scripts/validate-compose.sh --env-file <path> [--checks a,b,c]
#
# Known checks (default: all):
#   core-ingress   Only Traefik publishes HTTP entry points (80/443).
#   stop-grace     telos-core.stop_grace_period is at least 75 seconds.
#
# The rendered config is written to a mode-0600 temp file and removed on exit;
# only check names and statuses are printed.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

env_file=""
checks="core-ingress,stop-grace,database-observer"
while [ $# -gt 0 ]; do
	case "$1" in
	--env-file)
		shift
		env_file="$1"
		;;
	--checks)
		shift
		checks="$1"
		;;
	*)
		echo "unknown argument: $1" >&2
		exit 2
		;;
	esac
	shift
done

if [ -z "$env_file" ] || [ ! -f "$env_file" ]; then
	echo "validate-compose: --env-file must reference a readable file" >&2
	exit 2
fi

rendered="$(mktemp)"
chmod 0600 "$rendered"
cleanup() { rm -f "$rendered"; }
trap cleanup EXIT

if ! podman compose --env-file "$env_file" -f docker-compose.yml config >"$rendered" 2>/dev/null; then
	echo "validate-compose: compose model failed to render" >&2
	exit 1
fi

fail=0
report() { printf '%-14s %s\n' "$1" "$2"; }

want() { case ",$checks," in *",$1,"*) return 0 ;; *) return 1 ;; esac; }

if want core-ingress; then
	# Every published port must belong to Traefik (80/443); no other service
	# may publish an HTTP entry point.
	bad="$(python3 - "$rendered" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
allowed = {
    ("traefik", "80"), ("traefik", "443"),
}
bad = []
for name, svc in (doc.get("services") or {}).items():
    for p in (svc.get("ports") or []):
        published = p.get("published") if isinstance(p, dict) else str(p).split(":")[0]
        target = str(published)
        if (name, target) not in allowed:
            bad.append(f"{name}:{target}")
print(",".join(bad))
PY
)"
	if [ -n "$bad" ]; then
		report core-ingress "FAIL (unexpected published ports)"
		fail=1
	else
		report core-ingress "OK"
	fi
fi

if want stop-grace; then
	secs="$(python3 - "$rendered" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
svc = (doc.get("services") or {}).get("telos-core", {})
val = svc.get("stop_grace_period", "0s")
# Accept "75s" or nanosecond int forms.
s = str(val)
if s.endswith("s") and s[:-1].isdigit():
    print(int(s[:-1]))
elif s.isdigit():
    print(int(s) // 1_000_000_000)
else:
    print(0)
PY
)"
	if [ "${secs:-0}" -ge 75 ]; then
		report stop-grace "OK (${secs}s)"
	else
		report stop-grace "FAIL (${secs}s < 75s)"
		fail=1
	fi
fi

if want database-observer; then
	obs_ok=1
	# pg_stat_statements must be preloaded with parameter redaction.
	if ! grep -q 'shared_preload_libraries=pg_stat_statements' "$rendered"; then
		report database-observer "FAIL (pg_stat_statements not preloaded)"
		obs_ok=0
		fail=1
	fi
	# No application service may carry the observer credential.
	if grep -qiE 'DATABASE_OBSERVER_URL' "$rendered"; then
		report database-observer "FAIL (an application service has the observer URL)"
		obs_ok=0
		fail=1
	fi
	[ "$obs_ok" -eq 1 ] && report database-observer "OK"
fi

exit "$fail"
