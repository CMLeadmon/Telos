# Member Experience Phase 4: Audiobook Migration and Operations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate existing Jellyfin audiobooks to Grimmory without breaking stable links, progress, commentary, or chat shares, then make the new ownership model normative and operable.

**Architecture:** Add a maintenance-only `telos-core audiobook-migrate` command and profiled one-shot Compose service. The command inventories and hashes source files, copies them into bookdrop, verifies imported Grimmory files, and performs an item-scoped transactional source/surface/reference switch; cleanup is separately confirmed and backup-gated.

**Tech Stack:** Go 1.26.5, PostgreSQL/pgx, Jellyfin 10.11.11 API, Grimmory 3.2.4 API, SHA-256, existing Telos storage confinement and maintenance lock, Docker Compose/Podman, shell operations tests.

## Global Constraints

- Phases 1–3 are complete and green before any live audiobook inventory or file operation.
- Inventory, copy, verify, switch, rollback, and cleanup are separate explicit commands. No command advances automatically to the next stage.
- Never move or delete a source during inventory, copy, import, verify, or switch.
- Automatic matching requires exact SHA-256 and byte-size equality. Title, author, duration, filename, or path similarity alone cannot authorize a switch.
- A switch is item-scoped and transactional: active source, surface, annotation target type, and resolvable chat embed kind change together.
- The stable catalog ID, progress row, annotation target ID, and chat embed ref never change during switch or rollback.
- Cleanup requires a successful verification report, a completed restorable backup, exact manifest checksum confirmation, and the shared maintenance lock.
- Never log or write provider credentials, absolute paths, or member activity into migration reports.
- Old Jellyfin sources remain inactive aliases after cleanup so legacy URLs can resolve to Library.
- Preserve container isolation and perform provider integration only through HTTP.
- Do not modify or stage unrelated worktree changes.

---

## File structure

- Create `backend/audiobook_migration.go` — command modes, manifest/report models, stage validation, and orchestration.
- Create `backend/audiobook_migration_test.go` — pure manifest, hash, matching, stage, and confirmation tests.
- Create `backend/audiobook_migration_integration_test.go` — switch/rollback transaction and reference-preservation tests.
- Modify `backend/main.go` — dispatch `audiobook-migrate` before starting the HTTP server.
- Modify `backend/Dockerfile` only if required to preserve command availability; the selected design reuses the existing `telos-core` binary.
- Modify `docker-compose.yml` — add profiled one-shot `telos-audiobook-migrate` service.
- Modify `docker-compose.dev.yml` only if its mount/network overrides must be mirrored.
- Create `scripts/migrate-audiobooks.sh` — maintenance lock, backup gate, and Compose wrapper.
- Create `scripts/tests/audiobook_migration_test.sh` — fake-runtime safety tests.
- Create `documentation/operations/audiobook-migration.md` — exact operator runbook and rollback matrix.
- Modify `documentation/architecture/01-system-overview.md`.
- Modify `documentation/architecture/02-deployment.md`.
- Modify `documentation/architecture/03-gateway-and-api.md`.
- Modify `documentation/architecture/04-frontend-architecture.md`.
- Modify `documentation/product/beta-feature-status.md`.
- Modify `documentation/operations/api-list-inventory.md`.
- Modify `documentation/operations/backup-and-restore.md`.
- Modify `documentation/operations/browser-certification.md`.
- Modify `documentation/operations/release-inputs.pathspec`.
- Modify `documentation/README.md`, `README.md`, `AGENTS.md`, and `.env.example` comments only where current audiobook ownership is stated; add no speculative input.
- Modify `frontend/src/app/(shell)/library/page.tsx` — retire the Jellyfin-name-based audiobook shelf after cutover support is complete.
- Modify `frontend/src/stores/useMediaStore.ts` — redirect wrong-surface legacy aliases.
- Modify `frontend/src/components/chat/ShareCard.tsx` — honor canonical surface redirects.
- Modify focused frontend E2E specs.

### Task 1: Define the versioned manifest and stage machine

**Files:**
- Create: `backend/audiobook_migration.go`
- Create: `backend/audiobook_migration_test.go`

**Interfaces:**
- Consumes: standard library JSON/SHA-256/path APIs and existing config validation.
- Produces: `AudiobookMigrationManifest`, `AudiobookMigrationItem`, `MigrationStage`, checksum and transition validation.

- [ ] **Step 1: Write failing manifest canonicalization tests**

Define and exercise this contract:

```go
type MigrationStage string

const (
    StageInventoried MigrationStage = "inventoried"
    StageCopied MigrationStage = "copied"
    StageImported MigrationStage = "imported"
    StageVerified MigrationStage = "verified"
    StageSwitched MigrationStage = "switched"
    StageCleaned MigrationStage = "cleaned"
    StageRolledBack MigrationStage = "rolled_back"
)

type AudiobookMigrationFile struct {
    RelativeSource string `json:"relativeSource"`
    RelativeDestination string `json:"relativeDestination,omitempty"`
    SizeBytes int64 `json:"sizeBytes"`
    SHA256 string `json:"sha256"`
}

type AudiobookMigrationItem struct {
    CatalogID string `json:"catalogId"`
    JellyfinID string `json:"jellyfinId"`
    GrimmoryID string `json:"grimmoryId,omitempty"`
    Files []AudiobookMigrationFile `json:"files"`
    TotalSizeBytes int64 `json:"totalSizeBytes"`
    FileSetSHA256 string `json:"fileSetSha256"`
    DurationMS int64 `json:"durationMs"`
    Stage MigrationStage `json:"stage"`
    Verification []string `json:"verification"`
}

type AudiobookMigrationManifest struct {
    SchemaVersion int `json:"schemaVersion"`
    CreatedAt time.Time `json:"createdAt"`
    JellyfinLibraryIDs []string `json:"jellyfinLibraryIds"`
    Items []AudiobookMigrationItem `json:"items"`
    Stage MigrationStage `json:"stage"`
    ManifestSHA256 string `json:"manifestSha256"`
    BackupProof string `json:"backupProof,omitempty"`
}
```

Tests assert deterministic library/item/file sorting, checksum computed with
the checksum field empty, a file-set checksum computed from sorted
`sha256:size` pairs, rejection of duplicate catalog/Jellyfin/file-set keys,
relative clean paths only, supported stage transitions, and schema version 1.

- [ ] **Step 2: Run and observe undefined manifest types**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'TestAudiobookMigrationManifest|TestMigrationStage'
```

Expected: FAIL.

- [ ] **Step 3: Implement canonical JSON and validation**

```go
func (m *AudiobookMigrationManifest) Validate() error
func (m *AudiobookMigrationManifest) Seal() error
func ReadAudiobookManifest(path string) (AudiobookMigrationManifest, error)
func WriteAudiobookManifest(path string, m AudiobookMigrationManifest) error
func CanTransition(from, to MigrationStage) bool
```

Write atomically using a mode-0600 temporary file in the destination directory,
`fsync`, and rename. Reject symlinks and any path outside
`/data/shared/migrations/audiobooks`. Error messages identify catalog IDs and
stages, never absolute media paths.

- [ ] **Step 4: Add strict subcommand argument parsing**

Support only:

```text
telos-core audiobook-migrate inventory --library-id ID [--library-id ID...] --manifest RELATIVE
telos-core audiobook-migrate copy --manifest RELATIVE
telos-core audiobook-migrate verify --manifest RELATIVE
telos-core audiobook-migrate switch --manifest RELATIVE
telos-core audiobook-migrate rollback --manifest RELATIVE
telos-core audiobook-migrate cleanup --manifest RELATIVE --backup-proof RELATIVE --confirm-sha SHA256
```

Unknown modes/flags, empty library sets, absolute paths, and cleanup without all
confirmation inputs exit 2 before database, provider, or filesystem work.

- [ ] **Step 5: Run pure command/manifest tests**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'TestAudiobookMigration'
```

Expected: PASS.

- [ ] **Step 6: Commit the migration model**

```bash
git add backend/audiobook_migration.go backend/audiobook_migration_test.go
git commit -m "feat: define audiobook migration manifest"
```

### Task 2: Inventory and copy Jellyfin audiobook files safely

**Files:**
- Modify: `backend/audiobook_migration.go`
- Modify: `backend/audiobook_migration_test.go`
- Modify: `backend/catalog.go`
- Modify: `backend/catalog_integration_test.go`
- Modify: `backend/main.go:179-230`

**Interfaces:**
- Consumes: Jellyfin user/items APIs, canonical catalog repository, configured library authorization, `/data/shared/media`, `/data/shared/bookdrop`.
- Produces: `runAudiobookInventory`, `runAudiobookCopy`, exact SHA-256 manifests, collision-safe bookdrop copies.

- [ ] **Step 1: Write failing inventory/copy tests with a fake provider and filesystem**

Cover an allowed audiobook, non-audiobook audio, folder, out-of-scope library,
missing file, symlink, path traversal, duplicate source, destination collision,
hash mismatch during copy, and interrupted copy. Assert source inode/content is
unchanged in every case.

- [ ] **Step 2: Run and capture missing operation failures**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'TestAudiobook(Inventory|Copy)'
```

Expected: FAIL.

- [ ] **Step 3: Implement authorized Jellyfin inventory**

For each explicit `--library-id`, require it in `JELLYFIN_LIBRARY_IDS`, query
its direct book/folder records and bounded descendants with fields
`Path,RunTimeTicks,MediaSources`, group each one-file or folder-based audiobook
under its top-level canonical item, and reject non-audio leaves. Resolve/observe
the top-level canonical ID, map every Jellyfin `/data/media/<relative>` to Telos
`/data/shared/media/<relative>`, and open with the existing beneath/no-symlink
confinement primitives.

Hash every file by streaming SHA-256, count exact bytes, and derive the sorted
file-set checksum. Store only relative paths.
Inventory exits nonzero and writes no sealed manifest if any selected item is
unreadable or unresolved.

- [ ] **Step 4: Implement copy without overwrite**

Copy each source file to a unique `.partial` path under a bookdrop directory
named with the canonical UUID, hash while copying, compare hash/size to the
sealed manifest, `fsync`, then rename to its collision-free final name. Never
overwrite a destination. On failure remove only command-owned `.partial` files
and leave stage `inventoried`.

- [ ] **Step 5: Dispatch the maintenance command before server startup**

In `main`, recognize `os.Args[1] == "audiobook-migrate"` beside the existing
`migrate` command. Load only required database/provider/storage configuration,
run the selected mode, close resources, and exit. Do not initialize the HTTP
server, WebSocket registry, or background reconcilers.

- [ ] **Step 6: Run inventory/copy and full backend tests**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'TestAudiobook(Inventory|Copy|Migration)'
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit safe inventory/copy**

```bash
git add backend/audiobook_migration.go backend/audiobook_migration_test.go backend/main.go
git commit -m "feat: inventory and copy jellyfin audiobooks"
```

### Task 3: Verify Grimmory imports by file content

**Files:**
- Modify: `backend/audiobook_migration.go`
- Modify: `backend/audiobook_migration_test.go`

**Interfaces:**
- Consumes: Grimmory `/api/v1/books`, `/api/v1/books/{id}/file-metadata`, `/api/v1/audiobooks/{id}/info`, range stream, and shared books storage.
- Produces: `runAudiobookVerify` and item-scoped verified/ambiguous/error results.

- [ ] **Step 1: Write failing exact-match and ambiguity tests**

Fixtures must cover exact one-file and multi-track file-set matches, title-only
match, hash with wrong size, duplicate identical Grimmory file sets, missing catalog record, duration outside
tolerance, missing cover, failed Range response, and one good item alongside
one bad item. Only exact unique content can become `verified`.

- [ ] **Step 2: Run and observe missing verifier failure**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run TestAudiobookVerify
```

Expected: FAIL.

- [ ] **Step 3: Implement Grimmory candidate discovery and confined hashing**

List only configured `GRIMMORY_LIBRARY_IDS` and kind `AUDIOBOOK`. Read each
candidate's primary/alternative audiobook file paths, translate `/books` to
`/data/shared/books`, open beneath the shared root without symlinks, and hash
the sorted file set. Never trust provider path text directly.

Add a repository operation that attaches the verified destination without
changing the current source:

```go
func (r *CatalogRepository) AttachInactiveSource(ctx context.Context, catalogID string, in CatalogObservation) (CatalogResolution, error)
```

It locks the catalog item, requires that it already has one active source,
inserts or refreshes exactly the supplied inactive source, and rejects an
upstream ID already attached to another item.

- [ ] **Step 4: Verify metadata and Telos Range playback**

For the unique content match, call audiobook info and require:

- total bytes equal manifest;
- provider duration within max(2000 ms, 0.5% of source duration);
- cover endpoint returns an allowed image type and at most 10 MiB;
- audiobook stream returns 206 for `Range: bytes=0-1023`, correct
  `Content-Range`, and at most 1024 response bytes in the probe.

Call `AttachInactiveSource` for the exact Grimmory book match. Do not change
surface, annotations, embeds, or old source.

- [ ] **Step 5: Seal a verification report without sensitive paths**

Store per-item checks as fixed codes such as `hash`, `size`, `duration`, `cover`,
and `range`. Ambiguous/failed item errors identify canonical ID and check code.
The manifest reaches `verified` only when every selected item passes; partial
success remains `imported` and cannot switch.

- [ ] **Step 6: Run verifier/security tests**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'TestAudiobook(Verify|Migration)|Test.*(Proxy|Security|Storage)'
```

Expected: PASS.

- [ ] **Step 7: Commit content verification**

```bash
git add backend/audiobook_migration.go backend/audiobook_migration_test.go backend/catalog.go backend/catalog_integration_test.go
git commit -m "feat: verify grimmory audiobook imports"
```

### Task 4: Implement transactional source switch and rollback

**Files:**
- Modify: `backend/catalog.go`
- Modify: `backend/catalog_integration_test.go`
- Modify: `backend/audiobook_migration.go`
- Modify: `backend/audiobook_migration_test.go`
- Create: `backend/audiobook_migration_integration_test.go`

**Interfaces:**
- Consumes: one active Jellyfin source, one verified inactive Grimmory source, canonical annotations/messages/progress.
- Produces: `SwitchAudiobookSource`, `RollbackAudiobookSource`, and migration switch/rollback modes.

- [ ] **Step 1: Write a failing full-reference transaction fixture**

Seed an audiobook with Jellyfin active/Stream, Grimmory inactive, two progress
rows, private/community annotations plus replies, `stream_film` embeds with
snapshots, and an unrelated item. After switch assert:

- Grimmory is the only active source;
- surface is `library`, kind is `audiobook`;
- annotation target type is `book`, target ID unchanged;
- resolvable embed kind is `library_book`, embed ref/snapshot unchanged;
- progress rows are byte-for-byte unchanged except no columns;
- unrelated rows are untouched.

Force a failure after each SQL statement and assert the entire transaction
rolls back.

- [ ] **Step 2: Run and observe missing transaction methods**

```bash
scripts/test-backend.sh all -run 'TestSwitchAudiobookSource|TestRollbackAudiobookSource'
```

Expected: FAIL.

- [ ] **Step 3: Implement exact switch preconditions and transaction**

```go
func (r *CatalogRepository) SwitchAudiobookSource(ctx context.Context, catalogID, jellyfinID, grimmoryID string) error
func (r *CatalogRepository) RollbackAudiobookSource(ctx context.Context, catalogID, jellyfinID, grimmoryID string) error
```

The command validates the sealed verified manifest entry before calling the
repository. The repository locks the catalog item and both source rows `FOR
UPDATE`. Switch requires a current Stream/audiobook item, active expected
Jellyfin source, and inactive expected Grimmory source. Update active flags,
surface, annotation target type, matching embed kind, and increment the catalog
item revision in one transaction.
Rollback requires the inverse state and reverses those fields.

- [ ] **Step 4: Make command modes item-idempotent and cache-aware**

Switch/rollback iterate the sealed manifest in canonical-ID order. A row already
in the requested state is a verified no-op; mixed unexpected state stops before
the next item. After commit, invalidate canonical/Grimmory/Jellyfin catalog,
artwork, playback, and shelf caches. Write the manifest stage atomically after
database success.

- [ ] **Step 5: Run integration, annotation, chat, and continuity tests**

```bash
scripts/test-backend.sh all -run 'Test.*(SwitchAudiobook|RollbackAudiobook|Annotation|Embed|Continuity)'
```

Expected: PASS.

- [ ] **Step 6: Commit switch and rollback**

```bash
git add backend/catalog.go backend/catalog_integration_test.go backend/audiobook_migration.go backend/audiobook_migration_test.go backend/audiobook_migration_integration_test.go
git commit -m "feat: switch audiobook providers transactionally"
```

### Task 5: Add backup-gated cleanup and maintenance wrapper

**Files:**
- Modify: `backend/audiobook_migration.go`
- Modify: `backend/audiobook_migration_test.go`
- Modify: `docker-compose.yml`
- Modify: `docker-compose.dev.yml`
- Create: `scripts/migrate-audiobooks.sh`
- Create: `scripts/tests/audiobook_migration_test.sh`
- Modify: `tests/operations/backup_restore_test.sh`
- Modify: `tests/operations/runtime_hardening_test.sh`

**Interfaces:**
- Consumes: sealed switched manifest, verified backup manifest, exact confirmation SHA, maintenance lock.
- Produces: profiled one-shot service, safe operator wrapper, cleanup mode.

- [ ] **Step 1: Write fake-runtime shell tests before the wrapper**

Using the repository's fake `podman` pattern, assert:

- inventory/copy/verify/switch/rollback invoke only their matching mode;
- cleanup refuses without a mode-0600 backup proof, a verified backup
  `SHA256SUMS`, matching manifest SHA, and maintenance lock;
- cleanup never runs concurrently with backup/restore;
- a failed command leaves the manifest and source files intact;
- arguments containing traversal, newlines, or shell metacharacters are rejected;
- secrets never appear in output.

- [ ] **Step 2: Run and confirm the missing wrapper failure**

```bash
bash scripts/tests/audiobook_migration_test.sh
```

Expected: FAIL because the script does not exist.

- [ ] **Step 3: Add the profiled one-shot service**

Add `telos-audiobook-migrate` with:

- `profiles: ["tools"]` so default `up` never runs it;
- `restart: "no"`;
- command `./telos-core audiobook-migrate` with wrapper-supplied arguments;
- `telos-backend` and `telos-db` networks;
- runtime DB URL and existing Jellyfin/Grimmory credentials/allowlists;
- the shared storage mount at `/data/shared:z`;
- dependencies on healthy PostgreSQL, Jellyfin, and Grimmory;
- no ingress network, ports, privileged mode, or container-runtime socket.

- [ ] **Step 4: Implement the maintenance-lock wrapper**

`scripts/migrate-audiobooks.sh` validates a fixed mode allowlist, resolves a
relative manifest beneath the migration directory, acquires
`scripts/maintenance-lock.sh`, and runs Compose with the tools profile. Cleanup
first runs `scripts/backup.sh` into an operator-supplied backup root, validates
`SHA256SUMS` and the required current artifacts (`postgres.dump`,
`grimmory.sql`, `shared-storage.tar.gz`, and the three service-config
directories), then writes a mode-0600 proof under the audiobook migration
directory containing the backup generation path, checksum-file digest,
creation time, and sealed audiobook manifest SHA. It passes that relative proof
path and exact manifest SHA to the one-shot command.

- [ ] **Step 5: Implement cleanup as the only destructive mode**

Cleanup revalidates manifest stage `switched`, the mode-0600 backup proof,
confirmation SHA,
and that every source file still matches its recorded hash/size. It deletes only
exact manifest source files under `/data/shared/media`, never directories or globs.
After deletion it marks entries `cleaned`; a partial failure records completed
items and exits nonzero without broad rollback or further deletion.

- [ ] **Step 6: Run shell, Compose, hardening, and backend tests**

```bash
bash scripts/tests/audiobook_migration_test.sh
docker compose --env-file .env.example config --quiet
bash tests/operations/runtime_hardening_test.sh
bash tests/operations/backup_restore_test.sh
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run TestAudiobookMigration
```

Expected: PASS.

- [ ] **Step 7: Commit the operational command**

```bash
git add backend/audiobook_migration.go backend/audiobook_migration_test.go docker-compose.yml docker-compose.dev.yml scripts/migrate-audiobooks.sh scripts/tests/audiobook_migration_test.sh tests/operations/backup_restore_test.sh tests/operations/runtime_hardening_test.sh
git commit -m "feat: add backup-gated audiobook migration tool"
```

### Task 6: Complete compatibility redirects and retire Jellyfin audiobook UI

**Files:**
- Modify: `frontend/src/app/(shell)/stream/page.tsx`
- Modify: `frontend/src/app/(shell)/library/page.tsx`
- Modify: `frontend/src/stores/useMediaStore.ts`
- Modify: `frontend/src/components/chat/ShareCard.tsx`
- Modify: `frontend/src/components/chat/SharePicker.tsx`
- Modify: `frontend/e2e/library-audiobooks.spec.ts`
- Modify: `frontend/e2e/stream.spec.ts`
- Modify: `frontend/e2e/chat.spec.ts`

**Interfaces:**
- Consumes: canonical wrong-surface resolution/redirect and switched source state.
- Produces: one Library audiobook presentation and durable old-link behavior.

- [ ] **Step 1: Write failing switched-alias browser tests**

Assert an old `/stream?play=<jellyfin-id>` opens
`/library?listen=<canonical-id>`, a historical `stream_film` chat card does the
same, a new share uses `library_book`, and the migrated audiobook is absent from
Stream shelves while appearing once in Library.

- [ ] **Step 2: Run focused E2E tests and observe duplicate/wrong-route failures**

```bash
cd frontend
npx playwright test library-audiobooks.spec.ts stream.spec.ts chat.spec.ts
```

Expected: FAIL before redirect/retirement changes.

- [ ] **Step 3: Handle canonical surface redirects centrally**

When an item lookup returns a structured wrong-surface response containing only
canonical ID and `surface`, route `library` to `?listen=` and `stream` to
`?play=`. Do not infer provider or format from ID/name. Reuse the helper for
deep links and ShareCard clicks.

- [ ] **Step 4: Remove the Jellyfin-name-based Library audiobook shelf**

Delete the code that filters Jellyfin libraries by a name containing
`audiobook`. Library audiobooks come only from `useLibraryStore`; Stream skips
sources whose canonical active surface is Library. Retain ordinary Jellyfin
music/audio behavior.

- [ ] **Step 5: Run lint, components, and focused E2E tests**

```bash
cd frontend
npm run lint
npx vitest run
npx playwright test library-audiobooks.spec.ts stream.spec.ts chat.spec.ts
```

Expected: PASS with no duplicate audiobook.

- [ ] **Step 6: Commit the UI cutover**

```bash
git add frontend/src/app/'(shell)'/stream/page.tsx frontend/src/app/'(shell)'/library/page.tsx frontend/src/stores/useMediaStore.ts frontend/src/components/chat/ShareCard.tsx frontend/src/components/chat/SharePicker.tsx frontend/e2e/library-audiobooks.spec.ts frontend/e2e/stream.spec.ts frontend/e2e/chat.spec.ts
git commit -m "feat: complete grimmory audiobook cutover"
```

### Task 7: Update normative architecture and operator documentation

**Files:**
- Create: `documentation/operations/audiobook-migration.md`
- Modify: `documentation/architecture/01-system-overview.md`
- Modify: `documentation/architecture/02-deployment.md`
- Modify: `documentation/architecture/03-gateway-and-api.md`
- Modify: `documentation/architecture/04-frontend-architecture.md`
- Modify: `documentation/product/beta-feature-status.md`
- Modify: `documentation/operations/api-list-inventory.md`
- Modify: `documentation/operations/backup-and-restore.md`
- Modify: `documentation/operations/browser-certification.md`
- Modify: `documentation/operations/release-inputs.pathspec`
- Modify: `documentation/README.md`
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `.env.example`

**Interfaces:**
- Consumes: the exact shipping routes, command modes, Compose service, failure behavior, and tests from all phases.
- Produces: a consistent buildable specification and complete release inventory.

- [ ] **Step 1: Write the operator runbook from executable behavior**

Document prerequisites, explicit library-ID selection, each command with exact
syntax, expected manifest stages, Grimmory scan wait, verification codes,
switch, browser checks, backup-gated cleanup, pre-cleanup rollback, post-cleanup
restore, partial failure, and where reports live. State clearly that cleanup is
the only destructive mode.

- [ ] **Step 2: Update every architecture ownership statement**

Make these statements consistent:

- Jellyfin owns films, television, music, artwork, and HLS playback options.
- Grimmory owns EPUB, PDF, and audiobooks including Range/track streams.
- Telos owns stable identity, per-member progress, commentary, links, and
  provider migration aliases.
- Shared provider accounts never own member progress/history.

Remove the current claim that audiobooks are ordinary Jellyfin `books`/music
libraries. Update the headless API matrix with every new public/internal route.

- [ ] **Step 3: Update product truth, API inventory, backup, and browser certification**

Add only features proven by green tests: real artwork/details,
Continue/Recent/Related, author/series navigation, Grimmory audiobook player,
chapters, subtitles/audio tracks, and minimal progress. Add catalog/source/
progress tables and migration reports to backup/restore verification. Add the
new E2E specs to browser certification.

- [ ] **Step 4: Update repository and environment inventories**

Add every runtime file from all four plans to
`documentation/operations/release-inputs.pathspec` and the clean-checkout
inventory where required. `.env.example` gains no new variable; update comments
so selected Jellyfin libraries exclude migrated audiobook libraries after
cleanup and Grimmory's allowed IDs include the audiobook destination library.

- [ ] **Step 5: Run documentation placeholder and consistency scans**

```bash
grep -rn "ci""te:" documentation/ AGENTS.md ; echo "exit=$?"
grep -rnE "TO""DO|TB""D" documentation/ AGENTS.md ; echo "exit=$?"
rg -n 'Audiobooks are not served by Grimmory|audiobook.*Jellyfin|Jellyfin.*audiobook' documentation README.md AGENTS.md .env.example
scripts/verify-clean-checkout.sh --inventory-only
```

Expected: the first two searches return no matches with exit 1; the ownership
search returns only historical/migration-context statements; inventory passes.

- [ ] **Step 6: Commit the normative cutover**

```bash
git add documentation README.md AGENTS.md .env.example
git commit -m "docs: make grimmory the audiobook authority"
```

### Task 8: Run the full release and migration rehearsal gate

**Files:**
- Modify only files required by concrete failures from this gate.

**Interfaces:**
- Consumes: all four completed implementation phases.
- Produces: release evidence and a rehearsed no-cleanup migration/rollback.

- [ ] **Step 1: Run the complete repository verification suite**

```bash
grep -rn "ci""te:" documentation/ AGENTS.md ; echo "exit=$?"
grep -rnE "TO""DO|TB""D" documentation/ AGENTS.md ; echo "exit=$?"
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
cd frontend
npm run lint
npx vitest run
npx playwright test
```

Expected: searches exit 1 with no matches; backend, lint, unit, and all browser
tests PASS.

- [ ] **Step 2: Run operations and clean-checkout suites**

```bash
docker compose --env-file .env.example config --quiet
bash scripts/migrate-audiobooks.sh --help
bash scripts/tests/audiobook_migration_test.sh
bash tests/operations/accumulated_suite_test.sh
scripts/verify-clean-checkout.sh --inventory-only
```

Expected: all commands PASS; help performs no mutation.

- [ ] **Step 3: Rehearse inventory through rollback on fixture storage**

Using disposable fixture media and provider records, run inventory, copy,
verify, switch, compatibility browser checks, and rollback. Do not run cleanup.
Assert source hash remains unchanged, canonical ID/progress/comments/chat refs
remain stable, and the item returns to Stream after rollback.

- [ ] **Step 4: Rehearse backup-gated cleanup only on disposable fixtures**

Take a fixture backup, pass its manifest and exact sealed SHA to cleanup, assert
only the named fixture source is removed, then restore it and verify rollback
reactivation. Never use the live storage path for this test.

- [ ] **Step 5: Close the gate without a catch-all commit**

Run `git status --short`. If a gate failed, return to the task that owns the
failing file, add a regression test there, use that task's exact `git add` list,
and rerun Steps 1–4. If every gate passed, leave the tree unchanged and do not
create an empty commit.

## Phase 4 completion gate

The implementation is complete only when the full repository suite passes, a
fixture migration and rollback preserve all canonical member state, fixture
cleanup proves exact-target deletion and restore, the default Compose stack
does not run the migration service, and every normative document assigns
audiobooks to Grimmory.
