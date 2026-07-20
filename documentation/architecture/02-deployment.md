# Deployment

> Spec for AI coding agents and human developers. Statements are normative
> unless marked *(informative)*.

This document is the authoritative production deployment specification for
Telos. The executable Compose definition is [`../../docker-compose.yml`](../../docker-compose.yml);
do not duplicate it into documentation because the copy will drift.

## 1. Trust boundaries

| Network | Exposure | Members |
| --- | --- | --- |
| `telos-ingress` | edge bridge | `traefik`, `telos-core`, `livekit` |
| `telos-backend` | internal | `telos-core`, `redis`, `clamav`, `jellyfin`, `grimmory`, `livekit` |
| `telos-db` | internal | `telos-core`, `postgres`, `grimmory`, `grimmory-db` |

Only Traefik publishes HTTP ports 80 and 443. `telos-core:8080`,
`livekit:7880`, Jellyfin, Grimmory, PostgreSQL, Redis, MariaDB, and ClamAV must
not be published on host interfaces. LiveKit's WebRTC media ports remain
published because they are not HTTP traffic: 7881/TCP, 3478/UDP, and the
configured UDP relay range.

Traefik uses its read-only file provider. It must never receive the Podman or
Docker runtime socket, and its SELinux label must not be disabled. Dynamic
routing lives in [`../../config/dynamic/routes.yaml`](../../config/dynamic/routes.yaml).
The only public application services are the narrow Telos API/client and
LiveKit signaling routes; Jellyfin and Grimmory admin APIs stay internal.

## 2. Environment and secrets

Copy `.env.example` to `.env`, replace every example credential, and keep `.env`
out of version control. Production requires:

- `TELOS_ENV=production`;
- a real `TELOS_DOMAIN` whose A/AAAA records point to this host;
- `ACME_EMAIL` for Let's Encrypt notices;
- a newly generated `TELOS_BOOTSTRAP_TOKEN` of at least 32 characters;
- unique PostgreSQL, Redis, MariaDB, LiveKit, Jellyfin, and Grimmory secrets;
- the rootless runtime user's `APP_UID` and `APP_GID`.

Generate independent values instead of copying the examples:

```bash
openssl rand -hex 24
```

The gateway refuses to start in production when a required value is empty,
contains a known placeholder (including `generate-me`), or when the bootstrap
token is shorter than 32 characters. Development mode intentionally relaxes
cookie and origin protections and is never valid for an internet-facing node.

## 3. TLS and ingress

Traefik obtains certificates for `${TELOS_DOMAIN}` and
`turn.${TELOS_DOMAIN}` from Let's Encrypt with HTTP-01. Ports 80 and 443 must
reach Traefik, and both DNS names must resolve publicly before first boot. ACME
state is retained in the private `traefik_acme` volume. The static self-signed
`telos.local` certificate is not a production trust path.

The dynamic configuration uses Traefik's `env` template function, so the
`TELOS_DOMAIN` environment variable passed to Traefik is part of routing as well
as certificate issuance. Requests with an unrelated Host header do not reach
the application.

For development, layer the repository's separate override over the production
definition. It binds the gateway and LiveKit signaling to loopback; the public
frontend development server proxies their HTTP and WebSocket paths so clients
only need access to port 3000 for page, API, and signaling reachability:

```bash
podman compose -f docker-compose.yml -f docker-compose.dev.yml up -d
```

Plain HTTP on a non-loopback origin is not a browser secure context, so
microphone capture and voice publishing are unavailable through that port-3000
path. Use localhost for local voice development or place a trusted HTTPS proxy
or tunnel in front of the development server for a remote test. Never expose
port 3000 as the production entry point.

The frontend dev server must also allow the hostname used by that browser.
`frontend/next.config.ts` reads a comma-separated allowlist from
`TELOS_DEV_ORIGINS`. Put persistent machine-local values in the ignored
`frontend/.env.development.local`, or supply temporary LAN or Tailscale names
when starting the server:

```bash
cd frontend
TELOS_DEV_ORIGINS=telos-dev.lan,100.64.0.10 npm run dev
```

Never bind the development gateway or LiveKit signaling directly to public
interfaces, and never merge the development override into the production
Compose file.

## 4. Startup and health

Validate interpolation before starting:

```bash
podman compose config
podman compose up -d
```

`telos-core` has `restart: unless-stopped` and an HTTP healthcheck. `/api/v1/health`
returns `unhealthy` with HTTP 503 when PostgreSQL or Redis is unavailable. It
returns `degraded` with HTTP 200 when Jellyfin or Grimmory is unavailable so
operators can alert on lost features without creating an upstream-dependent
gateway restart loop.

After first boot, use the one-time bootstrap token to create the Owner account.
Rotate or remove `TELOS_BOOTSTRAP_TOKEN` from the runtime environment after the
Owner exists; the endpoint also refuses a second Owner bootstrap at the database
layer.

## 5. Storage

The default shared tree is `/mnt/storage/shared` and can be moved with
`STORAGE_PATH`:

- `media/` is the community media library and is mounted read-only in Jellyfin;
- `books/` is Grimmory's organized catalog;
- `bookdrop/` is the upload/ingestion inbox shared by Telos and Grimmory;
- `staging/` holds scanned Telos uploads.

PostgreSQL, Redis, Jellyfin configuration, Grimmory configuration, and MariaDB
use named volumes. Database and configuration durability does not replace a
backup. Follow the normative
[`../operations/backup-and-restore.md`](../operations/backup-and-restore.md)
runbook before admitting multiple users.

## 6. Security invariants

- All credentials come from `.env` interpolation; no secret is literal in
  source, Compose, or Traefik configuration.
- Jellyfin and Grimmory remain separate containerized processes. Their copyleft
  code is never linked or compiled into `telos-core`.
- The runtime socket is outside the perimeter container.
- Long-lived media responses are streamed, while connection establishment and
  upstream response headers are bounded by gateway timeouts.
- Production traffic reaches the application only through Traefik TLS.
