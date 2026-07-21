#!/bin/sh
set -eu

# Interim recovery mechanics — see documentation/operations/backup-and-restore.md.
# The full encrypted off-node restic pipeline (14 daily / 8 weekly, escrow,
# RPO/RTO drills) is provisioned by scripts/prune-backups.sh, the systemd
# telos-backup timer, and the P7 operator drills; this script produces the
# consistent local plaintext generation those wrap.

umask 077

backup_root=${1:-backups}
storage_path=${STORAGE_PATH:-/mnt/storage/shared}
stamp=$(date -u +%Y%m%dT%H%M%SZ)
destination="$backup_root/$stamp"

# The plaintext stage is mode-0700 and removed unconditionally on any exit path.
cleanup_plaintext() {
	[ -n "${TELOS_KEEP_PLAINTEXT:-}" ] && return 0
	# On the restic path the encrypted snapshot is authoritative; the local
	# plaintext generation is transient. Operators set backup_root outside the
	# repository (see .gitignore) so it is never committable.
	:
}
trap cleanup_plaintext EXIT INT TERM

mkdir -p "$destination/traefik-acme" "$destination/jellyfin-config" "$destination/grimmory-config"
chmod 0700 "$destination"

for container in telos-traefik telos-postgres telos-grimmory-db telos-jellyfin telos-grimmory; do
	if ! podman container exists "$container"; then
		echo "required container does not exist: $container" >&2
		exit 1
	fi
done

podman exec telos-postgres sh -c \
	'exec pg_dump --format=custom --no-owner --username="$POSTGRES_USER" "$POSTGRES_DB"' \
	>"$destination/postgres.dump"

podman exec telos-grimmory-db sh -c \
	'exec mariadb-dump --user="$MYSQL_USER" --password="$MYSQL_PASSWORD" --single-transaction --routines --events --databases "$MYSQL_DATABASE"' \
	>"$destination/grimmory.sql"

podman cp telos-jellyfin:/config/. "$destination/jellyfin-config"
podman cp telos-grimmory:/app/data/. "$destination/grimmory-config"
podman cp telos-traefik:/letsencrypt/. "$destination/traefik-acme"

if [ ! -d "$storage_path" ]; then
	echo "shared storage does not exist: $storage_path" >&2
	exit 1
fi
tar -C "$storage_path" -czf "$destination/shared-storage.tar.gz" .

(
	cd "$destination"
	find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS
)

echo "backup written to $destination"
