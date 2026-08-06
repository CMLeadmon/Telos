# Headless Backend & API Contract — Design

**Date:** 2026-08-06
**Status:** Approved design
**Sub-project ID:** S1
**Parent program:** `docs/superpowers/specs/2026-08-06-backend-client-split-program.md`

This sub-project makes the Go core gateway (`backend/`) buildable and runnable as a standalone headless service without requiring built frontend static assets (`frontend/out/`). It establishes an automated, committed OpenAPI 3.1 specification generated directly from the Go route table in `backend/main.go:402-549`, and generates a type-safe TypeScript client for frontend builds. This decouples backend deployment from web UI hosting and unblocks independent client release cadences.

## 1. Purpose

### 1.1 Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Default build target | **Headless by default**, embedding is opt-in | Enables containerized backend deployment without compiling or embedding frontend assets. Opt-in via `//go:build embedfrontend`. |
| Embed abstraction | Tagged file pair (`frontend_embed.go` vs `frontend_noembed.go`) | Encapsulates `//go:embed all:out` and `fs.Sub` inside build-tagged files exposing a unified `registerFrontendHandler(mux)` interface. |
| Theme middleware location | Gated behind `embedfrontend` build tag | `backend/frontend_theme.go` is same-origin static HTML rewrite machinery only; it has zero utility in headless mode. |
| Dockerfile build parameter | Build ARG `EMBED_FRONTEND` (default `false`) | Allows building both headless (`telos-core`) and bundled single-binary images from `backend/Dockerfile`. |
| OpenAPI specification source | Generated from route table, committed, CI-checked | `backend/main.go` route registrations are the single source of truth. Hand-maintained specs drift. CI fails if committed spec is stale. |
| Frontend client generation | Auto-generated TypeScript client from OpenAPI spec | Replaces manual `fetch` signatures with type-checked SDK contracts. CI verifies frontend typechecks without a running backend. |
| Headless CI boundary gate | Build backend with `frontend/` directory deleted | Guarantees zero compile-time or runtime build dependency on frontend assets when running headless. |
| ThemeSync cookie gating | Gate `telos_theme` cookie write on web origin | `ThemeSync.tsx` cookie writing is relevant only when loaded from the gateway origin. Native apps ignore theme pre-stamping. |
| Handler semantics scope | **OUT OF SCOPE**: Zero handler changes | No API endpoint signatures, database schemas, authorization rules, or response payloads are altered in S1. |

### 1.2 Success criteria

1. Executing `go build ./...` inside `backend/` succeeds cleanly without requiring a `backend/out/` directory or frontend static build artifacts.
2. Building with `go build -tags embedfrontend ./...` embeds `out/` assets and serves the static frontend with theme pre-stamping on `GET /`.
3. An automated generator produces a complete OpenAPI 3.1 JSON document (`documentation/api/openapi.json`) reflecting all 108 registered routes in `backend/main.go:402-549`.
4. CI gate `check-api-contract-drift` fails if modifications to backend handlers or routes are not accompanied by an updated committed `openapi.json`.
5. Frontend CI gate `typecheck-generated-client` confirms `npx tsc --noEmit` passes against the generated API client without connecting to a live backend container.

## 2. Verified current state

### 2.1 Frontend Embed Coupling Sites

| File & Line | Code Element | Verified Coupling & Current Behavior |
|---|---|---|
| `backend/main.go:117-118` | `//go:embed all:out`<br>`var frontendFS embed.FS` | Hardcoded embed directive that fails compilation if `backend/out` folder does not exist. |
| `backend/main.go:392-396` | `subFS, err := fs.Sub(frontendFS, "out")`<br>`if err != nil { log.Fatalf(...) }` | Fatal process crash at startup if `out/` is missing, blocking headless operation. |
| `backend/main.go:549` | `mux.Handle("/", themedFrontend(fileServer))` | Unconditional root catch-all handler binding the static file server to the HTTP mux. |
| `backend/frontend_theme.go` | `themedFrontend` handler & helper functions | 98 lines of same-origin HTML document buffering and `data-theme` attribute rewriting. |
| `frontend/src/components/ThemeSync.tsx:21-24` | `document.cookie = telos_theme=...` | Client-side cookie setter executed on hydration to inform gateway first-paint theme rendering. |
| `backend/Dockerfile:17-18` | `COPY --from=frontend /fe/out ./out`<br>`RUN CGO_ENABLED=0 ... go build` | Docker build forces multi-stage frontend compilation before building Go binary. |

### 2.2 Route Table Inventory

The Go core gateway registers 108 API routes and 1 root catch-all handler in `backend/main.go:402-549`:

| Route Group | Total Routes | Handler Location Range | Group Description |
|---|---|---|---|
| `library` | 27 | `backend/main.go:491-510` | Grimmory book catalog, covers, content streams, progress, metadata, and annotations. |
| `admin` | 16 | `backend/main.go:431-451` | User administration, invite management, channel management, role/permission matrices. |
| `channels` | 15 | `backend/main.go:455-471` | Chat channels, message posts, thread replies, read states, reactions, pinned messages. |
| `media` | 12 | `backend/main.go:476-485` | Jellyfin video/audio catalog, cover images, playback info, continue/recent shelves, comments. |
| `users` | 11 | `backend/main.go:415-428` | Profile settings, user preferences, password changes, active session revocation, avatars. |
| `files` | 8 | `backend/main.go:513-520` | File uploads, book imports, binary downloads, audit logs, file commentary. |
| `auth` | 6 | `backend/main.go:405-412` | Bootstrap, authentication login/logout, invite acceptance, session inspection. |
| `health` | 3 | `backend/main.go:402-404` | Public liveness (`/health/live`), readiness (`/health/ready`), and general health. |
| `stream` | 3 | `backend/main.go:486-488` | Direct audio streaming, video streaming, and subpath video segment delivery. |
| `folders` | 2 | `backend/main.go:518-519` | Directory folder creation and deletion operations. |
| `events` | 2 | `backend/main.go:523-524` | Event stream catch-up endpoint and real-time event WebSocket connection. |
| `search` | 1 | `backend/main.go:473` | Universal search endpoint across channels, messages, books, and media. |
| `hls` | 1 | `backend/main.go:489` | Session-bound HLS manifest and segment proxy delivery. |
| `chat` | 1 | `backend/main.go:454` | Primary real-time chat WebSocket gateway connection. |
| **Total API** | **108** | — | **All routes scoped under `/api/v1/`.** |
| `catch-all` | 1 | `backend/main.go:549` | Root HTTP handler serving static embedded Next.js assets (`/`). |

### 2.3 Binary, Streaming, and WebSocket Endpoints

Among the 108 API routes, 17 endpoints require binary proxying, range streaming, or WebSocket protocols rather than JSON responses:

| Method & Route Pattern | Line Reference | Content / Protocol Type |
|---|---|---|
| `GET /api/v1/users/{id}/avatar` | `backend/main.go:428` | Image binary bytes (`image/png`, `image/jpeg`) |
| `POST /api/v1/users/me/avatar` | `backend/main.go:426` | Multipart binary avatar upload |
| `GET /api/v1/media/items/{id}/cover` | `backend/main.go:481` | Image binary bytes from Jellyfin |
| `GET /api/v1/stream/audio/{id}` | `backend/main.go:486` | Audio binary Range stream |
| `GET /api/v1/stream/video/{id}` | `backend/main.go:487` | Video binary Range stream / HLS 302 redirect |
| `GET /api/v1/stream/video/{id}/{path...}` | `backend/main.go:488` | TS/M4S video segment binary streams |
| `GET /api/v1/hls/{locator}` | `backend/main.go:489` | Rewritten HLS playlist/segment stream |
| `GET /api/v1/library/books/{id}/cover` | `backend/main.go:500` | Image binary bytes from Grimmory |
| `GET /api/v1/library/books/{id}/content` | `backend/main.go:501` | EPUB/PDF book binary content stream |
| `GET /api/v1/library/audiobooks/{id}/stream` | `backend/main.go:503` | Grimmory audiobook binary Range stream |
| `GET /api/v1/library/audiobooks/{id}/tracks/{index}/stream` | `backend/main.go:504` | Grimmory audiobook track Range stream |
| `PUT /api/v1/library/books/{id}/cover` | `backend/main.go:509` | Custom book cover binary upload |
| `POST /api/v1/files` | `backend/main.go:514` | General file multipart binary upload |
| `POST /api/v1/files/books` | `backend/main.go:515` | Book file multipart binary upload |
| `GET /api/v1/files/{id}/download` | `backend/main.go:516` | Arbitrary file binary download stream |
| `GET /api/v1/chat/ws` | `backend/main.go:454` | Real-time Chat WebSocket protocol |
| `GET /api/v1/events/ws` | `backend/main.go:524` | Real-time Events WebSocket protocol |

## 3. Architecture

### 3.1 Boundaries

Sub-project S1 separates frontend asset embedding from backend compilation. S1 enforces that `backend/` compiles cleanly with no `out/` directory present. S1 introduces code generation utilities to parse Go route registrations and emit OpenAPI definitions, but strictly forbids altering existing route paths, HTTP methods, authorization middleware parameters, or JSON response structs.

### 3.2 Tagged Embed Architecture

The static frontend handler is refactored into build-tagged compilation units:

```
                            +--------------------------+
                            |     backend/main.go      |
                            |                          |
                            |  registerFrontend(mux)   |
                            +------------+-------------+
                                         |
                       +-----------------+-----------------+
                       |                                   |
         [Build Tag: embedfrontend]              [Build Tag: !embedfrontend]
                       |                                   |
                       v                                   v
         +---------------------------+       +---------------------------+
         | backend/frontend_embed.go |       |backend/frontend_noembed.go|
         |                           |       |                           |
         | //go:embed all:out        |       | func registerFrontend(...) |
         | var frontendFS embed.FS   |       |   // No-op implementation |
         |                           |       |   // Headless mode        |
         | Serves static assets &    |       +---------------------------+
         | wraps themedFrontend      |
         +---------------------------+
                       |
                       v
         +---------------------------+
         |backend/frontend_theme.go  |
         | //go:build embedfrontend  |
         +---------------------------+
```

1. **`backend/frontend_embed.go`** (`//go:build embedfrontend`): Contains `//go:embed all:out` and `var frontendFS embed.FS`. Performs `fs.Sub(frontendFS, "out")` safely and registers `mux.Handle("/", themedFrontend(fileServer))`.
2. **`backend/frontend_noembed.go`** (`//go:build !embedfrontend`): Exposes signature `func registerFrontend(mux *http.ServeMux)` with a no-op body.
3. **`backend/frontend_theme.go`**: Prepended with build constraint `//go:build embedfrontend`.

### 3.3 OpenAPI Contract Generation Pipeline

OpenAPI 3.1 generation is fully automated from the Go backend source code to prevent specification drift:

```
+------------------------+        +---------------------------+        +---------------------------+
|   backend/main.go      | ---->  | scripts/gen-openapi.go    | ---->  | documentation/api/        |
| (Route Registrations)  |        | (Parses Go Mux & AST)     |        |   openapi.json            |
+------------------------+        +---------------------------+        +-------------+-------------+
                                                                                     |
                                                                                     v
                                                                       +---------------------------+
                                                                       | npx openapi-typescript    |
                                                                       +-------------+-------------+
                                                                                     |
                                                                                     v
                                                                       +---------------------------+
                                                                       | frontend/src/lib/         |
                                                                       |   api-client.ts           |
                                                                       +---------------------------+
```

1. Generator script `scripts/gen-openapi.go` inspects route registrations in `backend/main.go` and handler signatures.
2. Generated OpenAPI 3.1 document is written to `documentation/api/openapi.json` and committed to the repository.
3. Frontend uses `openapi-typescript` to convert `openapi.json` into type definitions (`frontend/src/lib/api-client.ts`).

## 4. Contract changes

### 4.1 Build Tags & Flags

- **New Build Tag**: `embedfrontend` enables embedding static assets into the Go binary. Default `go build ./...` operates in headless mode without embedding.
- **Dockerfile Build ARG**: `EMBED_FRONTEND` added to `backend/Dockerfile:17`. Default value is `false`.

### 4.2 API Contract Artifacts

- **Committed API Specification**: `documentation/api/openapi.json` (OpenAPI 3.1 format describing all 108 routes).
- **TypeScript Type Definitions**: `frontend/src/lib/api-client.ts` generated from `openapi.json`.

## 5. Implementation surface

| File Path | Change | Rationale |
|---|---|---|
| `backend/frontend_embed.go` | Create file with `//go:build embedfrontend` tag containing `frontendFS` embed and static handler registration | Isolates `//go:embed` asset requirement behind explicit opt-in build tag. |
| `backend/frontend_noembed.go` | Create file with `//go:build !embedfrontend` tag containing no-op `registerFrontend(mux)` | Provides stub interface for default headless Go builds. |
| `backend/frontend_theme.go` | Add `//go:build embedfrontend` constraint header at line 1 | Prevents compilation of same-origin theme cookie middleware during headless builds. |
| `backend/main.go` | Remove lines 117-118, replace lines 392-396 and 549 with `registerFrontend(mux)` call | Strips hardcoded embed directives and delegates frontend mounting to tagged files. |
| `backend/Dockerfile` | Add `ARG EMBED_FRONTEND=false` and conditional frontend build stage execution | Allows container builds to produce lightweight headless binaries by default. |
| `scripts/gen-openapi.go` | Create OpenAPI 3.1 generator script | Automatically extracts route schemas from Go mux registrations. |
| `.github/workflows/ci.yml` | Add `check-headless-build` and `check-api-contract-drift` jobs | Enforces headless backend independence and committed OpenAPI freshness in CI. |

## 6. Failure handling and observability

1. **Missing Frontend Assets**: When `embedfrontend` build tag is active but `backend/out/` is missing, `frontend_embed.go` returns a descriptive compilation error at build time rather than crashing at runtime via `log.Fatalf`.
2. **OpenAPI Drift Detection**: In CI, `scripts/gen-openapi.go` generates a temporary spec file and performs `diff -u` against `documentation/api/openapi.json`. If diff is non-empty, CI exits with code `1` and prints instructions to run `go run scripts/gen-openapi.go`.

## 7. Security and privacy

1. **Origin Verification Intact**: Headless mode retains `requireTrustedOrigin` middleware (`backend/main.go:560`) and `SameSite=Strict` session cookies (`backend/main.go:1031-1039`). Headless operation does not bypass security headers or CORS controls.
2. **Zero Metadata Leakage in OpenAPI**: The generated OpenAPI specification includes route paths, query parameters, request body schemas, and HTTP status codes, but excludes internal filesystem paths, database connection strings, or service secrets.

## 8. Verification

Verification requires running unit tests, headless compilation checks, and contract drift validations:

```bash
# Backend — unit suite and static analysis (headless default)
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...

# Backend — embedded frontend build verification
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -tags embedfrontend ./...

# Validate headless compilation with frontend/ directory deleted/isolated
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go build -v -o /tmp/telos-core-headless .

# OpenAPI generation and drift check
podman run --rm -v ./:/app:z -w /app docker.io/library/golang:1.26.5 go run scripts/gen-openapi.go --check

# Frontend — typecheck against generated API client
cd frontend
npm run lint && npx tsc --noEmit && npm run test:unit

# Repository truth and compose validation
bash scripts/validate-compose.sh --env-file .env
bash scripts/check-product-truth.sh
```

## 9. Documentation changes required with implementation

1. **OpenAPI Reference**: Publish generated `documentation/api/openapi.json` to the repo and add a reference guide in `documentation/architecture/03-gateway-and-api.md`.
2. **Build Documentation Update**: Update `CLAUDE.md` and `documentation/operations/` build instructions documenting the `embedfrontend` build tag requirement for all-in-one single binary builds.

## 10. Deferred / out of scope

1. **Handler Semantics Modifications**: Handler parameter parsing, business logic, DB queries, and error formats remain unchanged in S1.
2. **Cross-Origin Authentication**: S1 does not implement bearer device token authentication or CORS allowlists for remote clients (deferred to `docs/superpowers/specs/2026-08-06-s2-device-token-auth-design.md`).
