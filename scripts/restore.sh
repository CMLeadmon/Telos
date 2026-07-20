#!/bin/sh
set -eu

backup=${1:-}
storage_path=${STORAGE_PATH:-/mnt/storage/shared}

if [ -z "$backup" ] || [ ! -f "$backup/SHA256SUMS" ]; then
	echo "usage: TELOS_RESTORE_CONFIRM=restore scripts/restore.sh BACKUP_DIRECTORY" >&2
	exit 2
fi
if [ "${TELOS_RESTORE_CONFIRM:-}" != "restore" ]; then
	echo "restore replaces live databases, service configuration, and shared files" >&2
	echo "set TELOS_RESTORE_CONFIRM=restore to continue" >&2
	exit 2
fi

(
	cd "$backup"
	sha256sum -c SHA256SUMS
)

podman stop telos-traefik telos-core telos-jellyfin telos-grimmory >/dev/null 2>&1 || true
podman start telos-postgres telos-grimmory-db >/dev/null

ready=0
while [ "$ready" -lt 60 ]; do
	if podman exec telos-postgres sh -c 'pg_isready --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' >/dev/null 2>&1 && \
		podman exec telos-grimmory-db healthcheck.sh --connect --innodb_initialized >/dev/null 2>&1; then
		break
	fi
	ready=$((ready + 1))
	sleep 1
done
if [ "$ready" -ge 60 ]; then
	echo "databases did not become ready within 60 seconds" >&2
	exit 1
fi

podman exec -i telos-postgres sh -c \
	'exec pg_restore --clean --if-exists --no-owner --username="$POSTGRES_USER" --dbname="$POSTGRES_DB"' \
	<"$backup/postgres.dump"

podman exec -i telos-grimmory-db sh -c \
	'exec mariadb --user=root --password="$MYSQL_ROOT_PASSWORD"' \
	<"$backup/grimmory.sql"

podman cp "$backup/jellyfin-config/." telos-jellyfin:/config
podman cp "$backup/grimmory-config/." telos-grimmory:/app/data
podman cp "$backup/traefik-acme/." telos-traefik:/letsencrypt

if [ -e "$storage_path" ]; then
	previous_storage="${storage_path}.pre-restore-$(date -u +%Y%m%dT%H%M%SZ)"
	mv "$storage_path" "$previous_storage"
	echo "previous shared storage preserved at $previous_storage"
fi
mkdir -p "$storage_path"
tar -C "$storage_path" -xzf "$backup/shared-storage.tar.gz"

podman start telos-traefik telos-jellyfin telos-grimmory telos-core >/dev/null
echo "restore complete; verify https://${TELOS_DOMAIN:-your-domain}/api/v1/health"
