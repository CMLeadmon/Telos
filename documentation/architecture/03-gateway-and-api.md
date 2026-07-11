# Gateway & API Integration

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

This document specifies how Telos routes traffic to its headless services, the static Traefik configuration that makes routing possible, the request-translation matrix `telos-core` implements over the headless Jellyfin and Grimmory APIs, and the unified OIDC/SSO identity flow. For the service inventory and topology, see [`01-system-overview.md`](01-system-overview.md). For the compose file and the Docker labels this document assumes, see [`02-deployment.md`](02-deployment.md).

## 1. Routing model

Telos presents a single origin to every client: `https://${TELOS_DOMAIN}`. There is no second domain, no subdomain-per-service, and no split between an "app" origin and an "API" origin. Because every request — chat, voice signaling, media streaming, book catalog — is same-origin, no cross-origin response headers are required anywhere in the stack; the browser never needs to be told which origins may read a response, because there is only ever one.

Routing itself is declared entirely as Docker labels on each service, not in any central routing file. The `traefik.http.routers.*` and `traefik.http.services.*` labels attached to `telos-core`, `jellyfin`, `grimmory`, and `livekit` in the `docker-compose.yml` (see [`02-deployment.md`](02-deployment.md), Section 3) are the single source of truth for what gets routed where. The static Traefik configuration file described in Section 2 below intentionally carries none of this: it configures only entry points, the Docker provider, and TLS. Adding, moving, or removing a route is a compose-file change, never a change to `config/traefik.yaml`.

Traefik v3 does not buffer response bodies by default. This matters for two traffic shapes in Telos:

- **HLS segment delivery** from `jellyfin` streams through Traefik untouched, segment by segment, rather than being accumulated in memory before forwarding.
- **HTTP Range requests** (used for seeking in audio/video playback and for partial book/asset downloads) pass through unmodified, preserving `206 Partial Content` semantics end to end.

The only place a buffering middleware is attached anywhere in the stack is on `telos-core`'s router, where the `upload-limits` middleware caps request bodies at 100 MB (`traefik.http.middlewares.upload-limits.buffering.maxRequestBodyBytes=104857600`) to bound file-manager and book-upload request sizes. No other router in the compose file carries a buffering middleware, and none should be added without a documented reason — buffering is the exception, not the default posture, in this deployment.

## 2. Static Traefik configuration

The complete `config/traefik.yaml`, mounted read-only into the `traefik` container (see [`02-deployment.md`](02-deployment.md), Section 3):

```yaml
entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint:
          to: websecure
          scheme: https
  websecure:
    address: ":443"

providers:
  docker:
    exposedByDefault: false
    network: telos-ingress

tls:
  certificates:
    - certFile: /certs/telos.crt
      keyFile: /certs/telos.key
```

`entryPoints.web` exists only to redirect plaintext `:80` traffic to `:443`; no service ever routes on the `web` entry point directly. `providers.docker.exposedByDefault: false` means a container is invisible to Traefik unless it explicitly carries `traefik.enable=true` — this is what makes the per-service labels in the compose file authoritative rather than incidental. `providers.docker.network: telos-ingress` pins the network Traefik uses to reach backends, matching the `telos-ingress` network membership documented in [`01-system-overview.md`](01-system-overview.md). The `tls` block loads the certificate pair mounted from `./config/certs`; there is no ACME/Let's Encrypt block here, since certificate acquisition is out of scope for this static file.

## 3. Headless API integration matrix

`telos-core` is the only service a client ever calls directly for these features. Internally, it translates each public `/api/v1/*` call into one or more requests against the headless Jellyfin or Grimmory APIs, both reachable only over `telos-backend` (never exposed to the client directly).

| Component | Telos endpoint | Internal headless request | Aggregation logic |
| --- | --- | --- | --- |
| Media Stream | `GET /api/v1/media` | Jellyfin `GET /Users/{userId}/Views` | Lists the user's available media libraries (views) as Telos media sections. |
| Media Catalog | `GET /api/v1/media/items` | Jellyfin `GET /Users/{userId}/Items` | Lists playable items within a library for the Telos media browser. |
| Audio Stream | `GET /api/v1/stream/audio/{id}` | Jellyfin `GET /Audio/{itemId}/stream` | Proxies a direct audio stream for the requested item. |
| Video Playback | `GET /api/v1/stream/video/{id}` | Jellyfin `GET /Videos/{itemId}/main.m3u8?PlaySessionId={sessionId}` | Proxies the HLS playlist and segment stream for adaptive video playback. |
| Digital Library | `GET /api/v1/library/books` | Grimmory `GET /api/v1/books` | Lists the book catalog for the Telos library view. |
| Library Facets | `GET /api/v1/library/facets` | Grimmory `GET /api/v1/books/facets` | Returns facet values (author, series, tags, etc.) for catalog filtering. |
| Read Progress | `POST /api/v1/library/progress` | Grimmory `POST /api/v1/books/progress` | Persists reading-progress updates against a book. |

## 4. Unified identity (OIDC/SSO)

`telos-core` is itself the OIDC identity provider for the entire stack, exposed at `/api/v1/auth/oidc`. There is exactly one login the user ever performs; every child service is federated against this same IdP rather than maintaining an independent credential store. A proxy-level interceptor on `telos-core` auto-provisions a matching child-service profile the first time a given user authenticates against a service that has not seen them before, so no manual per-service account creation is required.

**Jellyfin** federates via the `jellyfin-plugin-sso` plugin. It is configured by registering `telos-core` as an OpenID provider through Jellyfin's admin API:

```bash
curl -X POST "https://${TELOS_DOMAIN}/jellyfin/sso/OID/Add/telos-idp?api_key=${JELLYFIN_ADMIN_TOKEN}" \
     -H "Content-Type: application/json" \
     -d '{
       "oidEndpoint": "https://'"${TELOS_DOMAIN}"'/api/v1/auth/oidc",
       "oidClientId": "telos-jellyfin-client",
       "oidSecret": "'"${JELLYFIN_OIDC_SECRET}"'",
       "enabled": true,
       "enableAuthorization": true,
       "enableAllFolders": true,
       "defaultUsernameClaim": "preferred_username"
     }'
```

`JELLYFIN_ADMIN_TOKEN` and `JELLYFIN_OIDC_SECRET` are supplied via `.env` (see [`02-deployment.md`](02-deployment.md), Section 2) and referenced only as environment variables — never hardcoded. `defaultUsernameClaim: preferred_username` binds Jellyfin's local username to the same claim Telos's IdP issues, keeping identities consistent across both surfaces.

**Grimmory** federates via built-in OIDC support, configured entirely through environment variables rather than a plugin or a runtime API call. Claim templates map incoming token claims to Grimmory's user model at startup, synchronizing username, group membership, and admin-flag status each time a user session is established, so role and permission changes made in `telos-core` propagate to Grimmory without a separate administrative step.
