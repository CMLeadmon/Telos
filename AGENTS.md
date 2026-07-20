# Telos — Agent Guide

This repository contains the Telos platform: the Go core gateway (`backend/`), the Next.js client (`frontend/`), the container orchestration (`docker-compose.yml`), and the normative specification (`documentation/`). The docs remain the buildable specification; where code and docs disagree, flag the discrepancy. The project is guided by two core slogans: "your server, your community" (community-facing) and "be on the net, but not of the net" (philosophical/technical).

---

## 1. Read This First

Refer to this matrix to find the appropriate entry point for your development intent:

| Intent | Entry Point |
|---|---|
| **Deploy or Manage Infrastructure** | [`documentation/architecture/01-system-overview.md`](./documentation/architecture/01-system-overview.md) through [`documentation/architecture/03-gateway-and-api.md`](./documentation/architecture/03-gateway-and-api.md) |
| **Manage Frontend State & WebRTC** | [`documentation/architecture/04-frontend-architecture.md`](./documentation/architecture/04-frontend-architecture.md) |
| **Understand the Roadmap & Licensing** | [`documentation/architecture/05-roadmap-and-licensing.md`](./documentation/architecture/05-roadmap-and-licensing.md) |
| **Browse the Full Index** | [`documentation/README.md`](./documentation/README.md) |

---

## 2. Build Order

When implementing the system, execute the development phases in this sequence:
1. **Phase 1: Chat Core** — Establish PostgreSQL schemas, Redis cache prefixes, and the WebSocket core gateway.
2. **Phase 2: Storage & Media** — Mount shared volumes, set up headless Jellyfin, and integrate the custom HLS.js streaming player.
3. **Phase 3: Catalog** — Build Grimmory integration, watch directory routines, and e-book reader controls.
4. **Phase 4: Real-Time** — Provision the LiveKit server and hook up the Zustand voice session store.

---

## 3. Hard Constraints Digest

Every implementing agent must strictly comply with these core rules:
- **Secrets:** All credentials must be sourced from `.env` interpolation. Never commit hardcoded secrets.
- **Copyleft Boundary:** Never link or compile Jellyfin or Grimmory code directly into the Telos core gateway. Maintain strict containerized process boundaries.

---

## 4. Environment Inputs

`.env.example` is the canonical inventory of every environment variable the
stack reads; `docker compose --env-file .env.example config --quiet` must
succeed at all times. Production inputs (domain, ACME, database, Redis,
LiveKit, Jellyfin, Grimmory credentials, `STORAGE_PATH`, `TELOS_BOOTSTRAP_TOKEN`)
appear in the file's production section with placeholder values only.
Development-only inputs (`TELOS_ENV=development` plus the loopback-only
`docker-compose.dev.yml` overlay) are listed in the development section and
must never reach a production deployment. Runtime-critical configuration
(`backend/db/migrations/`, `config/`, `docker-compose*.yml`,
`documentation/operations/`, `scripts/`) must remain tracked;
`scripts/verify-clean-checkout.sh --inventory-only` enforces this.

---

## 5. Verification Suite

Run this plain shell verification command block to validate your changes:

```bash
# 1. Search for citations, TO-DOs, or temporary placeholders
grep -rn "ci""te:" documentation/ AGENTS.md ; echo "exit=$?"
grep -rnE "TO""DO|TB""D" documentation/ AGENTS.md ; echo "exit=$?"

# 2. Backend tests (Go is not installed on the host; run in a container)
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./...

# 3. Frontend lint + E2E (E2E needs `npm run dev` running on :3000)
cd frontend && npm run lint && npx playwright test
```

---

## 6. Assets Reference

- Canonical logo renders are located at [`resources/logos/Telos_sun_ink.svg`](./resources/logos/Telos_sun_ink.svg).
- The custom vaporwave logo variant is located at [`resources/logos/Telos_sun_synthwave.svg`](./resources/logos/Telos_sun_synthwave.svg).

