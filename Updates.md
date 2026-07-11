# Updates.md — Secure Multi-User Backend Implementation Plan

## Summary

Turn Telos into a secure, internet-facing, invite-only community service for roughly 10–15 users. The first release is backend-only: restore a reproducible gateway image and provide secure APIs; rebuilding the deleted frontend is explicitly deferred.

This plan replaces the current spoofable query-parameter identities, optional token checks, seeded admin identities, unauthenticated media/voice access, missing file implementation, and non-deployable image. Existing demo data is disposable; take an operator backup, then install the secure schema cleanly.

## Identity, Sessions, and Permissions

- Replace startup DDL/seeding with versioned SQL migrations. Create:
  - `users`: UUID ID, lowercase unique username, Argon2id password hash, active/disabled state, timestamps.
  - `sessions`: hashed opaque session token, user ID, creation/expiry/revocation metadata.
  - `invites`: hashed one-time token, creator, expiry, use/revocation metadata.
  - `roles`, `role_permissions`, `user_roles`, `channels`, `channel_role_overrides`, and `messages`.
  - `files` and file audit/status fields for the shared library.

- Remove all seeded identities and hardcoded Host/Admin roles. Bootstrap the sole initial Owner through `TELOS_BOOTSTRAP_TOKEN`; accept it only while no Owner exists, consume it after successful setup, and never expose it in URLs or logs. Owner password recovery is handled by an Owner/Admin reset action; no email recovery is in scope.

- Implement local accounts:
  - Usernames: normalized lowercase ASCII, 3–32 characters.
  - Passwords: 15–128 characters, hashed with Argon2id (`m=19456`, `t=2`, `p=1`); never truncate or log passwords.
  - Login throttling: rate-limit by username and client IP through Redis; return generic failures.
  - Issue an opaque, cryptographically random session token in a `Secure`, `HttpOnly`, `SameSite=Strict` cookie. Store only its SHA-256 digest. Expire sessions after 12 hours, rotate on login, and revoke all user sessions on password reset, disablement, or logout.
  - Remove permissive CORS; enforce same-origin requests and CSRF validation for every state-changing HTTP endpoint.

- Add these public APIs:
  - `POST /api/v1/auth/bootstrap`, `POST /auth/login`, `POST /auth/logout`, `GET /auth/me`.
  - `POST /api/v1/auth/invites/accept`.
  - Owner/Admin APIs to create/revoke invites, disable members, reset passwords, create/edit roles, and manage channel overrides.
  - All other APIs, including WebSockets, derive the user from the session cookie—never `user`, `token`, or role query parameters.

- Define roles and permissions:
  - Reserved roles: Owner, Administrator, Moderator, Member, Contributor, and Librarian.
  - Permissions: community/member/role/channel management; view/send/moderate chat; join voice; view media/library/files; upload files/books; manage files.
  - Members receive only view/send/join/view permissions. Contributor grants uploads; Librarian grants file management; Moderator grants moderation; Administrator and Owner bypass channel overrides.
  - Channel overrides are role-based only. Explicit denial wins over role allowance for non-administrators.

## Gateway, Chat, Media, Files, and Voice

- Make `telos-core` fail closed. If PostgreSQL, Redis, Jellyfin, Grimmory, or LiveKit dependencies required by a route are unavailable, return a safe `503`; remove mock media responses and the unauthenticated WebSocket fallback.

- Rebuild chat around authenticated authorization:
  - Replace string demo IDs with UUIDs and require a text channel plus `view_channel` before history/subscription and `send_messages` before writes.
  - Add message limits (4,000 characters; 8 KiB WebSocket frame), cursor-based history, and the `(channel_id, created_at DESC, id DESC)` index.
  - Keep PostgreSQL as durable truth. Insert a message transactionally, then publish its event. Use one Redis subscription per active channel per gateway instance, bounded per-client send queues, ping/pong heartbeats, read/write deadlines, and slow-client disconnection.
  - Expose filtered channel/message APIs; change WebSocket access to `GET /api/v1/chat/ws?channel=<uuid>` with cookie authentication only.

- Keep Jellyfin and Grimmory server-wide, not per-user, in this release:
  - Require `view_media` or `view_library` at the gateway before any proxy request.
  - Remove their public Traefik routes and detach them from the ingress network; only `telos-core` communicates with them over the backend network.
  - Continue using a service credential internally, with bounded HTTP-client timeouts and safely encoded upstream path/query values.
  - Remove the unimplemented OIDC claims from the normative docs; per-user Jellyfin/Grimmory federation is a later project.

- Implement a server-wide shared file library:
  - APIs: paginated `GET /api/v1/files`, multipart `POST /files`, `GET /files/{id}/download`, and `DELETE /files/{id}`. Add a book-upload target requiring `upload_books`.
  - Permit only PDF, EPUB, JPEG, PNG, WebP, MP3, M4A, OGG, WAV, MP4, and WebM. Enforce a 100 MiB file limit, extension allowlist, MIME/magic-byte validation, generated storage names, and 8 KiB filename limit.
  - Store uploads outside the web root in a staging directory. Scan with an internal ClamAV service; only clean files move atomically to shared-library storage or Grimmory’s `bookdrop`. Reject archives, executables, malformed files, oversized content, and scan failures.
  - Store file metadata, SHA-256, uploader, scan state, and storage key in PostgreSQL. Downloads require `view_files`, stream through the gateway, preserve valid range requests, and set safe attachment/content-sniffing headers.
  - Mount only upload staging and `bookdrop` into `telos-core`; remove its read-write mount of all shared media/book storage.

- Harden voice around LiveKit:
  - Replace the hand-rolled JWT with the maintained LiveKit Go server SDK.
  - Add `POST /api/v1/voice/channels/{id}/token`; validate session, voice-channel type, `join_voice`, and a 15-participant room cap before creating/fetching the room and returning a five-minute, audio-only token.
  - Use the internal user UUID as the LiveKit identity. Do not grant arbitrary room, identity, video, recording, or data-publish access.
  - Remove the duplicate Go `/livekit` proxy. Traefik alone routes signaling to LiveKit; direct RTP remains on its required ports.
  - Enable embedded TURN with `turn.<TELOS_DOMAIN>`, TURN/TLS on 5349, UDP TURN on 3478, and the existing UDP media range. Configure DNS, firewall rules, and ACME certificate export so LiveKit receives a current read-only TURN certificate.

## Deployment, Supply Chain, and Documentation

- Repair the gateway build without restoring the deleted frontend: replace the ignored embedded `backend/out` dependency with a committed, minimal backend status page or remove static embedding entirely. The image must build from a clean checkout.

- Make Traefik the only HTTP ingress:
  - Remove host port `8080`.
  - Configure HTTP-to-HTTPS redirect, ACME using required `ACME_EMAIL`, HSTS, security headers, upload limits, and no debug logging.
  - Move routes from Compose labels to Traefik’s file provider, then remove the Podman/Docker socket mount. Publish only 80/443 plus LiveKit’s 7881/TCP, 3478/UDP, 5349/TCP, and UDP media range.
  - Pin every image by version and digest. Use non-root users, read-only root filesystems where feasible, dropped capabilities, `no-new-privileges`, health checks, resource limits, and internal-only service networks.

- Replace environment-secret injection with mounted Podman secrets or `*_FILE` inputs for database, Redis, LiveKit, Jellyfin, Grimmory, bootstrap, and ACME credentials. Add startup validation that rejects placeholders and missing production secrets.

- Add structured redacted logs, readiness/liveness endpoints, internal metrics, encrypted nightly PostgreSQL/MariaDB/file backups with seven-day retention, and a documented restore drill.

- Add Apache-2.0 licensing, `SECURITY.md`, contribution guidance, dependency/license/SBOM scanning, image vulnerability scanning, and CI checks. Update normative documentation so it matches the secured gateway-only routing, local account model, shared library, and backend-only release scope.

## Verification and Acceptance Criteria

- A clean checkout builds the gateway image and starts the Compose stack; no frontend artifact is required.
- Fresh migration creates no demo accounts; bootstrap succeeds once, rejects reuse, and creates the only Owner.
- Login, logout, expiry, throttling, disabled-user handling, password reset, session revocation, CSRF rejection, and cookie flags are integration-tested.
- Permission tests prove that an unprivileged user cannot view/send in restricted channels, create invites, upload/manage files, access media, or mint voice tokens; Owner/Admin behavior and deny precedence are covered.
- WebSocket tests cover authentication, invalid channel/type rejection, message limits, reconnect history, slow-reader cleanup, and Redis fanout.
- File tests cover authorization, oversized input, traversal names, spoofed MIME/extension, malicious EICAR detection, failed scans, atomic promotion, range downloads, and bookdrop handoff.
- LiveKit integration tests prove unauthorized joins fail, authorized audio-only joins succeed, the sixteenth participant is refused, and TURN/TLS connectivity works from a restrictive-network test.
- Deployment tests confirm no public `8080`, Jellyfin/Grimmory are unreachable externally, TLS renews through ACME, images are pinned, health checks work, and a backup restores into a clean environment.
- Run a 15-user chat and audio-only voice load test before release; no recording, personal storage, email recovery, MFA, frontend rebuild, HA, or legacy-data preservation is included in this release.

## Assumptions

- Telos is public on the internet, invitation-only, and accessed through trusted HTTPS.
- Local passwords are the only member authenticator; MFA is intentionally out of scope for this release.
- The shared library is server-wide, not private or channel-scoped.
- The current database contents are prototype data and may be discarded after an operator backup.
- One gateway and one LiveKit node are sufficient for 10–15 active users; the design retains Redis-backed foundations for later scaling.
