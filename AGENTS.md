# Telos — Agent Guide

This repository contains the Telos platform: the Go core gateway (`backend/`), the Next.js client (`frontend/`), the container orchestration (`docker-compose.yml`), and the normative specification (`documentation/`). The docs remain the buildable specification; where code and docs disagree, flag the discrepancy. The project is guided by two core slogans: "your server, your community" (community-facing) and "be on the net, but not of the net" (philosophical/technical).

> **Note (less-is-more redesign, 2026-07):** Telos is a community library for commentary on and storage of media — **four modules (Chat, Stream, Library, Files) plus Settings**. Voice rooms, LiveKit, Watch Parties, the notification inbox, and My List were removed; annotations were generalized to commentary on any media target. The redesign temporarily folded Files into Library, but Files has since graduated back to its own top-level module. Descriptions of removed features below are historical.

> **Start with `CLAUDE.md`.** It is maintained against the running code and describes the repository layout, the `telos` CLI, the migration engine, and what is already built. This file covers the durable constraints and the verification suite.

> **Standalone beta Phase 1:** Production hosted-browser delivery is blocked until Phase 2 adds `telos-client`. The default backend build is headless and does not require `backend/out/` or a build tag.

---

## 1. Read This First

Refer to this matrix to find the appropriate entry point for your development intent:

| Intent | Entry Point |
|---|---|
| **Work on the code (any module)** | [`CLAUDE.md`](./CLAUDE.md) — repo map, commands, current architecture |
| **Work on the frontend** | [`frontend/AGENTS.md`](./frontend/AGENTS.md) — design-system and Next.js rules |
| **Operate or install a node** | `./telos help`, then [`documentation/operations/install.md`](./documentation/operations/install.md) |
| **Deploy or Manage Infrastructure** | [`documentation/architecture/01-system-overview.md`](./documentation/architecture/01-system-overview.md) through [`documentation/architecture/03-gateway-and-api.md`](./documentation/architecture/03-gateway-and-api.md) |
| **Manage Frontend State** | [`documentation/architecture/04-frontend-architecture.md`](./documentation/architecture/04-frontend-architecture.md) *(its voice/LiveKit sections are removed features)* |
| **Understand the Roadmap & Licensing** | [`documentation/architecture/05-roadmap-and-licensing.md`](./documentation/architecture/05-roadmap-and-licensing.md) |
| **Run an operations procedure** | [`documentation/operations/`](./documentation/operations/) — 19 runbooks (backup, restore, upgrade, incidents, capacity…) |
| **See why something was built this way** | [`docs/superpowers/specs/`](./docs/superpowers/specs/) and [`docs/superpowers/plans/`](./docs/superpowers/plans/) |
| **Browse the Full Index** | [`documentation/README.md`](./documentation/README.md) |

---

## 2. Build Order — complete

**All four phases are implemented.** This section is kept as the record of the original sequence; do not read it as a to-do list.

1. **Phase 1: Chat Core** — PostgreSQL schemas, Redis cache prefixes, WebSocket core gateway. ✅
2. **Phase 2: Storage & Media** — Shared volumes, headless Jellyfin, custom HLS.js player. ✅
3. **Phase 3: Catalog** — Grimmory integration, watch directory routines, e-book reader controls. ✅
4. **Phase 4: Commentary** — Annotations generalized to any media target, commentary on media items and files. ✅ (The original Phase 4 real-time voice layer was removed in the less-is-more redesign.)

Four further programs have run since — beta readiness, review remediation, the less-is-more redesign, and member experience — followed by the current `telos` CLI/installer work. See `CLAUDE.md` § Project History and the specs and plans in `docs/superpowers/`.

---

## 3. Hard Constraints Digest

Every implementing agent — human, Claude, or delegated — must strictly comply with these
core rules. They are ordered by how expensive the violation is to undo.

- **Secrets:** All credentials must be sourced from `.env` interpolation. Never commit hardcoded secrets.
- **Copyleft Boundary:** Never link or compile Jellyfin or Grimmory code directly into the Telos core gateway. Maintain strict containerized process boundaries.
- **Applied migrations are immutable.** `backend/migrations.go` verifies a checksum for every
  row in `schema_migrations`. Editing an already-applied file in `backend/db/migrations/`
  breaks every existing deployment. Schema changes are always a **new** numbered file
  (`NNNN_description.sql`, currently through `0023`), written idempotently with
  `IF NOT EXISTS` / `ON CONFLICT`. There is no `schema.sql`.
- **Never run `telos init --force` on an existing node.** `telos doctor` suggests it to clear
  `change-me` placeholders, but it regenerates every secret and desynchronizes them from the
  existing Postgres/MariaDB volumes. Placeholders are intentional in local dev.
- **Never hand-edit `frontend/src/styles/tokens/` or `frontend/src/styles/foundations/`.**
  They are imported verbatim from the design project; changes belong upstream.
- **Never alter the environment to make a gate pass.** Do not patch installed packages, stub
  missing dependencies, edit lockfiles, or relax a test to turn a check green. If a gate
  fails, report the failure with its output. A forced pass is worse than a red build because
  it destroys the signal everyone downstream depends on.

---

## 4. Environment Inputs

`.env.example` is the canonical inventory of every environment variable the
stack reads; `docker compose --env-file .env.example config --quiet` must
succeed at all times. Production inputs (domain, ACME, database, Redis,
Jellyfin, Grimmory credentials, `STORAGE_PATH`, `TELOS_BOOTSTRAP_TOKEN`)
appear in the file's production section with placeholder values only.
Development-only inputs (`TELOS_ENV=development` plus the loopback-only
`docker-compose.dev.yml` overlay) are listed in the development section and
must never reach a production deployment. Runtime-critical configuration
(`backend/db/migrations/`, `config/`, `docker-compose*.yml`,
`documentation/operations/`, `scripts/`) must remain tracked;
`scripts/verify-clean-checkout.sh --inventory-only` enforces this.

---

## 5. Verification Suite

Run the gates that match what you touched. Go is not installed on the host — backend commands run in a container, via **podman**.

```bash
# Backend — unit suite (integration tests self-skip without a live PG/Redis)
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...

# Backend — integration suite (spins up disposable PG 16.14 + Redis 7, isolated network)
bash scripts/test-backend.sh                 # all
bash scripts/test-backend.sh db              # auth|realtime|security|db|storage|product

# Frontend (from frontend/; E2E needs `npm run dev` already running on :3000)
npm run lint && npx tsc --noEmit && npm run test:unit && npm run build
npx playwright test

# Repo-level guards
bash scripts/validate-compose.sh --env-file .env
bash scripts/check-product-truth.sh          # no claims about unbuilt features
bash scripts/check-release-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only

# Documentation hygiene (must return nothing)
grep -rn "ci""te:" documentation/ AGENTS.md
grep -rnE "TO""DO|TB""D" documentation/ AGENTS.md
```

`scripts/` holds ~38 further operational scripts (backup, restore, upgrade, rollback, drills, evidence signing). Read the script header before running one — several are destructive.

---

## 6. Working Conventions

Things that are not obvious from the tree and that cost real time to rediscover.

- **The host runs podman, not docker.** Use `podman` / `podman-compose`. Go is not installed
  on the host; backend toolchain commands run in `docker.io/library/golang:1.26.5` per §5.
- **`backend/` is one flat Go package (`module telos-core`) of ~50 files, not one file.**
  `main.go` holds the route table (`mux.Handle(...)`) and the `//go:embed` directives; the
  work lives in siblings named for their concern (`auth.go`, `chat.go`, `realtime.go`,
  `media.go`, `library.go`, `annotations.go`, `files.go`, `security.go`, …). Extend the
  matching sibling rather than growing `main.go`.
- **The default backend build is headless.** It does not require `backend/out/` or a build
  tag. The opt-in `embedfrontend` variant embeds a prepared static export, but it is not the
  Phase 1 production delivery path. Production hosted-browser delivery is blocked until
  Phase 2 adds `telos-client`.
- **`podman-compose up -d --build` does not recreate containers when only the image changed.**
  Verify image IDs and use `--force-recreate` when needed. Prefer a full `down`/`up` over a
  single-service `--force-recreate`, which breaks `telos-backend` DNS resolution.
- **Where code and the `documentation/` spec disagree, the code wins.** Every
  `documentation/architecture/*.md` file carries a "Superseded in part" banner and predates
  most of the implementation. Flag the discrepancy; do not follow the doc.
- **Design docs go to `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md`; execution plans
  go to `docs/superpowers/plans/`.** Read a sibling before writing a new one — the house
  format is specific and consistent.
- **Cite `file.go:line` for every claim about the code.** Assertions about call sites, route
  counts, or behavior must be greppable. An unverifiable claim in a spec is a defect.

---

## 7. Assets Reference

- Canonical logo renders are located at [`resources/logos/Telos_sun_ink.svg`](./resources/logos/Telos_sun_ink.svg).
- The custom vaporwave logo variant is located at [`resources/logos/Telos_sun_synthwave.svg`](./resources/logos/Telos_sun_synthwave.svg).
