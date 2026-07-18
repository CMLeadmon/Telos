# Telos Beta-Readiness Remediation Design

**Status:** Approved on 2026-07-18

## Purpose

This design defines the remediation program required to move Telos from its
current development state to an Internet-exposed, invite-only beta on one
operator-controlled server.

The beta treats community data as durable and confidential. There is no planned
data reset. Every visible product surface must work at its documented minimum
level; incomplete functionality must not remain visible or advertised.

## Product Decisions

The beta includes the complete advertised product with these explicit
decisions:

- Remove Oracle, AI summaries, and every other AI feature from the beta.
- Remove AI controls, API scope, runtime dependencies, marketing claims, and
  normative documentation.
- Do not reserve a beta schema or endpoint for a future AI module.
- Implement one-level chat threads.
- Implement a durable in-app notification inbox.
- Implement EPUB and PDF annotations that are private by default and may be
  explicitly shared community-wide.
- Permit annotation authors to edit or delete their annotations.
- Permit moderators with the relevant capability to remove shared annotations.
- Implement annotation replies and notifications for shared-annotation replies.
- Implement in-app PDF reading and preserve EPUB reading.
- Implement a durable per-user My List for authorized Jellyfin items.
- Implement host-controlled synchronized Watch Parties.
- Link Watch Parties to existing Telos chat and voice channels rather than
  introducing a second communication stack.
- License the Telos gateway and client under Apache-2.0.

## Beta Environment and Support Targets

- Deployment shape: one Internet-exposed, operator-controlled, self-hosted
  server.
- Membership: trusted, invite-only community members.
- Registered-member target: 100.
- Concurrent authenticated-user target: 25.
- Real-time event target: 10 chat or annotation events per second.
- Watch Party target: 5 concurrent parties.
- Voice target: 25 participants across rooms.
- Desktop browsers: current Chrome, Firefox, Safari, and Edge.
- Mobile browsers: current Safari on iOS and Chrome on Android.
- Recovery point objective: at most 24 hours.
- Recovery time objective: at most 4 hours.
- Backup retention: 14 daily generations and 8 weekly generations.
- Availability model: scheduled maintenance is allowed; high availability is
  outside beta scope.

## Remediation Program

The work is delivered as one master program and seven sequential phase plans.
Each phase produces independently testable software and has an entry gate, an
exit gate, and a review stop.

### Phase 1: Release Baseline

Preserve and classify the current dirty working tree, track every required
migration and configuration file, remove tracked prototype data, establish
Apache-2.0 licensing, and prove that a clean checkout can build and boot.
Replace generic repository links, add the required in-app Credits surface, and
publish an explicit beta feature-status matrix.

No later phase may begin from untracked runtime-critical files.

### Phase 2: Security and Authentication

Repair proxy credential isolation, remove URL-based sessions, make WebSocket
authorization revocable, enforce channel existence and permissions, eliminate
invite/bootstrap/last-Owner races, bound request bodies, harden client-address
and rate-limit handling, add production security headers, and upgrade vulnerable
runtime and application dependencies.

### Phase 3: Database and Data Lifecycle

Separate schema-owner and runtime roles, harden migrations, add constraints and
indexes, configure database and pool timeouts, implement transactional event
delivery, complete account deletion and retention behavior, and prove backup
and restore semantics.

### Phase 4: Storage and Media Reliability

Make uploads atomic, validate EPUB containers, restore malware scanning and
controlled metadata egress, add quotas and free-space safeguards, reconcile
filesystem/database state, authorize Jellyfin items, and preserve range-aware
long-running media and book responses.

### Phase 5: Advertised Product Features

Implement threads, notifications, annotations, in-app PDF reading, My List, and
Watch Parties. Remove all AI/Oracle surfaces and claims during this phase so
that product copy and implemented functionality remain aligned.

### Phase 6: Frontend Quality

Repair mobile navigation and layouts, accessibility and focus behavior, error
recovery, WebSocket reconnection, capability-driven administration, and bundle
performance. Complete responsive and browser-specific behavior for the support
matrix.

### Phase 7: Operations and Beta Certification

Pin release artifacts, implement CI security and license gates, publish an SBOM,
complete monitoring and incident runbooks, execute the cross-browser stateful
suite, and perform clean-node installation, restore, upgrade, rollback, and
capacity drills.

## Backend Component Boundaries

The remediation extracts only responsibilities that are actively changing from
the existing large gateway files:

- `backend/auth.go`: authentication, sessions, bootstrap, invites, password
  changes, and session/socket revocation.
- `backend/security.go`: origin policy, request limits, trusted-proxy address
  resolution, security headers, and rate limits.
- `backend/chat.go`: channels, root messages, one-level threads, reactions,
  pins, and presence.
- `backend/notifications.go`: notification creation, idempotency, pagination,
  unread counts, and read-state operations.
- `backend/annotations.go`: annotations, visibility transitions, replies, and
  moderation.
- `backend/watchparty.go`: party lifecycle, host authorization, invitations,
  and synchronized playback state.
- `backend/media.go`: Jellyfin item authorization and safe media proxying.
- `backend/files.go`: upload staging, scanning, quotas, promotion, and
  reconciliation.
- `backend/migrations.go`: migration discovery, locking, checksums, validation,
  and execution.
- `backend/health.go`: liveness, readiness, dependency, storage, migration, and
  backlog reporting.

Existing patterns remain in place where they are not part of the remediation.
The program does not perform unrelated repository-wide refactoring.

## Durable Data and Event Delivery

PostgreSQL is authoritative for all durable state. Redis is used only for
rebuildable caches, presence, pub/sub delivery, rate limiting, and short-lived
Watch Party playback state.

Any durable mutation that also needs real-time delivery writes a transactional
outbox event in the same PostgreSQL transaction. A bounded dispatcher publishes
the event through Redis. Outbox events carry unique idempotency keys, and
consumers tolerate duplicate delivery.

Clients always have a REST reconciliation path after reconnecting. Loss of
Redis cannot lose messages, notifications, annotations, invitations, list
membership, or durable Watch Party identity.

## Feature Data Flows

### Threads

- Threads have one nesting level.
- A root message belongs to a channel.
- A reply references one root message in the same channel.
- Channel history returns roots with reply counts and last-reply metadata.
- Thread replies use stable cursor pagination.
- Mentions and thread replies create durable notifications.
- Channel view and send permissions apply to both roots and replies.

### Notifications

- Notifications are durable and scoped to one recipient.
- The inbox uses stable cursor pagination.
- Unread counts are server-derived.
- Mark-one-read and mark-all-read operations are idempotent.
- Idempotency keys prevent duplicate notifications when outbox delivery is
  retried.
- Beta notification sources are mentions, thread replies, shared-annotation
  replies, Watch Party invitations, and account/security administration events.
- Delivery is in-app only; Web Push and email are outside beta scope.

### Annotations

- Annotation visibility is `private` or `community`.
- New annotations default to `private`.
- EPUB locations use validated CFI data.
- PDF locations use validated page and rectangle coordinates.
- Selected text and note bodies have explicit size limits.
- Owners may edit, delete, or change the visibility of their annotations.
- Users with the moderation capability may remove community annotations.
- Replies are permitted only on community annotations.
- Annotation reads require current library access.

### My List

- My List stores a user ID, Jellyfin item ID, explicit ordering, and timestamps.
- Insertion validates that the item belongs to the configured Telos Jellyfin
  library.
- Listing and playback revalidate item authorization.
- Missing upstream items remain identifiable to the user and can be removed
  without failing the entire list.

### Watch Parties

- PostgreSQL stores party identity, host, media item, linked chat/voice channel,
  invitations, membership, lifecycle, and timestamps.
- Redis stores the current play/pause state, position, playback rate, version,
  server timestamp, and host lease with a bounded TTL.
- Only the host may publish play, pause, seek, rate, and media-selection
  controls.
- Participants receive monotonically versioned, server-timestamped events.
- Participants may resynchronize, detach into independent playback, rejoin, or
  leave.
- Host loss pauses the party and permits an explicit authorized host transfer.
- Existing Telos chat and voice provide party communication.

### PDF and EPUB Reading

- PDF.js is dynamically loaded for PDF content.
- EPUB.js remains dynamically loaded for EPUB content.
- Reader progress is durable per user and book.
- Readers integrate with the annotation service.
- PDF range requests and long-lived book responses remain proxy-safe.

## Frontend Boundaries

Frontend modules mirror backend capabilities:

- Stores use narrow Zustand selectors rather than whole-store subscriptions.
- Voice, PDF, EPUB, and media playback dependencies are loaded only when used.
- Server-provided permissions control feature and administration visibility.
- Optimistic updates retain rollback state and surface persistence failures.
- WebSocket clients reconnect with bounded exponential backoff and reconcile
  durable state through REST.
- Authentication transport failures preserve the last known authenticated state
  and show a reconnecting/error condition rather than silently logging out.
- Failed preference and message mutations retain user input and offer retry.
- Dialogs trap focus, restore focus on close, and expose correct accessible
  names and modal semantics.
- Interactive controls use native elements or complete keyboard semantics.
- Search follows the combobox/listbox pattern with keyboard selection and
  announced result counts.
- No visible control is inert; every control performs its documented action or
  is absent.
- Authenticated initial JavaScript has an enforced bundle budget, and LiveKit,
  PDF.js, EPUB.js, and media-player chunks are excluded from routes that do not
  use them.

## Security Invariants

- Only `telos-core` may receive the `telos_session` cookie.
- Upstream requests and responses use explicit header allowlists.
- Session tokens are never accepted in URLs or query strings.
- Production is same-origin and does not emit permissive CORS headers.
- Development CORS uses an explicit configured allowlist.
- Authentication JSON bodies are limited to 16 KiB.
- Other JSON bodies are limited to 64 KiB.
- Avatar uploads are limited to 5 MiB.
- Normal file and book uploads are limited to 100 MiB.
- Request limits are applied before decoding or buffering.
- Traefik is the only trusted proxy.
- Forwarded client addresses are accepted only from the configured Traefik
  network.
- Security changes close affected local sockets immediately.
- Every socket is revalidated within 30 seconds as a fallback.
- Channel identifiers must parse as UUIDs, exist, and pass channel-specific
  permission checks before allocating subscriptions or presence state.
- Login throttling is enforced at the edge and application layers.
- Redis failure activates a documented degraded throttling policy rather than
  disabling throttling.
- Production responses include CSP, HSTS, frame, MIME-sniffing, referrer, and
  permissions-policy protections.
- Logs redact session identifiers, invite tokens, credentials, and sensitive
  query data.
- Secret validation reports only the variable name and never logs its value.
- Cryptographic random generation errors fail the operation; zero or partial
  random output is never accepted.
- Usernames use one documented canonical form and reject control characters,
  ambiguous whitespace, and invalid Unicode.
- Health and API errors return stable public codes instead of raw database,
  Redis, filesystem, or upstream error text.
- WebSocket writes use bounded queues and deadlines, and per-user/per-channel
  connection caps prevent unbounded goroutine and subscription growth.
- LiveKit grants permit microphone audio only, prohibit data/video/screen
  publication, and enforce configured participant limits.
- HTTP shutdown drains normal requests, WebSockets, outbox work, and open
  upstream bodies within documented deadlines.

## Database Invariants

- A one-shot migration service uses schema-owner credentials.
- `telos-core` uses a separate non-owner role with only required table and
  sequence privileges.
- Migration execution takes a PostgreSQL advisory lock.
- Migration versions are numeric, ordered, contiguous, and immutable.
- Applied migrations store a content checksum.
- Startup rejects changed migration history, version gaps, and unknown future
  versions.
- Security and lifecycle assumptions are backed by database constraints.
- Every foreign-key access path used for joins or deletes has a suitable
  leading index.
- Database, pool, statement, lock, idle-transaction, and request deadlines are
  explicit and tested.
- Authentication resolves the user, roles, and permissions without per-request
  query fan-out or unbounded last-seen goroutines.
- Message history, administration lists, notifications, annotations, and
  library-facing local data use stable cursor pagination.
- Search queries use purpose-built indexes or bounded upstream search rather
  than unbounded `ILIKE '%query%'` scans.
- PostgreSQL query statistics are enabled without exposing statement parameters
  to untrusted users.

## Account Deletion and Retention

The implementation publishes a retention matrix covering every durable table
and physical asset.

- Private annotations, preferences, progress, notifications, sessions, reads,
  reactions, list entries, invitations owned by the user, and private uploads
  are deleted.
- Physical avatar and private-upload assets are removed after the database
  transaction records the deletion job.
- Public chat messages and community annotations may be retained only in
  anonymized form according to the documented community policy.
- Every session and socket is invalidated before deletion reports success.
- Deletion jobs are idempotent and reconcile database and filesystem state.

## Storage and Network Invariants

- Uploads stage into exclusive temporary files.
- Hashing, content validation, and malware scanning complete before promotion.
- EPUB validation examines the ZIP structure, required mimetype entry, and
  bounded archive expansion rather than relying on browser MIME detection.
- Promotion uses no-replace semantics and verifies copy, flush, close, and
  rename results.
- Database and filesystem changes have reconciliation jobs for interrupted
  operations.
- Per-user and node-wide quotas preserve configured free-space reserves.
- Filesystem confinement rejects symlink traversal and performs sensitive opens
  relative to trusted directory descriptors.
- Search and download expose only clean files of the intended product purpose;
  malware records, avatar assets, and book-ingestion staging are not returned as
  general shared files.
- File create, move, delete, download, and administrative moderation actions
  generate durable audit records.
- Pagination arithmetic rejects overflow and pathological page values.
- ClamAV connections use read and write deadlines in addition to a connect
  deadline.
- ClamAV, Jellyfin, and Grimmory receive narrowly controlled outbound access for
  signatures and metadata.
- Jellyfin and Grimmory administrative endpoints remain unreachable from public
  interfaces.
- Every Jellyfin cover, metadata, audio, video, and Watch Party operation
  verifies that the requested item belongs to the configured Telos library.
- Range and conditional headers are forwarded only where explicitly supported.

## Backup, Restore, and Release Operations

- Backups are encrypted before leaving the node.
- Automated retention keeps 14 daily and 8 weekly generations.
- Backup output includes database dumps, service configuration, ACME state,
  shared storage, a release/version manifest, and checksums.
- A backup is not successful unless every required component completed.
- Restore verifies checksums and compatible release/schema versions before
  altering live state.
- Restore stops required writers without ignoring failures.
- Restored sessions and invites are invalidated.
- Redis is cleared before reopening the application.
- Functional verification covers health, authentication, recent chat, one
  media item, one EPUB, one PDF, annotations, notifications, and Watch Party
  creation.
- Clean-node restore drills demonstrate RPO at most 24 hours and RTO at most
  4 hours.
- Releases use digest-pinned images, reproducible dependency installation,
  vulnerability and license reports, an SBOM, and documented rollback
  compatibility.
- Containers run as explicit non-root users wherever supported, use
  `no-new-privileges`, drop unused capabilities, prefer read-only root
  filesystems, and declare CPU, memory, process, and file-descriptor limits.
- Secret files are mode `0600`; backup output locations are ignored by version
  control and cannot default into a committable repository path.
- Edge access logs redact sensitive data and have documented rotation and
  retention.

## Health and Observability

Readiness covers:

- PostgreSQL connectivity and migration state.
- Redis connectivity.
- ClamAV availability and signature freshness.
- Jellyfin and Grimmory availability.
- LiveKit availability.
- Required storage mounts, writability, and free-space reserve.
- Transactional outbox age and backlog.

Health responses do not expose raw infrastructure errors. Structured logs carry
request and event correlation identifiers. Metrics and alerts cover dependency
loss, authentication throttling, socket counts, outbox lag, upload rejection,
free space, backup age, and restore-drill age.

## Product Truth and Administrative Contracts

- Landing-page privacy copy distinguishes direct HTTPS access, optional private
  network access, and operator-enabled external metadata providers.
- Claims that nothing leaves the node are removed unless a deployment actually
  disables every external integration.
- Documentation, runtime behavior, and settings UI use the same feature names,
  theme names, permissions, and service inventory.
- The mandatory Credits surface lists Jellyfin, Grimmory, LiveKit, and
  infrastructure dependencies with source and license information.
- Apache-2.0 `LICENSE` and applicable `NOTICE` content ship with source and
  release artifacts.
- OIDC is documented as outside beta scope and is not presented as an available
  integration.
- `manage_channels` receives working channel CRUD and role-override management,
  or the permission is removed; the approved design chooses working management.
- `manage_members` documentation describes only shipped member-management
  actions. Administrative password replacement is not part of beta; operators
  may disable an account and the member may change a known current password.
- Custom roles see every settings and media-management surface granted by their
  effective permissions.
- Traefik is the only published HTTP entry point for the Telos production
  stack. Host-level beta certification also checks unrelated services and
  firewall exposure.

## Verification Strategy

Every implementation task begins by reproducing the original failure or adding
a failing acceptance test. A finding is not closed solely because code changed.

Automated gates include:

- Go unit and integration tests with disposable PostgreSQL and Redis.
- Go race tests and `go vet`.
- At least 70 percent backend statement coverage overall.
- At least 90 percent coverage for authentication, authorization, migrations,
  uploads, proxy boundaries, deletion, outbox, and synchronization logic.
- Frontend unit and component tests using Vitest and Testing Library.
- Playwright projects for Chromium, Firefox, WebKit, Edge, desktop, and mobile
  viewport/touch behavior.
- Manual final checks on current Safari/iOS and Chrome/Android devices.
- WCAG 2.2 AA automated checks plus keyboard, focus, reduced-motion, zoom, and
  contrast scenarios.
- Empty-database, live-schema-upgrade, checksum-rejection, concurrent-startup,
  and unknown-future-migration tests.
- Stateful end-to-end coverage for authentication, invitations, role changes,
  socket revocation, threads, notifications, annotations, reading, My List,
  Watch Parties, uploads, malware rejection, deletion, backup, and restore.
- `govulncheck`, production npm audit, secret scanning, license auditing,
  container scanning, dependency review, and SBOM generation.
- Pinned-container capacity tests for the approved beta targets.
- Clean-node installation, upgrade, restore, and rollback drills.

## Fable 5 Medium Execution Contract

- Execute one phase per inline session.
- Execute one reviewer-sized task at a time.
- Each task names exact files, interfaces, test commands, expected results, and
  its focused commit.
- Use test-driven development for every behavior change and regression.
- Do not continue after a failed step or phase exit gate.
- Stop for review after every task and every phase.
- Later phases consume only interfaces and migrations accepted by earlier
  phases.
- The master plan maps every audit finding and beta requirement to a task and a
  verification gate.

## Completion Criteria

Telos is beta-ready only when:

- Every audit finding is mapped to a closed task with passing acceptance
  evidence.
- Every visible non-AI feature in this design is functional.
- No AI or Oracle functionality remains visible or advertised.
- The complete stateful browser matrix passes or has a documented,
  user-approved exception.
- Vulnerability, secret, license, container, and SBOM gates pass.
- A clean checkout builds and installs without untracked inputs.
- A clean-node restore meets the approved recovery objectives.
- The capacity test meets the approved small-community target.
- Normative documentation matches the shipped implementation.
