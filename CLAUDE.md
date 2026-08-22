# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Telos Is

A self-hosted, single-origin "digital sovereignty" platform: a community library for commentary on and storage of media. Four modules — **Chat**, **Stream** (Jellyfin media), **Library** (Grimmory e-books + audiobooks), and **Files** — plus Settings. The canonical list is `MODULES` in `frontend/src/components/AppShell.tsx`; each entry is capability-gated (`view_channel` / `view_media` / `view_library` / `view_files`). Commentary (private-by-default, community-shareable, with replies) attaches to any book, media item, or file. A single Go gateway (`backend/`) serves the statically-exported Next.js frontend (`frontend/`) and fronts all bundled services; Traefik routes everything under one domain.

The **less-is-more redesign (2026-07)** removed voice rooms, LiveKit, Watch Parties, the notification inbox, and My List, and generalized annotations to any media target. That redesign also folded Files into Library behind `?view=`, but **Files has since graduated back to its own top-level module** — `/library?view=files` now redirects *to* `/files`, not the other way around (`frontend/src/app/(shell)/library/page.tsx`). Docs describing voice, Watch Parties, the inbox, My List, or Files-under-Library are historical.

Read `AGENTS.md` for hard constraints and the verification suite, and `documentation/README.md` for the normative architecture spec index. Two caveats on those specs: they predate most of the code, and every `documentation/architecture/*.md` file carries a "Superseded in part" banner. Where code and docs disagree, **the code wins** — flag the discrepancy rather than following the doc.

## Repository Map

| Path | What it is |
|---|---|
| `backend/` | Go gateway — ~50 source files, not one (see Architecture below) |
| `frontend/` | Next.js 16 static export; see `frontend/AGENTS.md` |
| `cli/` + `telos` + `install.sh` | The authoritative Bash `./telos` operator CLI (dispatcher + one file per subcommand) |
| `backend/cmd/telos/` | Experimental Go CLI; not an operator entry point |
| `scripts/` | ~38 operational/verification scripts — the real CI gates live here |
| `documentation/` | Normative spec: `architecture/` (5), `operations/` (19 runbooks), `product/` |
| `docs/superpowers/` | Historical design specs + execution plans, one per feature program |
| `deploy/`, `config/` | Service-level config (Postgres init, Traefik routes, etc.) |
| `ci/`, `.github/workflows/` | Pinned toolchains, license policy, phase gates |
| `tests/` | Fixtures, load tests, operations drills |

## Commands

**Go is not installed on the host** — run backend toolchain commands in a container. The host uses **podman** (not docker). Traefik intentionally does not mount the Podman socket.

### Running the stack — the `telos` CLI

The Bash dispatcher `./telos` is the authoritative operator entry point: a thin
dispatcher over one file per subcommand in `cli/commands/`. Adding a command
means adding a file with a `run()` function and a `# summary:` line — nothing
else. `backend/cmd/telos/` is experimental and must not be documented as an
operator path.

```bash
bash install.sh --link-only --user --yes   # symlink into ~/.local/bin, run in place from the checkout
telos doctor                               # diagnostics, non-destructive
telos start --dev --build                  # layers docker-compose.dev.yml -> gateway on 127.0.0.1:8080
telos status                               # boot state per service
telos stop                                 # compose down, volumes preserved
```

`--link-only` runs straight from the checkout, so edits take effect with no reinstall (moving the repo breaks the command). **Never run `telos init --force` on an existing node** — `telos doctor` suggests it to clear `change-me` placeholders, but it regenerates every secret and desynchronizes them from the existing Postgres/MariaDB volumes. Placeholders are intentional for local dev.

Raw compose still works as a fallback: `podman-compose up -d --build` (requires `.env` — copy `.env.example`). Note that `up -d --build` does **not** recreate containers when only the image changed; verify image IDs and use `--force-recreate` when needed.

### Verification gates

```bash
# Backend unit suite (containerized; Go is not on the host)
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...

# Backend integration suite — spins up disposable PG 16.14 + Redis 7 on an isolated
# network and sets TELOS_TEST_DATABASE_URL / TELOS_TEST_REDIS_URL. Integration tests
# self-skip without those, so plain `go test ./...` remains the unit suite.
bash scripts/test-backend.sh                    # everything
bash scripts/test-backend.sh db                 # selector: auth|realtime|security|db|storage|product

# Frontend (from frontend/)
npm run dev            # dev server on :3000, proxies /api/* to the gateway on :8080
npm run build          # static export to frontend/out/
npm run lint
npx tsc --noEmit
npm run test:unit      # vitest + node:test

# Playwright E2E (from frontend/; needs `npm run dev` running first — no webServer in config)
npx playwright test
npx playwright test -g "mobile matrix"

# Repo-level guards
bash scripts/validate-compose.sh --env-file .env
bash scripts/check-product-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only

# Live smoke check
curl http://localhost:8080/api/v1/health        # also /health/live and /health/ready
```

## Architecture

**Single-origin gateway.** `backend/` is a single flat Go package (`module telos-core`) of ~50 source files plus their tests — **not one file**. `main.go` holds the route table (`mux.Handle(...)`, the fastest way to find a handler) and the `//go:embed` directives; the work lives in siblings named for their concern: `auth.go`, `chat.go`, `realtime.go`, `media.go`, `hls.go`, `library.go`, `catalog*.go`, `annotations.go`, `files.go`, `storage.go`, `upload.go`, `migrations.go`, `database.go`, `security.go`, `outbox.go`, `health.go`. When adding a feature, extend the matching file rather than growing `main.go`.

- **Migrations, not a schema file.** `main.go:114` is `//go:embed db/migrations/*.sql`. Schema changes go in `backend/db/migrations/` as a **new numbered file** (`NNNN_description.sql`, currently through `0022`), written idempotently with `IF NOT EXISTS` / `ON CONFLICT`. `migrations.go` is a locked, checksum-verified engine (Discover/Plan/Run/Verify) tracking applied state in `schema_migrations`; **never edit an already-applied migration** — its checksum is verified. The one-shot `telos-migrate` compose service runs `./telos-core migrate` and `telos-core` waits on `service_completed_successfully`. There is no `schema.sql`.
- Embeds the frontend static export via `//go:embed all:out`. **A local `go build` fails unless `backend/out/` exists**; the Dockerfile populates it from the frontend build stage (build context is the repo root, dockerfile `backend/Dockerfile`).
- Chat: `GET /api/v1/chat/ws` — persists messages to Postgres, publishes to Redis pub/sub channel `telos:chat:<channelID>`, which fans out to every connected gateway/client. The **channel** comes from a query param; **user identity comes from the cookie session**, since the route is wrapped in `withAuth(..., "view_channel")` (`main.go:454`). `realtime.go` holds the session registry, bounded single-writer socket, and live revocation.
- Media: `/api/v1/media*` and `/api/v1/stream/*` reverse-proxy Jellyfin (internal hostname `jellyfin:8096`) using `JELLYFIN_ADMIN_TOKEN`. Video streaming resolves PlaybackInfo then redirects to an HLS `.m3u8` sub-path proxied to Jellyfin. Redis caches Jellyfin lookups under `telos:jellyfin:*` keys.
- Commentary: annotations are anchored to `(target_type, target_id)` over `('book','media','file')`. Book highlights use `/api/v1/library/books/{id}/annotations` (locator validated against the Grimmory format); comments on media/files use `/api/v1/media/items/{id}/comments` and `/api/v1/files/{id}/comments` (gated on `view_media` / `view_files`). Edit/delete/reply routes authorize from the row's own target type.
- User events: `GET /api/v1/events` (catch-up) and `GET /api/v1/events/ws` (live) carry mentions and replies over a recipient-scoped, idempotent stream (`RecordUserEvent` in `events.go`). There is no notification-inbox table.

**Explicit degraded behavior.** Upstream failures return 502/503 and appear as `degraded` in `/api/v1/health`; handlers must never fabricate catalog or media records.

**Frontend.** Next.js 16.2.10 static export (`output: "export"` in `next.config.ts`) — no server components at runtime, no API routes. Per-route client components under `frontend/src/app/(shell)/` (`chat`, `stream`, `library`, `files`, `settings`) inside `components/AppShell.tsx`, plus Zustand stores in `frontend/src/stores/` — exactly 11, and this is the complete list: `useAnnotationStore`, `useAuthStore`, `useChatSessionStore`, `useConnectionStore`, `useFilesStore`, `useLibraryStore`, `useMediaStore`, `useMobileNavStore`, `usePreferencesStore`, `useSettingsStore`, `useThemeStore`. (`useConnectionStore` arrived with the backend/client split's S4 connection layer.) Only `useThemeStore` uses the `persist` middleware (`useThemeStore.ts:2,13`); `usePreferencesStore` has a field named `persisted` but does not persist to storage. Shared design tokens (breakpoints 720/900/1100, fluid type, spacing, 44px touch targets, safe-area) live in `styles/tokens/scale.css`. In dev, `frontend/dev-server.mjs` reverse-proxies `/api/*` to the gateway on `:8080`; `apiBase()` (`frontend/src/lib/api.ts:5`) returns `""` unconditionally and does no port detection. Heed `frontend/AGENTS.md`: this Next.js version is newer than training data — consult `node_modules/next/dist/docs/` before nontrivial Next.js work, and never hand-edit `styles/tokens/` or `styles/foundations/` (imported verbatim from the design project).

**Compose topology.** Networks: `telos-ingress` (Traefik-facing), `telos-backend` (internal), `telos-db` (internal), `telos-egress` (controlled outbound). Services: traefik, telos-migrate (one-shot), telos-audiobook-migrate (one-shot), telos-core, postgres, redis, jellyfin, grimmory + grimmory-db (MariaDB), clamav, telos-egress-proxy. Shared media/books volumes mount from `${STORAGE_PATH:-/mnt/storage/shared}`. Overlays: `docker-compose.dev.yml` (loopback-only dev bindings), `.override.yml`, `.test.yml`.

## Hard Constraints (from AGENTS.md)

- All credentials come from `.env` interpolation — never hardcode secrets.
- **Copyleft boundary:** never link or compile Jellyfin or Grimmory code into the Go gateway (they are GPL/AGPL; the gateway targets MIT/Apache-2.0). Integrate over HTTP APIs across container boundaries only.

## Project History

**All four original build-order phases are implemented.** The phase list in `AGENTS.md` §2 is the record of the original sequence, not a to-do list.

1. Chat Core (Postgres schemas, Redis, WS gateway) — done
2. Storage & Media (Jellyfin, HLS player) — done
3. Catalog (Grimmory, e-book reader) — done: facet-filtered catalog, in-app EPUB/PDF readers with per-user progress in Telos Postgres, audiobooks shelf via Jellyfin. Grimmory is reached via admin-credential JWT login, **not** OIDC federation — see `documentation/architecture/03-gateway-and-api.md` §3.
4. Commentary — done: annotations generalized to any media target, private-by-default with community sharing and replies.

Four further programs have since run, each with a design spec and execution plan under `docs/superpowers/` — **read those before assuming a feature is unbuilt**:

| Program | What it did |
|---|---|
| Beta readiness (7 phases, `2026-07-18-beta-readiness-master.md`) | Security/authn hardening, the migration engine + role split, storage/media reliability, controlled egress, ops certification |
| Review remediation (`2026-07-23`) | Post-review fixes |
| Less-is-more redesign (6 phases, `2026-07-27`) | Removed voice/LiveKit/Watch Parties/inbox/My List; design tokens; mobile-first rebuild |
| Member experience (4 phases, `2026-07-31`) | Catalog continuity, normalized library items, Jellyfin stream enrichment, audiobook migration ops |

Current work (branch `installer-cli-and-frontend`) is the `telos` operator CLI and installer. The surviving real-time paths are chat and the recipient-scoped user-event stream.
