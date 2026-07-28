# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Telos Is

A self-hosted, single-origin "digital sovereignty" platform: a community library for commentary on and storage of media. Three modules — **Library** (Grimmory e-books + a Files browser), **Stream** (Jellyfin media), and **Chat** — plus Settings. Commentary (private-by-default, community-shareable, with replies) attaches to any book, media item, or file. A single Go gateway (`backend/`) serves the statically-exported Next.js frontend (`frontend/`) and fronts all bundled services; Traefik routes everything under one domain.

The **less-is-more redesign (2026-07)** removed voice rooms, LiveKit, Watch Parties, the notification inbox, and My List; folded Files into Library; and generalized annotations to any media target. Anything below describing those removed features is historical.

Read `AGENTS.md` for the build-order phases and hard constraints, and `documentation/README.md` for the normative architecture spec index. Note: `AGENTS.md` predates the code — the repo is no longer documentation-only.

## Commands

**Go is not installed on the host** — run backend toolchain commands in a container. The host uses **podman** (not docker). Traefik intentionally does not mount the Podman socket.

```bash
# Full stack (requires .env — copy .env.example and fill in real values)
podman-compose up -d --build

# Backend tests/vet (from repo root)
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...

# Frontend (from frontend/)
npm run dev      # dev server on :3000, talks to gateway on :8080
npm run build    # static export to frontend/out/
npm run lint

# Playwright E2E (from frontend/; needs `npm run dev` running first — no webServer in config)
npx playwright test
npx playwright test -g "mobile matrix"   # single test group by name

# Live smoke check
curl http://localhost:8080/api/v1/health
```

## Architecture

**Single-origin gateway.** `backend/main.go` is the entire backend (one file). It:
- Embeds `backend/db/schema.sql` (`//go:embed`) and applies it idempotently at startup — schema changes go in that file, using `IF NOT EXISTS` / `ON CONFLICT` patterns.
- Embeds the frontend static export via `//go:embed all:out`. **A local `go build` fails unless `backend/out/` exists**; the Dockerfile populates it from the frontend build stage (build context is the repo root, dockerfile `backend/Dockerfile`).
- Chat: `GET /api/v1/chat/ws` — persists messages to Postgres, publishes to Redis pub/sub channel `telos:chat:<channelID>`, which fans out to every connected gateway/client. Channel and user identity come from query params.
- Media: `/api/v1/media*` and `/api/v1/stream/*` reverse-proxy Jellyfin (internal hostname `jellyfin:8096`) using `JELLYFIN_ADMIN_TOKEN`. Video streaming resolves PlaybackInfo then redirects to an HLS `.m3u8` sub-path proxied to Jellyfin. Redis caches Jellyfin lookups under `telos:jellyfin:*` keys.
- Commentary: annotations are anchored to `(target_type, target_id)` over `('book','media','file')`. Book highlights use `/api/v1/library/books/{id}/annotations` (locator validated against the Grimmory format); comments on media/files use `/api/v1/media/items/{id}/comments` and `/api/v1/files/{id}/comments` (gated on `view_media` / `view_files`). Edit/delete/reply routes authorize from the row's own target type.
- User events: `GET /api/v1/events` (catch-up) and `GET /api/v1/events/ws` (live) carry mentions and replies over a recipient-scoped, idempotent stream (`RecordUserEvent` in `events.go`). There is no notification-inbox table.

**Explicit degraded behavior.** Upstream failures return 502/503 and appear as `degraded` in `/api/v1/health`; handlers must never fabricate catalog or media records.

**Frontend.** Next.js 16 static export (`output: "export"` in `next.config.ts`) — no server components at runtime, no API routes. Per-route client components under `frontend/src/app/(shell)/` (chat, stream, library, settings; `/files` redirects to `/library?view=files`) inside `components/AppShell.tsx`, plus Zustand stores in `frontend/src/stores/` (`useThemeStore`, `usePreferencesStore`, `useChatSessionStore`, `useAnnotationStore`, etc.). Shared design tokens (breakpoints 720/900/1100, fluid type, spacing, 44px touch targets, safe-area) live in `styles/tokens/scale.css`. In dev, the app detects port 3000 and points API/WS calls at `:8080`. Heed `frontend/AGENTS.md`: this Next.js version is newer than training data — consult `node_modules/next/dist/docs/` before nontrivial Next.js work.

**Compose topology.** Networks: `telos-ingress` (Traefik-facing), `telos-backend` (internal), `telos-db` (internal), `telos-egress` (controlled outbound). Services: traefik, telos-migrate (one-shot), telos-core, postgres, redis, jellyfin, grimmory + grimmory-db (MariaDB), clamav, telos-egress-proxy. Shared media/books volumes mount from `${STORAGE_PATH:-/mnt/storage/shared}`.

## Hard Constraints (from AGENTS.md)

- All credentials come from `.env` interpolation — never hardcode secrets.
- **Copyleft boundary:** never link or compile Jellyfin or Grimmory code into the Go gateway (they are GPL/AGPL; the gateway targets MIT/Apache-2.0). Integrate over HTTP APIs across container boundaries only.

## Build Order (roadmap phases)

1. Chat Core (Postgres schemas, Redis, WS gateway) — implemented
2. Storage & Media (Jellyfin, HLS player) — implemented; the Files browser now lives under Library (`?view=files`)
3. Catalog (Grimmory integration, e-book reader) — implemented: catalog with facet filtering, in-app EPUB/PDF readers with per-user progress stored in Telos Postgres, audiobooks shelf via Jellyfin. Grimmory is reached via admin-credential JWT login, not OIDC federation — see `documentation/architecture/03-gateway-and-api.md` §3.
4. Commentary — implemented: annotations generalized to any media target (book highlights + comments on media/files), private-by-default with community sharing and replies.

The Phase 1–4 real-time voice/Watch Party layer was removed in the less-is-more redesign; the surviving real-time paths are chat and the recipient-scoped user-event stream.
