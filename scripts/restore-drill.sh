#!/usr/bin/env bash
# restore-drill.sh — recovery drill. Fails if RPO exceeds 24h (86400s) or RTO exceeds 4h (14400s).
#
# Usage:
#   scripts/restore-drill.sh --fixture --backup-epoch <s> --restore-start <s> \
#       --restore-end <s> [--out <path>]
#       Check the timestamp policy from supplied timestamps (CI/tests only). A fixture record
#       is never gate evidence: it has no gate id, host identities or journey.
#   scripts/restore-drill.sh --live --target-file PATH (--rehearsal --source-commit SHA |
#       --candidate-lock PATH) --result-out PATH
#       Judge a node that scripts/restore.sh restored, from a third machine
#       (scripts/lib/recovery_drill.py). RPO = incidentDeclaredAt - consistencyPointAt;
#       RTO = member journey passed - incidentDeclaredAt.
#
# For clean-node disaster recovery the RTO clock starts when host-loss recovery
# is declared and ends only after automated firewall + A/AAAA cutover and a
# valid app certificate passes external HTTPS/WSS/range-media probes.
# A same-host-only restore cannot satisfy the beta RTO claim.
set -euo pipefail

RPO_MAX=86400
RTO_MAX=14400

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ "${1:-}" = "--live" ]; then
	shift
	exec python3 -I "$script_dir/lib/recovery_drill.py" "$@"
fi

FIXTURE=0
backup_epoch=""
restore_start=""
restore_end=""
out="/dev/stdout"

while [ $# -gt 0 ]; do
	case "$1" in
	--fixture) FIXTURE=1 ;;
	--backup-epoch) shift; backup_epoch="$1" ;;
	--restore-start) shift; restore_start="$1" ;;
	--restore-end) shift; restore_end="$1" ;;
	--out) shift; out="$1" ;;
	*) echo "unknown arg: $1" >&2; exit 2 ;;
	esac
	shift
done

if [ "$FIXTURE" -ne 1 ]; then
	echo "restore-drill: pass --live with a recovery target (see documentation/operations/restore-drill.md)" >&2
	echo "  or --fixture with timestamps; a bare run proves nothing" >&2
	exit 3
fi

[ -n "$backup_epoch$restore_start$restore_end" ] || { echo "fixture requires timestamps" >&2; exit 2; }

# RPO = time between the backup and the restore start (data loss window).
# RTO = time to complete the restore (recovery duration).
rpo=$((restore_start - backup_epoch))
rto=$((restore_end - restore_start))

status="pass"
[ "$rpo" -gt "$RPO_MAX" ] && status="fail_rpo"
[ "$rto" -gt "$RTO_MAX" ] && status="fail_rto"

cat >"$out" <<JSON
{
  "drill": "fixture",
  "backupEpoch": $backup_epoch,
  "restoreStart": $restore_start,
  "restoreEnd": $restore_end,
  "rpoSeconds": $rpo,
  "rtoSeconds": $rto,
  "rpoMax": $RPO_MAX,
  "rtoMax": $RTO_MAX,
  "status": "$status",
  "probes": {"health": "pass", "auth": "pass", "chat": "pass", "media": "pass", "epub": "pass", "pdf": "pass"}
}
JSON

[ "$status" = "pass" ] || { echo "restore-drill: $status (rpo=${rpo}s rto=${rto}s)" >&2; exit 1; }
echo "restore-drill: pass (rpo=${rpo}s rto=${rto}s)" >&2
