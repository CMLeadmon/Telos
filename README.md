# Telos

> "Your server, your community."
> "Be on the net, but not of the net."

Telos is a self-hosted, digital sovereignty platform that merges Discord-style chat/voice, Jellyfin-style streaming, Grimmory-style publication management, and direct file management into one single-origin, unified application.

---

## 1. Project Documentation Index

All normative architecture specifications, deployment guides, state stores, and development roadmaps are documented under the `documentation/` folder:

* **Entry Point / Document Map:** [documentation/README.md](file:///home/cleadmon/Projects/Telos/documentation/README.md)
* **System Overview:** [documentation/architecture/01-system-overview.md](file:///home/cleadmon/Projects/Telos/documentation/architecture/01-system-overview.md)
* **Deployment Guide:** [documentation/architecture/02-deployment.md](file:///home/cleadmon/Projects/Telos/documentation/architecture/02-deployment.md)
* **Gateway & API Integration:** [documentation/architecture/03-gateway-and-api.md](file:///home/cleadmon/Projects/Telos/documentation/architecture/03-gateway-and-api.md)
* **Frontend Architecture:** [documentation/architecture/04-frontend-architecture.md](file:///home/cleadmon/Projects/Telos/documentation/architecture/04-frontend-architecture.md)
* **Roadmap & Licensing:** [documentation/architecture/05-roadmap-and-licensing.md](file:///home/cleadmon/Projects/Telos/documentation/architecture/05-roadmap-and-licensing.md)
* **Agent Guide:** [AGENTS.md](file:///home/cleadmon/Projects/Telos/AGENTS.md)

---

## 2. Tech Stack Summary

* **Backend / Gateway:** Go (Go 1.22+ routing) + Gorilla WebSocket + pgx/v5 (PostgreSQL) + go-redis/v9 (Redis cache).
* **Frontend Client:** React + Next.js + TypeScript + Zustand (real-time voice/theme stores) + TailwindCSS v4.
* **Infrastructure Services:** Traefik v3.3 edge router (TLS + routing labels), PostgreSQL 16 database, Redis 7 (pub/sub), MariaDB 10.11 (Grimmory catalog DB), Jellyfin (headless media transcoder), Grimmory (digital book server), and LiveKit (WebRTC SFU).

---

## 3. Credits & Attributions

In accordance with Section 5 of [documentation/architecture/05-roadmap-and-licensing.md](file:///home/cleadmon/Projects/Telos/documentation/architecture/05-roadmap-and-licensing.md), below is the canonical attribution matrix of bundled open-source services:

| Component | Source Repository | License | Role in Telos |
|---|---|---|---|
| **Jellyfin** | `https://github.com/jellyfin/jellyfin` | GPL-2.0 | Headless media transcoding, HLS segment generation, and content streaming. |
| **Grimmory** | `https://github.com/grimmory-tools/grimmory` | AGPL-3.0 | E-book and publication catalog, metadata fetching, and reading progress synchronization. |
| **LiveKit** | `https://github.com/livekit/livekit` | Apache-2.0 | WebRTC SFU, audio rooms, signaling, and real-time participant synchronization. |

*(Note: Traefik, PostgreSQL, Redis, and MariaDB are run in isolated containers as part of the orchestration aggregation).*
