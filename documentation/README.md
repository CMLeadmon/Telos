# Telos Documentation

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

## 1. Vision

Telos merges Discord-style chat/voice, Jellyfin-style streaming, Grimmory-style publication management, and file management into one self-hosted, single-origin application. The system serves as an infrastructure of digital sovereignty, guided by the slogans "your server, your community" and "be on the net, but not of the net."

---

## 2. Quick Facts

| Parameter | Specification |
|---|---|
| **Default Host** | `telos.local` |
| **Tech Stack** | Go/Rust core gateway + React/TypeScript/Zustand client |
| **Headless Services** | Traefik v3.3, PostgreSQL 16, Redis 7, Jellyfin, Grimmory, MariaDB 10.11, LiveKit |
| **Theming System** | Flat light / Flat dark (default) / Vaporwave (classic neon) |
| **Licensing Target** | MIT or Apache-2.0 for custom gateway/client core; child services run under their respective copyleft licenses (GPL/AGPL) |

---

## 3. Document Map

### Technical Architecture Specification

| Document | Purpose |
|---|---|
| [`architecture/01-system-overview.md`](./architecture/01-system-overview.md) | Suite-orchestration paradigm, network ports, volume paths, and system topology. |
| [`architecture/02-deployment.md`](./documentation/../architecture/02-deployment.md) | Secret interpolation schema, `.env.example` configurations, and multi-network Compose files. |
| [`architecture/03-gateway-and-api.md`](./architecture/03-gateway-and-api.md) | Ingress routing labels, static Traefik parameters, SSO curl examples, and core REST API matrices. |
| [`architecture/04-frontend-architecture.md`](./architecture/04-frontend-architecture.md) | Zustand real-time stores, LiveKit v2 listener connections, and persistent app shell integrations. |
| [`architecture/05-roadmap-and-licensing.md`](./architecture/05-roadmap-and-licensing.md) | Integrated roadmap phases, mitigation strategies, copyleft boundary analysis, and service attributions. |

---

## 4. Reading Order

- **For Infrastructure & Operations:** Study the system layout in [`architecture/01-system-overview.md`](./architecture/01-system-overview.md), then inspect the Compose configurations in [`architecture/02-deployment.md`](./architecture/02-deployment.md).
- **Logo Renders:** The canonical monochromatic logo renders reside at `resources/logos/Telos_sun_ink.svg`. The custom vaporwave/synthwave logo variant is located at `resources/logos/Telos_sun_synthwave.svg`.

---

## 5. History Note

*(informative)* This structured documentation set supersedes the original monolithic `design.md` and `implementation.md` files, which are preserved for historical reference in the repository's git history.
