# Container-Powered Member Experience — Design

**Date:** 2026-07-31

**Status:** Approved for planning

**Scope:** Use capabilities verified in the running Grimmory 3.2.4 and Jellyfin
10.11.11 containers to improve the existing Library and Stream modules, add
minimal per-member continuity, and move audiobook ownership from Jellyfin to
Grimmory through a staged migration.

## 1. Purpose

Telos already has capable catalog services underneath a deliberately small
product. The gateway currently exposes only a narrow subset of those services:
basic hierarchy browsing and playback from Jellyfin, and EPUB/PDF catalog,
content, progress, and management from Grimmory. The member experience leaves
useful upstream capabilities unused and exposes provider identifiers directly
in commentary, progress, and chat links.

This design improves the experience without adding a module or turning Telos
into a generic Jellyfin or Grimmory client. Library, Stream, Chat, and Settings
remain the complete information architecture.

### 1.1 Decisions

- Improve Library and Stream together through consistent detail, artwork,
  discovery, and continuity patterns.
- Store per-member continuity in Telos PostgreSQL because both providers are
  accessed through shared service accounts.
- Store only the latest position and completion state, not a timestamped
  playback ledger.
- Use transparent discovery: Continue, Recently Added, series, author, and
  related metadata. Personalized recommendations are deferred.
- Make Grimmory the sole audiobook catalog and playback provider.
- Migrate existing Jellyfin audiobooks in stages, preserving links,
  commentary, and progress through stable Telos catalog identities.
- Limit the canonical Grimmory Library catalog to EPUB, PDF, and audiobook
  records. Omit unknown or unsupported formats from member-visible canonical
  Library responses and defer them. They must never be retyped as audiobook,
  EPUB, PDF, or folder. This preserves the minimal scope alongside the existing
  deferral of comics and physical-book records.

### 1.2 Success criteria

1. Every displayed book, audiobook, film, episode, or music item has a stable
   Telos identifier independent of its active provider record.
2. Library and Stream show real provider artwork and useful item details instead
   of synthetic poster colors and title/duration alone.
3. Members can resume EPUB, PDF, audiobook, video, and audio items independently
   without one member seeing or overwriting another member's position.
4. Stream exposes available chapters, subtitles, and audio tracks through
   narrow gateway routes without leaking a Jellyfin token or host.
5. Library exposes Grimmory author and series navigation and plays Grimmory
   audiobooks with track-aware resume.
6. Existing Jellyfin audiobook links, chat shares, commentary, and progress
   resolve after migration to Grimmory.
7. No automated step deletes an existing audiobook file. Removal from the old
   Jellyfin storage happens only after verification, backup confirmation, and
   an explicit operator action.
8. The normative documentation describes Grimmory, rather than Jellyfin, as
   the audiobook owner.

## 2. Verified container capabilities

The inventory was taken from the running Telos containers on 2026-07-31. It is
release-specific evidence, not an assumption based on a different upstream
version.

| Service | Running version | Verified surface | Relevant unused capabilities |
| --- | --- | --- | --- |
| Grimmory | 3.2.4 | Internal OpenAPI document with 347 operations | Audiobook info, cover, range streams and per-track streams; author/series browsing; recent and continue queries; shelves; ratings/status; recommendations; OPDS/Kobo/KoReader; reading sessions/statistics; bookdrop review; duplicate detection; metadata tasks; audit logs; CBX and physical books |
| Jellyfin | 10.11.11 | Internal OpenAPI document with 368 operations | Real item images and rich metadata; playback info; chapters; subtitle search/streaming; audio streams; trickplay; latest, next-up, similar and related queries; playstate; lyrics; playlists; collections; Live TV; SyncPlay |

The selected increment uses only the capabilities needed by the approved
member-experience scope. The broad provider surfaces are not exposed wholesale.

### 2.1 Current discrepancies to resolve

- `documentation/architecture/03-gateway-and-api.md` says audiobooks are not
  served by Grimmory. That is true of the integration as currently implemented,
  but false of the running Grimmory 3.2.4 feature surface. This design changes
  the normative ownership decision to Grimmory.
- The less-is-more design listed re-architecture of the Jellyfin and Grimmory
  integrations as a non-goal. The stable identity and continuity layer in this
  design intentionally supersede that narrow non-goal while preserving the
  container/process boundary and the three-module product structure.
- `book_progress.book_id`, annotation `target_id`, and chat `embed_ref` retain
  upstream IDs. They cannot preserve identity when an audiobook changes
  provider and must migrate to stable Telos IDs.
- Current Stream cards synthesize colored posters even though Jellyfin provides
  image endpoints and image tags.
- The current Grimmory book shape assumes EPUB or PDF. It must recognize
  audiobook records, while unknown or unsupported Grimmory formats stay out of
  the canonical Library catalog rather than being retyped. CBX comics and
  physical-book handling remain deferred.

## 3. Architecture

### 3.1 Boundaries

The browser continues to call only `telos-core`. Jellyfin and Grimmory stay on
internal networks and are integrated only over HTTP. No provider source is
linked or compiled into the Go gateway, preserving the copyleft boundary.

The gateway is divided into four logical units:

1. **Catalog identity store** — stable Telos item IDs and provider aliases.
2. **Jellyfin adapter** — Stream catalog, artwork, details, playback options,
   subtitles, chapters, and binary streams.
3. **Grimmory adapter** — Library catalog, EPUB/PDF content, audiobook streams,
   covers, authors, and series.
4. **Continuity store** — the latest per-member locator/position and completion
   state for a stable catalog item.

These are internal boundaries, not new public modules. Existing focused files
may host them initially, but provider translation, catalog identity, and
continuity must remain separately testable and must not be added to the already
large route-registration portions of `backend/main.go` as one intertwined
handler.

### 3.2 Stable catalog identity

Add two tables:

```sql
CREATE TABLE catalog_items (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    surface     TEXT NOT NULL CHECK (surface IN ('library', 'stream')),
    kind        TEXT NOT NULL CHECK (
                  kind IN ('epub', 'pdf', 'audiobook', 'video', 'audio', 'folder')
                ),
    revision    BIGINT NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE catalog_sources (
    catalog_item_id    UUID NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    provider           TEXT NOT NULL CHECK (provider IN ('grimmory', 'jellyfin')),
    upstream_id        TEXT NOT NULL,
    upstream_library_id TEXT NOT NULL,
    active             BOOLEAN NOT NULL DEFAULT true,
    available          BOOLEAN NOT NULL DEFAULT true,
    first_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    missing_since      TIMESTAMPTZ,
    PRIMARY KEY (provider, upstream_id),
    UNIQUE (catalog_item_id, provider, upstream_id)
);

CREATE UNIQUE INDEX uq_catalog_sources_active
    ON catalog_sources (catalog_item_id)
    WHERE active;
```

A folder identity supports stable Stream hierarchy navigation but never carries
member progress. The partial unique index permits only one active source per
catalog item. Old source rows remain as inactive aliases. A request containing
an old Jellyfin audiobook ID therefore resolves to the same Telos UUID whose
active source is now Grimmory.

Catalog reconciliation upserts provider records and marks a source unavailable
only after a completed provider scan. Observation restores availability and
clears `missing_since`. A transient provider failure never deletes an identity
or makes a previously known item disappear permanently. Activating another
source increments the catalog revision so cache keys cannot reuse bytes or
metadata from the previous provider.

Provider library authorization is still evaluated on every sensitive catalog,
content, artwork, commentary, and stream request. Resolving a valid UUID is not
authorization by itself. `JELLYFIN_LIBRARY_IDS` and `GRIMMORY_LIBRARY_IDS`
remain stable upstream allowlists.

### 3.3 Minimal member continuity

Replace the provider-specific `book_progress` model with one continuity table:

```sql
CREATE TABLE member_progress (
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    catalog_item_id UUID NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    locator         JSONB NOT NULL DEFAULT '{}',
    position_ms     BIGINT NOT NULL DEFAULT 0 CHECK (position_ms >= 0),
    duration_ms     BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    percent         REAL NOT NULL DEFAULT 0 CHECK (percent >= 0 AND percent <= 1),
    completed       BOOLEAN NOT NULL DEFAULT false,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, catalog_item_id)
);
```

The locator is format-specific and validated against the server-derived item
kind:

- EPUB: `{ "cfi": "...", "fraction": 0.42 }`
- PDF: `{ "page": 12, "zoom": 1.25 }`
- Grimmory audiobook: `{ "trackIndex": 3 }` plus `position_ms`
- Jellyfin video/audio: `{}` plus `position_ms`

Clients save on pause, seek completion, track/chapter change, media completion,
and at a bounded 15-second interval while playing. Writes are idempotent
upserts. `completed=true` is set by an `ended` event or an explicit completion
action; percentage alone does not silently complete an item. Completed items
remain recorded but do not appear in Continue.

No table records individual play events, starts, pauses, seeks, or historical
positions.

## 4. Public gateway contract

All item payloads use a common envelope where practical:

```json
{
  "id": "telos-uuid",
  "title": "Item title",
  "kind": "audiobook",
  "surface": "library",
  "coverUrl": "/api/v1/library/items/telos-uuid/cover",
  "overview": "Provider description",
  "year": 2024,
  "genres": ["History"],
  "series": { "name": "Series", "number": 2 },
  "progress": {
    "positionMs": 125000,
    "durationMs": 3600000,
    "percent": 0.034,
    "completed": false,
    "updatedAt": "2026-07-31T18:00:00Z"
  }
}
```

Provider-only fields stay inside adapter types. The public envelope does not
expose provider tokens, base URLs, filesystem paths, or the active upstream ID.

### 4.1 Shared compatibility behavior

- New responses and generated links use Telos UUIDs.
- During the compatibility period, routes accept a UUID or a validated legacy
  upstream ID and return the canonical UUID in the response.
- Existing `/stream?play=<legacy-id>` links resolve the alias. If the item is a
  migrated audiobook, the client redirects to
  `/library?listen=<canonical-id>`.
- Existing chat share cards keep their immutable display snapshot. Their
  `embed_ref` is backfilled to the canonical UUID when resolvable. An audiobook
  cutover also changes its resolvable `embed_kind` from `stream_film` to
  `library_book`; the compatibility resolver protects any unconverted
  historical record.
- Commentary reads and writes resolve both the identifier and canonical
  surface before querying annotations. Audiobook cutover migrates its
  annotation namespace from `(media, canonical-id)` to
  `(book, canonical-id)`, so old and new links address the same thread.

### 4.2 Library additions

- Existing `GET /api/v1/library/books` returns canonical IDs, real cover URLs,
  item kind, progress summary, description, author, series, and publication
  metadata.
- `GET /api/v1/library/continue` returns the current member's incomplete items,
  ordered by `member_progress.updated_at DESC`.
- `GET /api/v1/library/recent` uses Grimmory's recently-added application query
  and returns a bounded shelf.
- `GET /api/v1/library/authors` and
  `GET /api/v1/library/authors/{id}/books` translate Grimmory author browsing.
- `GET /api/v1/library/series` and
  `GET /api/v1/library/series/{name}/books` translate Grimmory series browsing.
- Existing progress routes accept the canonical item ID and use
  `member_progress`.
- `GET` and `POST /api/v1/library/items/{id}/comments` provide plain commentary
  for audiobooks with an empty locator. EPUB/PDF selection annotations retain
  the existing book routes and validated reader locators.
- `GET /api/v1/library/audiobooks/{id}/info` returns track, duration, and chapter
  information normalized from Grimmory.
- `GET /api/v1/library/audiobooks/{id}/stream` and
  `GET /api/v1/library/audiobooks/{id}/tracks/{index}/stream` proxy only the
  verified Grimmory range-stream endpoints. Browser Range and conditional
  headers pass through the existing binary proxy allowlist; provider auth and
  redirects do not.

`view_library` gates every member Library route. Existing shared management
operations continue to require `manage_library`.

### 4.3 Stream additions

- Existing media list and item endpoints return canonical IDs, Jellyfin image
  URLs, overview, year, genres, series/season/episode metadata, and progress.
- `GET /api/v1/media/continue` uses Telos progress, not Jellyfin's shared-user
  resume list.
- `GET /api/v1/media/recent` translates Jellyfin Latest queries within the
  configured library allowlist.
- `GET /api/v1/media/items/{id}/related` uses deterministic Jellyfin similar
  results and labels the shelf by its source rather than implying personal
  recommendations.
- `GET /api/v1/media/items/{id}/playback` wraps Jellyfin PlaybackInfo and exposes
  normalized media sources, audio tracks, subtitles, chapters, and direct-play
  or HLS choices.
- `GET` and `PUT /api/v1/media/items/{id}/progress` read and update the minimal
  Telos progress row.
- Explicit subtitle routes accept only the canonical item, selected media
  source, numeric stream index, and an allowed output format. They translate to
  Jellyfin's subtitle stream endpoint without accepting an arbitrary upstream
  subpath.
- HLS manifest rewriting continues to mint opaque, session-bound Telos
  locators. Track and subtitle selection become allowlisted playback inputs,
  never raw query forwarding.

`view_media` gates every Stream route.

## 5. Member experience

### 5.1 Consistent browse hierarchy

Library and Stream use the same shelf vocabulary without merging their
catalogs:

1. Continue — present only when the member has incomplete progress.
2. Recently Added — provider catalog chronology, not behavioral ranking.
3. Browse — current Library facets or Stream provider libraries.
4. Contextual related shelf — author/series in Library and similar metadata in
   Stream, shown only from an item detail view.

Empty shelves are omitted. Provider failure affects only the corresponding
shelf and displays a recoverable inline error; it does not erase cached browse
content or block the other module.

### 5.2 Real artwork and item details

Stream removes synthetic poster motifs whenever Jellyfin has a primary image.
A deterministic placeholder remains for items without artwork. Library keeps
Grimmory covers and uses the same loading, error, and aspect-ratio behavior.

Selecting a leaf item opens an item detail surface before playback. It shows
cover/backdrop, title, creator/cast summary where available, description, year,
duration, genres, series context, progress, commentary, and a primary action of
Read, Listen, Play, or Continue. Folder items retain the existing hierarchical
navigation behavior.

### 5.3 Library readers and audiobook player

EPUB and PDF retain their current readers and annotation locators. Their
progress storage changes underneath them without changing member semantics.
Author and series labels become navigable filters.

An audiobook detail opens a Library-owned audio player with:

- cover, title, author, and series context;
- track/chapter list when Grimmory exposes one;
- current and total time;
- playback speed using the existing supported rates;
- previous/next track or chapter;
- resume from the member's local track and position;
- commentary on the stable Library item;
- share-to-Chat using `library_book` and the canonical ID.

The player does not write progress back to Grimmory's shared admin account.

### 5.4 Stream player

The existing HLS player gains:

- resume or start-over when saved progress is meaningful;
- chapter navigation;
- audio-track selection;
- subtitle selection and Off;
- playback speed and Picture-in-Picture retained;
- final progress save on pause, stop, and ended;
- commentary on the stable Stream item.

Subtitles and alternate audio are displayed only when PlaybackInfo confirms
they exist. Unsupported formats degrade to the provider-selected default rather
than preventing playback.

## 6. Audiobook migration

The migration is item-scoped and reversible. It is not a flag-day transfer.

### 6.1 Additive preparation

1. Deploy the catalog identity, source alias, and continuity tables.
2. Reconcile current Grimmory books and allowed Jellyfin media into stable
   catalog items.
3. Backfill existing book progress, commentary targets, and chat embed refs to
   canonical IDs while legacy resolution remains active.
4. Add Grimmory audiobook browse and playback behind the normal
   `view_library` capability.

Database migrations create schema only. They never call a provider. Runtime
reconciliation performs provider-dependent backfills and records an auditable
summary of mapped, ambiguous, and unresolved records.

### 6.2 Import and matching

For each Jellyfin audiobook:

1. Produce a manifest containing relative source path, byte size, SHA-256,
   duration, title, authors, and the current Jellyfin ID. The manifest contains
   no credentials.
2. Copy the files into Grimmory's bookdrop or managed books path. Do not move or
   delete the source.
3. Import through Grimmory and wait for its completed scan.
4. Match the new Grimmory record using file hash and byte size. Metadata-only
   matches are never accepted automatically.
5. Verify file count, total bytes, duration within container-reported tolerance,
   cover availability, and successful HTTP Range playback from Telos.
6. Add the Grimmory source to the existing catalog item while leaving Jellyfin
   active.
7. In one transaction, mark Grimmory active, change the catalog item's surface
   from `stream` to `library`, retain kind `audiobook`, migrate annotation
   target type from `media` to `book`, and update resolvable chat embed kinds.
   The old Jellyfin source remains an inactive alias and rollback target.

Ambiguous or failed items remain active in Jellyfin and are reported to the
operator. Successfully migrated items appear once in Library; they are not
duplicated in Stream.

### 6.3 Cutover and cleanup

After all desired items pass verification:

- refresh the Stream and Library caches;
- verify legacy Stream URLs redirect to the Library audiobook;
- verify existing commentary, chat cards, and member progress;
- take or confirm a restorable backup;
- require an explicit operator confirmation before removing old files from the
  Jellyfin media path;
- run a completed Jellyfin scan and retain the inactive source aliases.

Before old-file cleanup, rollback is the inverse source-switch transaction: it
reactivates Jellyfin, returns the item to Stream, changes the annotation target
type back to `media`, restores resolvable chat embed kinds, and refreshes caches.
The stable item ID means progress rows, annotation target IDs, and chat embed
refs do not change. After old-file cleanup, reactivation requires restoring the
verified source files from backup before switching providers.

## 7. Failure handling and observability

- Upstream not found maps to 404 only after canonical identity and library
  authorization checks; out-of-scope IDs are indistinguishable from missing
  IDs.
- Provider timeout or malformed response maps to a terse 502/503 API error and
  a recoverable inline member message. Telos never fabricates metadata,
  chapters, or a successful migration.
- A progress-save failure does not interrupt playback. The client retains the
  last unsaved position, retries with bounded backoff, and reports a subtle
  unsaved-state indicator.
- Catalog reconciliation records counts and durations for created, refreshed,
  missing, ambiguous, and failed sources. It emits no titles, paths, or member
  activity into metrics labels.
- Migration verification produces a local operator report keyed by canonical
  ID and status. It never logs provider tokens or full filesystem paths.
- Existing stream concurrency limits apply to Jellyfin and Grimmory audiobook
  streams so moving providers cannot bypass node-wide or per-user admission.
- Cache entries are keyed by canonical item ID and provider source revision.
  Activating a new source invalidates the old artwork, metadata, and playback
  entries atomically with the switch.

## 8. Security and privacy

- Provider admin credentials stay in `.env` interpolation and are injected only
  into server-to-server requests.
- No raw Jellyfin or Grimmory URL, redirect, cookie, authorization header, or
  query string reaches the browser.
- Every binary route validates canonical identity, active source, configured
  library membership, member capability, range bounds, response type, and
  allowed response headers.
- Stable IDs prevent provider migration from bypassing commentary
  authorization; they do not replace it.
- Progress is recipient-scoped by `(user_id, catalog_item_id)`. Administrative
  routes do not expose another member's position.
- Continue shelves return only the current member's rows.
- No playback event history or personalized ranking profile is created.
- Provider annotations, reviews, ratings, playstate, and user statistics are
  not used because the shared upstream accounts cannot represent Telos member
  identity safely.

## 9. Verification

### 9.1 Backend and database

- Migration tests for additive schema, idempotency, foreign keys, partial
  uniqueness, legacy progress backfill, and rollback-safe source switching.
- Unit tests for canonical/legacy resolution, authorization after resolution,
  provider adapter normalization, missing artwork, malformed provider data, and
  inactive aliases.
- Integration tests proving member isolation for progress and Continue results.
- Proxy tests proving Range support, header/query allowlists, redirect
  rejection, token stripping, HLS locator binding, subtitle path validation,
  and shared stream admission limits.
- Reconciliation tests proving a failed provider scan cannot mark known sources
  missing.
- Audiobook migration fixture tests for exact hash match, ambiguous metadata,
  partial import, verification failure, source switch, and rollback.

### 9.2 Frontend

- Component tests for normalized cards, artwork fallback, detail actions,
  Continue shelf omission, resume/start-over, progress retry indication,
  audiobook track changes, chapters, subtitle/audio selection, and provider
  degradation.
- Playwright coverage for independent member positions, EPUB/PDF continuity,
  Grimmory audiobook Range playback and resume, Jellyfin video resume,
  subtitles, chapter navigation, real artwork, author/series navigation,
  commentary continuity, chat-card compatibility, and migrated legacy links.
- Mobile coverage at the existing portrait and landscape support sizes,
  including touch targets and player overflow.
- Accessibility checks for artwork alternatives, detail-surface focus
  management, timed-media controls, subtitle labels, keyboard operation, and
  status announcements.

### 9.3 Repository suite

Run the verification suite from `AGENTS.md`, plus focused migration and
compatibility tests. The final implementation plan must divide work so each
phase leaves `docker compose --env-file .env.example config --quiet` valid and
keeps documentation consistent with shipping code.

## 10. Documentation changes required with implementation

- Update the architecture integration matrix with stable catalog identities,
  continuity, artwork/details, playback options, and Grimmory audiobook routes.
- Replace every statement that assigns audiobooks to Jellyfin.
- Update the beta feature-status matrix only as each user-visible slice ships.
- Document the staged audiobook migration, verification report, explicit
  cleanup confirmation, and rollback procedure under `documentation/operations/`.
- Update `.env.example` only if implementation introduces a real runtime input;
  do not add speculative variables.
- Preserve the three modules plus Settings and the current privacy posture.

## 11. Deferred candidates

The container examination identified credible later increments, but none belong
in this implementation plan:

- Grimmory CBX comic reading, physical-book records, shelves/magic shelves,
  OPDS, Kobo, KoReader, bookdrop review, duplicate handling, metadata tasks,
  sidecar metadata, audit UI, reviews, ratings, and reading statistics.
- Jellyfin playlists, favorites/My List, personalized suggestions, lyrics,
  trickplay thumbnails, Live TV, collections, remote subtitle acquisition,
  SyncPlay, and session remote control.
- Per-service Telos member provisioning or OIDC federation.
- Voice, Watch Parties, notification inbox, or a new top-level module.

Candidates should be reconsidered only through a separate design. In
particular, Jellyfin SyncPlay must not reintroduce the removed Watch Party
surface, and provider ratings/statistics must not write multiple members into a
shared upstream identity.
