# System Overview

> **Superseded in part by the less-is-more redesign (2026-07).** Voice rooms,
> LiveKit/TURN, Watch Parties, the notification inbox, and My List were removed;
> Files were folded into the Library module (`?view=files`); annotations were
> generalized to commentary on any media target. Passages below describing those
> removed features — including the `livekit` service, the WebRTC/RTP media plane,
> and voice flows — are historical. See
> [beta-feature-status](../product/beta-feature-status.md).

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

## 1. Problem

Self-hosted platforms today are fragmented. A household running chat/voice, media streaming, an e-book catalog, and file management typically deploys four or more independent stacks, each with its own reverse proxy entry, its own authentication, its own container footprint, and its own upgrade cadence. This fragmentation produces three concrete costs:

- **Resource overhead** — redundant reverse proxies, redundant auth layers, and redundant background workers per stack, multiplied across every service the household wants.
- **Inconsistent UX** — every stack ships its own UI conventions, navigation model, and visual language, so users context-switch between unrelated interfaces to do related things.
- **Configuration fatigue** — each stack requires its own TLS termination, its own network wiring, and its own backup and update procedure, with no shared operational model.

Telos exists to collapse this fragmentation into a single deployable, single-domain, single-sign-on system without discarding the maturity of the underlying open-source services that already solve the hard technical problems (transcoding, WebRTC SFU, catalog ingestion).

## 2. Paradigm: Suite/Orchestration ("Strategy A")

Telos is a **suite/orchestration** system, not a monolith and not a plugin host. It is a single-port gateway plus a master UI that wraps dedicated, headless, best-of-breed open-source services, each running in its own isolated container.

The defining constraint of this strategy: Telos does not rewrite transcoders, e-book metadata parsers, or WebRTC SFUs. Jellyfin already solves transcoding. LiveKit already solves the SFU problem. Grimmory already solves catalog ingestion and metadata enrichment. Telos's job is orchestration, identity, routing, and presentation — not reimplementation.

Concretely, "Strategy A" means:

- Every underlying service (Jellyfin, Grimmory, LiveKit) runs **headless** — its own web UI is disabled or ignored; Telos's master UI is the only surface a user sees.
- A single ingress (Traefik) terminates TLS and fronts every service under one domain, path-routed.
- `telos-core` is the only component Telos builds from scratch: it is the gateway, the auth boundary, and the chat/presence backend.
- To the end user, the result reads as **one native application** — a single login, a single URL, a single navigation shell — even though it is composed of independently-maintained upstream projects running as separate containers.

## 3. Service inventory

All internal ports are container-internal (the port the process listens on inside its own container), not published host ports. Network columns list every Telos-managed Docker network the container joins.

| Service | Image | Role | Networks | Internal port |
| --- | --- | --- | --- | --- |
| `traefik` | `traefik:v3.3` | Edge router / reverse proxy | ingress | 80, 443 |
| `telos-core` | custom Go (or Rust) build | Gateway, auth, chat/presence backend | ingress, backend, db | 8080 |
| `postgres` | `postgres:16-alpine` | Chat + metadata persistence | db | 5432 |
| `redis` | `redis:7-alpine` | Presence/state/cache | backend | 6379 |
| `jellyfin` | `jellyfin/jellyfin:latest` | Headless transcoding + HLS streaming | backend | 8096 |
| `grimmory` | `grimmory/grimmory:latest` | Headless publication catalog | backend, db | 6060 |
| `grimmory-db` | `mariadb:10.11` | Grimmory's dedicated database | db | 3306 |
| `livekit` | `livekit/livekit-server:v1.10` | WebRTC SFU (voice/video) | ingress, backend | 7880 + published media ports |

Three Docker networks partition the stack by trust boundary:

- **`telos-ingress`** — joined only by Traefik and its two direct targets (`telos-core` and `livekit`). Nothing outside this network can be routed to by the edge.
- **`telos-backend`** — joined by every container that participates in application-level backend traffic (`telos-core`, `redis`, `clamav`, `jellyfin`, `grimmory`, `livekit`). This is where presence, cache, scans, and inter-service calls travel.
- **`telos-db`** — joined only by containers that speak to a database (`telos-core`, `postgres`, `grimmory`, `grimmory-db`). This network is not reachable from `telos-ingress`, isolating datastores from direct edge exposure.

`telos-core` straddles all three networks. Grimmory joins only backend and database networks and is never an ingress target.

## 4. Topology diagram

The diagram below shows the two fundamentally different traffic shapes in Telos: everything that is a request/response or signaling exchange goes through Traefik; real-time media (RTP/UDP) does not.

```
                     +----------+
                     |  Client  |
                     +----+-----+
                          |
                          | HTTPS :443 (all signaling, all API, all WS)
                          v
                     +-----------+
                     |  traefik  |
                     | (ingress) |
                     +-----+-----+
                           |
                           | file-provider routes
                    +------+------+
                    |             |
                    v             v
               /api/v1, /     /livekit
               auth, WS       signaling
                    |             |
                    v             v
              +-----------+ +-----------+
              |telos-core | |  livekit  |
              +-----+-----+ +-----------+
                    |
          +---------+----------+
          |         |          |
          v         v          v
   +----------+ +--------+ +----------+
   | postgres | | redis  | | Jellyfin |
   +----------+ +--------+ +----------+
                                |
                                v
                    +-----------------------+
                    |  shared host storage  |
                    +-----------------------+

   telos-core --------------------> grimmory

                                                +-------------+
                                     grimmory-> | grimmory-db |
                                                |  (mariadb)  |
                                                +-------------+


 RTP/UDP media plane -- direct client<->livekit, BYPASSES Traefik entirely:

    +----------+                                       +-----------+
    |  Client  | ====================================> |  livekit  |
    +----------+   RTP/UDP on published media ports,    +-----------+
                    no reverse proxy anywhere in path
```

Key facts this diagram encodes:

- **Traefik is a pure signaling/API path.** Every arrow that terminates at `traefik` is HTTPS request/response or WebSocket traffic — including LiveKit's `/livekit` path, which carries only signaling (room join, track negotiation), never media payload.
- **RTP/UDP media flows directly between the client and `livekit`**, on LiveKit's published media ports, entirely outside Traefik. This is the one traffic shape in the system that does not go through the edge router — a direct consequence of WebRTC's need for low-latency, connection-oriented UDP transport that an HTTP reverse proxy is not built to carry.
- `telos-core` is the only service with a direct edge to both `postgres` and `redis`.
- `livekit` uses `redis` for room/session state but has no direct database (`telos-db`) membership.
- `jellyfin` and `grimmory` share the same host storage mount (`/mnt/storage/shared`) for media/book files, while `grimmory` additionally owns a private relational store (`grimmory-db`) that no other service touches.

## 5. Key data flows

- **Chat** — client opens a WebSocket to `telos-core` (routed through `traefik`); messages persist to `postgres`; presence and typing/online state propagate via `redis` pub/sub to other connected `telos-core` instances.
- **Voice** — client requests a join token from `telos-core`, which mints a signed JWT; the client presents that JWT to `livekit` over the `/livekit` signaling path (through `traefik`); once the session negotiates, the actual audio/video media flows as direct RTP/UDP between client and `livekit` on published media ports, never touching `traefik`.
- **Media (streaming)** — video/audio playback requests use narrow `/api/v1/stream/*` handlers on `telos-core`; the gateway authenticates and streams the corresponding Jellyfin response without exposing Jellyfin's administrative API.
- **Books** — file uploads land in the `bookdrop` staging location; `grimmory` watches that location, ingests new files into its catalog, and fetches enrichment metadata from external sources (Google Books, Open Library) before persisting catalog records to `grimmory-db`.

## 6. Grimmory provenance

*(informative)* Grimmory (`https://github.com/grimmory-tools/grimmory`) is the community successor to BookLore, whose original maintainer withdrew the project in March 2026. Grimmory continues BookLore's architecture and is data-format compatible with BookLore's MariaDB schema, meaning an existing BookLore database can be adopted by Grimmory without migration. Telos integrates Grimmory rather than BookLore on this basis.

## See also

- [`02-deployment.md`](02-deployment.md) — compose topology, volumes, environment variables, and network definitions for every service listed above.
- [`03-gateway-and-api.md`](03-gateway-and-api.md) — `telos-core` routing rules, auth boundary, and the full `/api/v1` surface.
