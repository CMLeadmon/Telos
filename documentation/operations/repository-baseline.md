# Repository Baseline Classification

Task: P1-T1 (`chore: classify beta release workspace`)
Baseline commit: `1c556e0b029b3836cc20bbe7e9ecc241c032c531` (branch `docs-rewrite`)
Date: 2026-07-19
Owner of all pre-existing work: Carter (repository owner)

Every path that was modified, deleted, or untracked at the baseline commit is
recorded below under exactly one disposition:

- **release input** — required by a clean checkout to build, boot, or operate.
- **reviewed application change** — pre-existing source/configuration work that
  is preserved and will be committed with the release inputs.
- **generated output** — build or test products; ignored, never committed.
- **local secret/state** — machine-local credentials or environment; ignored.
- **deferred non-runtime artifact** — intentionally left untracked for now;
  not required at runtime.

## 1. Release inputs (untracked at baseline; tracked in P1-T2)

| Path | Notes |
|---|---|
| `backend/db/migrations/0004_settings.sql` | Applied migration; bytes preserved as applied. |
| `backend/db/migrations/0007_manage_library.sql` | Applied migration; bytes preserved as applied. |
| `config/dynamic/routes.yaml` | Production Traefik dynamic route provider (replaces removed `certificates.yaml`). |
| `docker-compose.dev.yml` | Development overlay; loopback-only bindings. |
| `documentation/operations/backup-and-restore.md` | Operations doc; recovery mechanics marked for Phase 3 hardening. |
| `scripts/backup.sh` | Backup script; writes to `backups/` (ignored). |
| `scripts/restore.sh` | Restore script. |

## 2. Reviewed application changes (modified tracked files)

All preserved byte-for-byte; none discarded or reformatted.

| Path | Notes |
|---|---|
| `.env.example` | Documents new environment inputs. |
| `CLAUDE.md` | Agent guidance update. |
| `README.md` | Product/docs rewrite in progress. |
| `backend/library.go`, `backend/library_test.go` | Library/book management changes. |
| `backend/main.go`, `backend/main_test.go` | Gateway changes. |
| `backend/settings.go`, `backend/settings_test.go` | Settings module. |
| `config/traefik.yaml` | Static Traefik config update. |
| `docker-compose.yml` | Compose topology update. |
| `documentation/README.md`, `documentation/architecture/01-system-overview.md`, `02-deployment.md`, `03-gateway-and-api.md` | Normative doc updates. |
| `frontend/AGENTS.md` | Frontend agent guidance. |
| `frontend/e2e/chat.spec.ts` | E2E update. |
| `frontend/next.config.ts`, `frontend/package.json`, `frontend/package-lock.json` | Build config/dependency updates. |
| `frontend/src/app/(shell)/chat/page.tsx`, `library/page.tsx`, `stream/page.tsx`, `frontend/src/app/login/page.tsx` | Route components. |
| `frontend/src/components/AppShell.tsx`, `VaporwaveScene.tsx`, `chat/ChatMessage.tsx` | Components. |
| `frontend/src/lib/api.ts`, `frontend/src/lib/voiceAudio.ts` | Client libraries. |
| `frontend/src/stores/useAuthStore.ts`, `useChatSessionStore.ts`, `useLibraryStore.ts`, `useMediaStore.ts`, `useVoiceSessionStore.ts` | Zustand stores. |
| `frontend/src/styles/app.css`, `chat.css`, `foundations/shell.css`, `library.css`, `stream.css` | Styles. |

### Deleted tracked files (intentional)

| Path | Notes |
|---|---|
| `config/dynamic/certificates.yaml` | Superseded by `config/dynamic/routes.yaml`. |
| `resources/Telos_1.png`, `Telos_2.png`, `Telos_3.png`, `Telos_V.png` | Moved/reworked under `resources/logos/` (untracked, classified below). |

### Untracked reviewed application changes

| Path | Notes |
|---|---|
| `frontend/dev-server.mjs` | Development HTTPS/dev server helper. |
| `frontend/e2e/library-manage.spec.ts`, `login.spec.ts`, `stream-refresh.spec.ts`, `voice-secure-context.spec.ts` | E2E suites. |
| `frontend/src/components/chat/ShareCard.tsx`, `SharePicker.tsx` | Chat share components. |
| `frontend/src/components/library/BookManageModal.tsx` | Library management modal. |
| `frontend/src/components/settings/AdminInvitesSection.tsx`, `AdminRolesSection.tsx`, `AdminUsersSection.tsx`, `AppearanceSection.tsx`, `ProfileSection.tsx`, `SecuritySection.tsx` | Settings sections. |
| `frontend/src/lib/voiceAudio.test.mjs` | Unit test. |
| `frontend/src/styles/settings.css` | Settings styles. |
| `resources/logos/Telos_1.png`, `Telos_2.png`, `Telos_3.png`, `Telos_black.png`, `Telos_black_alt.png`, `Telos_vaporwave_transparent.png` | Reorganized logo assets. |

## 3. Generated output (ignored; removed where present)

| Path | Notes |
|---|---|
| `backend/telos-core` | Compiled Go binary; deleted and ignored. |
| `backend/out/` | Embedded frontend export copy; already ignored (`out/`). |
| `frontend/.next/`, `frontend/out/` | Next.js build/export output; already ignored. |
| `frontend/test-results/`, `frontend/playwright-report/` | Test reports; already ignored. |
| `node_modules/` | Dependency install output; already ignored. |

## 4. Local secret/state (ignored; never committed)

| Path | Notes |
|---|---|
| `.env` | Live environment values; already ignored. |
| `config/certs/` | Local certificates; already ignored. |
| `backups/` | Backup output default destination; ignore rule added in P1-T1. |

## 5. Removed prototype data

| Path | Notes |
|---|---|
| `backend/db/legacy-prototype-backup-20260711.sql` | Tracked `pg_dump` containing live prototype users, channels, and messages; removed from source control in P1-T1. No other tracked file contains a database dump. |

## 6. Deferred non-runtime artifacts (classified, intentionally untracked for now)

| Path | Notes |
|---|---|
| `docs/superpowers/plans/*.md` | Implementation plans (including the beta-readiness master and phase plans). Documentation only; may be committed as docs outside the release-input manifest. |
| `docs/superpowers/specs/2026-07-17-voice-secure-context-design.md` | Historical design doc. |
| `docs/voice-turn-setup-guide.md` | Operator setup notes. |
