# Roadmap & Licensing

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

## 1. Phased Roadmap

The Telos deployment and integration roadmap is divided into four distinct phases, prioritizing base services before layering cataloging and real-time communication modules.

| Stage | Weeks | Scope | Primary Tasks | Risk & Mitigation |
|---|---|---|---|---|
| **Phase 1: Foundation** | 1–6 | Core Gateway & Chat | PostgreSQL schemas, Redis cache, WebSocket chat core, Traefik edge router configuration | **Risk:** Redis state collisions. <br>**Mitigation:** Implement isolated key prefixes for distinct module domains. |
| **Phase 2: Storage & Media** | 7–12 | Volume Management & Streaming | Headless Jellyfin paths, shared host volume mounts, HLS.js custom player integration | **Risk:** Disk I/O transcoding bottlenecks. <br>**Mitigation:** Utilize non-blocking Go channels and configure hardware acceleration where host-supported. |
| **Phase 3: Catalog** | 13–18 | Digital Library | Grimmory integration, catalog watch folders, EPUB/PDF reader controls (OIDC federation is outside beta scope) | **Risk:** Metadata update desynchronization. <br>**Mitigation:** Enforce database constraints and watcher triggers in Grimmory. |
| **Phase 4: Real-Time** | 19–24 | Voice Session Layer | LiveKit server provisioning, WebRTC signaling path, voice session Zustand store, latency tuning | **Risk:** WebRTC packet jitter behind strict symmetric NATs. <br>**Mitigation:** Configure public STUN/TURN servers and expose dedicated host port ranges. |

---

## 2. Copyleft Boundary Analysis

Telos aggregates various third-party open-source components with copyleft licenses (e.g. GPL-2.0, AGPL-3.0). The custom Telos gateway and client codebase is licensed under Apache-2.0 (see the repository `LICENSE` and `NOTICE`). To ship it without triggering copyleft requirements, the platform enforces strict structural boundaries:

1. **Process Separation:** Every service runs inside its own isolated Docker container. No shared memory or binary linking exists between the Telos core gateway and the third-party dependencies.
2. **Standardized Network APIs:** Communication between the core gateway and downstream components (Jellyfin, Grimmory, LiveKit) is performed exclusively via standardized network protocols over HTTP/JSON, WebSockets, or gRPC.
3. **No Build-Time Linking:** The Telos gateway executable does not dynamically or statically link any GPL or AGPL libraries during compilation.
4. **Independent Operability:** The gateway remains functional if individual sub-services are stopped or disabled; it continues serving the rest of the application suite ("mere aggregation").

Consequently, the custom Telos core and client components remain outside the copyleft derivative-work boundary.

---

## 3. Modification Obligations

Any direct changes made to the source code of copyleft components during customization or deployment must be handled in compliance with their respective licenses:
- **Jellyfin:** Modifications to the Jellyfin server or its plugins must be published and distributed under the GNU GPL-2.0 (or later).
- **Grimmory:** Modifications to the Grimmory application code must be made publicly available via network source disclosure under GNU AGPL-3.0 §13.

---

## 4. Compliance Verification

Automated security and licensing audits must verify that:
- No copyleft-licensed source files are included or imported in the `backend/` or `frontend/` directories.
- All inter-service calls are strictly checked to ensure they cross container and network boundaries.
- No third-party copyleft binaries are packaged within the gateway Docker image.

---

## 5. Attributions

The canonical attribution matrix — every isolated service and infrastructure component with its role, source repository, and license — is maintained in the repository root [`CREDITS.md`](../../CREDITS.md). The in-app Settings → Credits surface renders a reviewed static projection of the same matrix, and the project README links to it. The matrix covers Jellyfin (GPL-2.0), Grimmory (AGPL-3.0), LiveKit (Apache-2.0), ClamAV (GPL-2.0), Traefik (MIT), PostgreSQL (PostgreSQL License), Redis (RSALv2/SSPLv1), and MariaDB (GPL-2.0).
