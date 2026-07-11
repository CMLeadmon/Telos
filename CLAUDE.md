# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Telos Is

A self-hosted, single-origin "digital sovereignty" platform merging Discord-style chat/voice, Jellyfin-style media streaming, Grimmory-style e-book management, and file management. A single Go gateway (`backend/`) serves the statically-exported Next.js frontend (`frontend/`) and fronts all bundled services; Traefik routes everything under one domain.

Read `AGENTS.md` for the build-order phases and hard constraints, and `documentation/README.md` for the normative architecture spec index. Note: `AGENTS.md` predates the code — the repo is no longer documentation-only.

## Commands

**Go is not installed on the host** — run backend toolchain commands in a container. The host uses **podman** (not docker); the Traefik service mounts the podman socket.

```bash
# Full stack (requires .env — copy .env.example and fill in real values)
podman-compose up -d --build

# Backend tests/vet (from repo root)
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go vet ./...

# Frontend (from frontend/)
npm run dev      # dev server on :3000, talks to gateway on :8080
npm run build    # static export to frontend/out/
npm run lint

# Playwright E2E (from frontend/; needs `npm run dev` running first — no webServer in config)
npx playwright test
npx playwright test -g "Voice signaling"   # single test by name

# Live smoke check
curl http://localhost:8080/api/v1/health
```

## Architecture

**Single-origin gateway.** `backend/main.go` is the entire backend (one file). It:
- Embeds `backend/db/schema.sql` (`//go:embed`) and applies it idempotently at startup — schema changes go in that file, using `IF NOT EXISTS` / `ON CONFLICT` patterns.
- Embeds the frontend static export via `//go:embed all:out`. **A local `go build` fails unless `backend/out/` exists**; the Dockerfile populates it from the frontend build stage (build context is the repo root, dockerfile `backend/Dockerfile`).
- Chat: `GET /api/v1/chat/ws` — persists messages to Postgres, publishes to Redis pub/sub channel `telos:chat:<channelID>`, which fans out to every connected gateway/client. Channel and user identity come from query params.
- Media: `/api/v1/media*` and `/api/v1/stream/*` reverse-proxy Jellyfin (internal hostname `jellyfin:8096`) using `JELLYFIN_ADMIN_TOKEN`. Video streaming resolves PlaybackInfo then redirects to an HLS `.m3u8` sub-path proxied to Jellyfin. Redis caches Jellyfin lookups under `telos:jellyfin:*` keys.
- Voice: `GET /api/v1/voice/token` mints a LiveKit HS256 JWT by hand (no LiveKit SDK) — see `GenerateLiveKitToken` and its test in `main_test.go`.

**Mock fallbacks everywhere.** When Postgres/Redis are down or Jellyfin auth fails, handlers log a warning and serve hardcoded mock data (names suffixed `(Mock)`). A working-looking UI does not prove an integration works — check `podman logs telos-core` for fallback warnings.

**Frontend.** Next.js 16 static export (`output: "export"` in `next.config.ts`) — no server components at runtime, no API routes. Essentially one large client component, `frontend/src/components/CoreAppShell.tsx` (all four modules: Chat/Stream/Books/Files), plus two Zustand stores in `frontend/src/stores/` (`useThemeStore`, `useVoiceSessionStore` wrapping `livekit-client`). In dev, the component detects port 3000 and points API/WS calls at `:8080`. Heed `frontend/AGENTS.md`: this Next.js version is newer than training data — consult `node_modules/next/dist/docs/` before nontrivial Next.js work.

**Compose topology.** Three networks: `telos-ingress` (Traefik-facing), `telos-backend` (internal), `telos-db` (internal). Services: traefik, telos-core, postgres, redis, jellyfin, grimmory + grimmory-db (MariaDB), livekit. Shared media/books volumes mount from `${STORAGE_PATH:-/mnt/storage/shared}`.

## Hard Constraints (from AGENTS.md)

- All credentials come from `.env` interpolation — never hardcode secrets.
- **Copyleft boundary:** never link or compile Jellyfin or Grimmory code into the Go gateway (they are GPL/AGPL; the gateway targets MIT/Apache-2.0). Integrate over HTTP APIs across container boundaries only.

## Build Order (roadmap phases)

1. Chat Core (Postgres schemas, Redis, WS gateway) — implemented
2. Storage & Media (Jellyfin, HLS player) — implemented, Files module UI is still static mock
3. Catalog (Grimmory integration, e-book reader) — not started in backend; Books module UI is static mock
4. Real-Time (LiveKit + voice store) — token minting and client store implemented
