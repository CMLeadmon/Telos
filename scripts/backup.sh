#!/bin/sh
set -eu

umask 077

backup_root=${1:-backups}
storage_path=${STORAGE_PATH:-/mnt/storage/shared}
stamp=$(date -u +%Y%m%dT%H%M%SZ)
destination="$backup_root/$stamp"

mkdir -p "$destination/traefik-acme" "$destination/jellyfin-config" "$destination/grimmory-config"

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
