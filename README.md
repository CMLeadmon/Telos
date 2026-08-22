# Telos

> "Your server, your community."
> "Be on the net, but not of the net."

Telos is a self-hosted, digital-sovereignty platform: a community library for commentary on and storage of media. It merges Jellyfin-style streaming, Grimmory-style publication management, channel chat, and direct file management into one single-origin, unified application, so a community can read, watch, store, and discuss on a server it owns.

Source repository: [https://github.com/CMLeadmon/Telos](https://github.com/CMLeadmon/Telos)

Operate a node with the authoritative Bash dispatcher, `./telos` (start with
`./telos help`). The Go program in `backend/cmd/telos/` is experimental and is
not an operator entry point.

---

## 1. Project Documentation Index

All normative architecture specifications, deployment guides, state stores, and development roadmaps are documented under the `documentation/` folder:

* **Entry Point / Document Map:** [documentation/README.md](./documentation/README.md)
* **System Overview:** [documentation/architecture/01-system-overview.md](./documentation/architecture/01-system-overview.md)
* **Deployment Guide:** [documentation/architecture/02-deployment.md](./documentation/architecture/02-deployment.md)
* **Gateway & API Integration:** [documentation/architecture/03-gateway-and-api.md](./documentation/architecture/03-gateway-and-api.md)
* **Frontend Architecture:** [documentation/architecture/04-frontend-architecture.md](./documentation/architecture/04-frontend-architecture.md)
* **Roadmap & Licensing:** [documentation/architecture/05-roadmap-and-licensing.md](./documentation/architecture/05-roadmap-and-licensing.md)
* **Beta Feature Status:** [documentation/product/beta-feature-status.md](./documentation/product/beta-feature-status.md)
* **Agent Guide:** [AGENTS.md](./AGENTS.md)

---

## 2. Tech Stack Summary

* **Backend / Gateway:** Go 1.26.5 + Gorilla WebSocket + pgx/v5 (PostgreSQL) + go-redis/v9 (Redis cache).
* **Frontend Client:** React 19 + Next.js 16.2.10 (Node.js 24.18.0 LTS) + TypeScript + Zustand (chat/theme/preferences stores) + TailwindCSS v4.
* **Infrastructure Services:** Traefik v3 edge router (ACME TLS + file-provider routing), PostgreSQL 16.14 database, Redis 7 (pub/sub), MariaDB 10.11 (Grimmory catalog DB), Jellyfin (headless media transcoder), Grimmory (digital book server), and ClamAV (upload malware scanning).

---

## 3. License, Credits & Attributions

The Telos gateway and client in this repository are licensed under the
[Apache License 2.0](./LICENSE) (see also [NOTICE](./NOTICE)).

The bundled services (Jellyfin, Grimmory, ClamAV) and infrastructure
(Traefik, PostgreSQL, Redis, MariaDB) run as isolated processes behind
container boundaries under their own licenses. The canonical attribution
matrix — component, role, source, and license — lives in
[CREDITS.md](./CREDITS.md) and is mirrored by the in-app Settings → Credits
surface.

---

## 4. Privacy and Network Posture

A Telos node is reached over direct HTTPS through its Traefik edge, or over a
private network the operator runs. Outbound connections are limited to
integrations the operator enables: ACME certificate issuance, ClamAV signature
updates, and media/book metadata providers. Current backup scripts create local
plaintext recovery material; encrypted off-node backup and recovery are blocked
until Phase 4 evidence exists. Telos does not claim that no data ever leaves the
node; it documents exactly which operator-controlled channels exist.
