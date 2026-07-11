# Deployment

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

This document is the authoritative deployment specification for Telos: the Docker network model, the environment variables that parameterize every service, the complete `docker-compose.yml`, the media transport boundary, and the shared storage layout. For the service inventory and topology this deployment implements, see [`01-system-overview.md`](01-system-overview.md). For `telos-core`'s routing rules, auth boundary, and API surface (including the Traefik labels summarized here), see [`03-gateway-and-api.md`](03-gateway-and-api.md).

## 1. Network model

Three Docker networks partition the stack by trust boundary. Membership below matches the `docker-compose.yml` in Section 3 exactly.

| Network | Type | Members |
| --- | --- | --- |
| `telos-ingress` | exposed (bridge) | `traefik`, `telos-core`, `jellyfin`, `grimmory`, `livekit` |
| `telos-backend` | internal | `telos-core`, `redis`, `jellyfin`, `grimmory`, `livekit` |
| `telos-db` | internal | `postgres`, `grimmory-db`, `grimmory`, `telos-core` |

Two fixes versus earlier drafts *(informative)*:

- Every Traefik-routed service must share `telos-ingress` with Traefik itself — a container cannot receive edge-routed traffic unless it is reachable on the same network as the router.
- `telos-core` must join `telos-db` directly, since it is the only component that queries PostgreSQL; omitting this membership would leave `telos-core` unable to reach its own persistence layer.

## 2. Secrets

All credentials are supplied by a `.env` file at the repository root, which is never committed to version control. The `docker-compose.yml` in Section 3 references every credential exclusively through `${VAR}` interpolation — no literal secret value appears in the compose file itself. `.env.example` documents every variable the stack requires, with placeholder values that must be replaced before deployment:

```ini
TELOS_DOMAIN=telos.local
APP_UID=1000
APP_GID=1000
TZ=Etc/UTC

POSTGRES_USER=telos
POSTGRES_PASSWORD=change-me
POSTGRES_DB=telos

REDIS_PASSWORD=change-me

LIVEKIT_API_KEY=change-me
LIVEKIT_API_SECRET=change-me

JELLYFIN_ADMIN_TOKEN=change-me
JELLYFIN_OIDC_SECRET=change-me
GRIMMORY_API_TOKEN=change-me

GRIMMORY_DB_NAME=grimmory
GRIMMORY_DB_USER=grimmory
GRIMMORY_DB_PASSWORD=change-me
MARIADB_ROOT_PASSWORD=change-me
```

## 3. Compose file

The complete, corrected `docker-compose.yml` for the stack:

```yaml
networks:
  telos-ingress:
    name: telos-ingress
    driver: bridge
  telos-backend:
    name: telos-backend
    driver: bridge
    internal: true
  telos-db:
    name: telos-db
    driver: bridge
    internal: true

volumes:
  postgres_data:
  redis_data:
  jellyfin_config:
  grimmory_config:
  grimmory_db_data:

services:
  traefik:
    image: traefik:v3.3
    container_name: telos-traefik
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
      - label=disable
    networks:
      - telos-ingress
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ${XDG_RUNTIME_DIR}/podman/podman.sock:/var/run/docker.sock:ro,z
      - ./config/traefik.yaml:/etc/traefik/traefik.yaml:ro,z
      - ./config/certs:/certs:ro,z
      - ./config/dynamic:/etc/traefik/dynamic:ro,z

  telos-core:
    build:
      context: .
      dockerfile: backend/Dockerfile
    container_name: telos-core
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    networks:
      - telos-ingress
      - telos-backend
      - telos-db
    environment:
      - DATABASE_URL=postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable
      - REDIS_URL=redis://:${REDIS_PASSWORD}@redis:6379/0
      - LIVEKIT_API_KEY=${LIVEKIT_API_KEY}
      - LIVEKIT_API_SECRET=${LIVEKIT_API_SECRET}
      - JELLYFIN_ADMIN_TOKEN=${JELLYFIN_ADMIN_TOKEN}
      - GRIMMORY_API_TOKEN=${GRIMMORY_API_TOKEN}
    volumes:
      - ${STORAGE_PATH:-/mnt/storage/shared}:/data/shared:z
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=telos-ingress"
      - "traefik.http.routers.telos-core.rule=PathPrefix(`/`)"
      - "traefik.http.routers.telos-core.entrypoints=websecure"
      - "traefik.http.routers.telos-core.tls=true"
      - "traefik.http.routers.telos-core.middlewares=upload-limits"
      - "traefik.http.middlewares.upload-limits.buffering.maxRequestBodyBytes=104857600"
      - "traefik.http.services.telos-core.loadbalancer.server.port=8080"

  postgres:
    image: postgres:16-alpine
    container_name: telos-postgres
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    networks:
      - telos-db
    environment:
      - POSTGRES_USER=${POSTGRES_USER}
      - POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
      - POSTGRES_DB=${POSTGRES_DB}
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER} -d ${POSTGRES_DB}"]
      interval: 5s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    container_name: telos-redis
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    networks:
      - telos-backend
    command: redis-server --appendonly yes --requirepass ${REDIS_PASSWORD}
    environment:
      - REDISCLI_AUTH=${REDIS_PASSWORD}
    volumes:
      - redis_data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 5s
      retries: 5

  jellyfin:
    image: jellyfin/jellyfin:latest
    container_name: telos-jellyfin
    restart: unless-stopped
    user: "${APP_UID}:${APP_GID}"
    networks:
      - telos-ingress
      - telos-backend
    environment:
      - TZ=${TZ}
      - JELLYFIN_PublishedServerUrl=https://${TELOS_DOMAIN}/jellyfin
    volumes:
      - jellyfin_config:/config
      - ${STORAGE_PATH:-/mnt/storage/shared}/media:/data/media:ro,z
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=telos-ingress"
      - "traefik.http.routers.jellyfin.rule=PathPrefix(`/jellyfin`)"
      - "traefik.http.routers.jellyfin.entrypoints=websecure"
      - "traefik.http.routers.jellyfin.tls=true"
      - "traefik.http.services.jellyfin.loadbalancer.server.port=8096"

  grimmory-db:
    image: mariadb:10.11
    container_name: telos-grimmory-db
    restart: unless-stopped
    networks:
      - telos-db
    environment:
      - MYSQL_ROOT_PASSWORD=${MARIADB_ROOT_PASSWORD}
      - MYSQL_DATABASE=${GRIMMORY_DB_NAME}
      - MYSQL_USER=${GRIMMORY_DB_USER}
      - MYSQL_PASSWORD=${GRIMMORY_DB_PASSWORD}
    volumes:
      - grimmory_db_data:/var/lib/mysql
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 10s
      timeout: 5s
      retries: 3

  grimmory:
    image: grimmory/grimmory:latest
    container_name: telos-grimmory
    restart: unless-stopped
    depends_on:
      grimmory-db:
        condition: service_healthy
    networks:
      - telos-ingress
      - telos-backend
      - telos-db
    environment:
      - USER_ID=${APP_UID}
      - GROUP_ID=${APP_GID}
      - TZ=${TZ}
      - DATABASE_URL=jdbc:mariadb://grimmory-db:3306/${GRIMMORY_DB_NAME}
      - DATABASE_USERNAME=${GRIMMORY_DB_USER}
      - DATABASE_PASSWORD=${GRIMMORY_DB_PASSWORD}
      - API_DOCS_ENABLED=true
      - DISK_TYPE=LOCAL
    volumes:
      - grimmory_config:/app/data
      - ${STORAGE_PATH:-/mnt/storage/shared}/books:/books:z
      - ${STORAGE_PATH:-/mnt/storage/shared}/bookdrop:/bookdrop:z
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O - http://localhost:6060/api/v1/healthcheck"]
      interval: 60s
      timeout: 10s
      retries: 5
      start_period: 60s
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=telos-ingress"
      - "traefik.http.routers.grimmory.rule=PathPrefix(`/grimmory`)"
      - "traefik.http.routers.grimmory.entrypoints=websecure"
      - "traefik.http.routers.grimmory.tls=true"
      - "traefik.http.services.grimmory.loadbalancer.server.port=6060"

  livekit:
    image: livekit/livekit-server:v1.10
    container_name: telos-livekit
    restart: unless-stopped
    command: --config /etc/livekit/config.yaml --redis-password ${REDIS_PASSWORD}
    depends_on:
      redis:
        condition: service_healthy
    networks:
      - telos-ingress
      - telos-backend
    environment:
      - "LIVEKIT_KEYS=${LIVEKIT_API_KEY}: ${LIVEKIT_API_SECRET}"
      - REDIS_PASSWORD=${REDIS_PASSWORD}
    ports:
      - "7881:7881"
      - "3478:3478/udp"
      - "50000-50100:50000-50100/udp"
    volumes:
      - ./config/livekit.yaml:/etc/livekit/config.yaml:ro,z
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=telos-ingress"
      - "traefik.http.routers.livekit.rule=PathPrefix(`/livekit`)"
      - "traefik.http.routers.livekit.entrypoints=websecure"
      - "traefik.http.routers.livekit.tls=true"
      - "traefik.http.middlewares.livekit-strip.stripprefix.prefixes=/livekit"
      - "traefik.http.routers.livekit.middlewares=livekit-strip"
      - "traefik.http.services.livekit.loadbalancer.server.port=7880"
```

## 4. Media transport note

Traefik proxies only LiveKit's HTTP/WebSocket signaling path (room join, track negotiation) under `/livekit`. RTP media never passes through Traefik: it flows directly between clients and the `livekit` container on the published UDP/TCP ports (`7881/tcp`, `3478/udp`, `50000-50100/udp`). Never route media through the HTTP proxy — an HTTP reverse proxy is not built to carry low-latency, connection-oriented UDP transport, and attempting to do so would defeat the purpose of a dedicated WebRTC SFU.

## 5. Storage layout

All media, book, and staging data lives under a single host mount, `/mnt/storage/shared`, subdivided by role:

- **`/mnt/storage/shared/media`** — video and audio libraries. Mounted read-only into `jellyfin` (`/data/media:ro`); Jellyfin only ever reads from this path, it never writes back to it.
- **`/mnt/storage/shared/books`** — Grimmory's canonical, organized book catalog storage, mounted read-write into `grimmory` (`/books`).
- **`/mnt/storage/shared/bookdrop`** — the staging/inbox path, mounted read-write into `grimmory` (`/bookdrop`). Uploads from the Telos file manager land here. Grimmory's watcher process monitors this location, ingests new files into its catalog, pulls enrichment metadata from external sources (Google Books, Open Library), and queues the resulting records for review before they are promoted into `books`.

`DISK_TYPE=LOCAL` configures Grimmory to perform transactional renames when moving files between `bookdrop` and `books`, rather than unsafe concurrent writes — this matters because the underlying storage is a local (non-networked, non-distributed) filesystem where atomic rename semantics are guaranteed, and Grimmory relies on that guarantee to avoid partially-written or corrupted catalog entries during ingestion.
