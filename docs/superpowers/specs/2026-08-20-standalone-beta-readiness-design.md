# Standalone Client/Server Beta Readiness — Design

**Original design:** 2026-08-20. **Remediated:** 2026-10-13 against source `237692fc5da5654f95d1582cd1789b6491d22283` and the platform-scope work in this checkout. **Status:** implementation target; release not certified.

## Purpose

Telos is a community-owned library and discussion space: members read, watch, listen, store and discuss through four modules (Chat, Stream, Library, Files), with Settings and commentary attached to media. Its “your server, your community” promise requires more than working screens. An operator must be able to install, secure, recover and upgrade the whole service without losing control of data or depending on undocumented developer knowledge.

The architecture is suitable for that purpose. It keeps the application gateway and client under one product model while isolating mature media/catalog services behind HTTP. The remaining work is delivery integration and operational proof, not a product rewrite. Concentrating server support on Ubuntu reduces the number of runtime, filesystem and recovery combinations that must be proven.

## Binding scope

[Platform policy](../../../documentation/product/platform-scope.md) governs all work. Server: Ubuntu 24.04+ only, initially x86-64 and Podman 5+; 24.04 is the reference certification host, newer combinations require verification. Windows, macOS, WSL/Docker Desktop and other Linux server distributions are excluded.

First clients: hosted browsers and unsigned Linux x86-64 Tauri desktop on Ubuntu. Browser support includes Chrome, Firefox, Edge, actual desktop Safari, iOS Safari and Android Chrome; automated WebKit is additional coverage. Future Windows/Mac native clients retain the same server protocol, but do not block initial delivery. Native mobile, public desktop signing/notarization, high availability and self-signed TLS bypasses are deferred.

## Current baseline versus target

The source already includes contiguous checksum-verified migrations, Linux `openat2` storage confinement, production HTTPS-origin checks, transactional refresh rotation/replay revocation, central API and WebSocket clients, four module surfaces and cross-media commentary. Preserve those foundations.

The assembled deployment is unfinished:

- `backend/frontend_noembed.go` deliberately serves no frontend, while production Compose has no separate `telos-client`; Traefik's browser catch-all still points to core.
- `.env.example` and Compose name cursor/HLS secret paths without provisioning/mounting the required files. Configuration is tied to the checkout rather than a stable installed-node path.
- `cli/commands/init.sh` leaves upstream credentials/library selections incomplete; ordinary gateway startup requires them. Guided validation must bridge that gap.
- `backend/main.go` puts device registration behind `withAuth`, preventing the handler's unauthenticated password exchange. Native login, CORS bearer preflight and refresh integration are incomplete.
- `frontend/src-tauri/src/lib.rs` persists secrets through a JSON store; its separate certificate fingerprint probe does not enforce the transport actually used by fetch.
- Direct native media and image elements cannot attach bearer headers. HLS locators must authorize their own requests with current issuer revocation checks.
- Backup, generation lifecycle and runtime certification remain unfinished. The current backup cleanup does not provide the required encrypted off-node recovery guarantee.

Earlier assertions that certification scripts still report unearned success are obsolete: envelope v2, catalog v1, fail-closed wrappers and runner containment now exist. Phase 1 must reverify and close residual gaps instead of recreating them. Product backup status is already honestly blocked. Current artifact/runtime evidence integration is still insufficient for final release certification; Phase 5 explicitly migrates it.

## Delivery architecture

Traefik terminates platform-trusted HTTPS and is the only published Telos listener. API routes go to headless core; browser routes go to a separate read-only, unprivileged static-export service on the ingress network. Browser API access remains same-origin with cookies and CSRF controls. Native clients use an explicitly allowed Tauri origin, bearer access tokens and device credentials.

Production uses rootful Podman through the authoritative Bash CLI, while core and static-client containers run as fixed non-root identities. Durable `node.env`, signing files and operator state live under `/var/lib/telos`; immutable generations live under `/srv/telos/generations` with an atomic current pointer. These are target layout changes, not current installation guarantees. The [shared contracts](../plans/2026-10-13-standalone-beta-handoff-contracts.md) specify identities, helper functions, modes and migration rules.

`telos init` creates distinct cursor/HLS roots, refuses overwrite and validates readable mounts. Existing audiobook and new direct-audio signing use separate derivation domains from the HLS root. Doctor recommends targeted repairs rather than regenerating durable secrets.

Guided setup temporarily exposes upstream administration only on loopback, reachable remotely through SSH forwarding. The operator creates upstream accounts/libraries in their UIs; `telos configure integrations` validates credentials over container networks, discovers library IDs and atomically persists selected configuration without exposing secrets. Validate the exact pinned Grimmory response before implementing its decoder. Stop the setup overlay before normal start.

## Client completion

Native registration reaches handler-owned password/session validation, rate limiting and audit logic. Token login stores only the refresh credential in Linux Secret Service; access tokens remain in memory. Startup rotates stored credentials. The fetch layer deduplicates refresh, retries once, clears rejected credentials and preserves them on transient failures. Tests cover credential-write/delete failures and retirement of the legacy plaintext store.

Platform trust is the sole TLS policy. Remove the independent permissive fingerprint probe and misleading pin UI. An operator private CA must be installed in the relevant trust stores; there is no application bypass.

Media issuers return signed URLs for native HLS/direct audio. Spend-time checks validate signature, expiry, resource, issuing account/session/device state and current authorization. URLs are bearer capabilities: copied bytes remain usable until expiry/revocation, so “reject replay from another device” without additional authentication is not a valid acceptance test. Phase 3 defines renewal, Range/HEAD and authenticated artwork behavior. Logs and evidence must not expose capabilities.

Linux native packaging and real desktop journeys are initial release gates. Windows/Mac client portability remains a later task, never a reason to add their server support or require their native runners now.

## Operational safety

Pin production and build images to verified digests. Inspect actual runtime IDs, privileges, mounts, networks, listeners and limits; Compose text alone is not proof. Keep service-specific writable paths bounded. Rootful host operation does not excuse root core/client containers.

Encrypted off-node restic backups retain 14 daily and 8 weekly snapshots. A maintenance lock and durable journal serialize backup/restore/upgrade. Quiesce writers for a consistent snapshot, verify uploaded contents, and remove plaintext on normal failure, signals and restart recovery. Measure RPO from the consistency point, not upload completion.

Restore to a distinct reference host, invalidate sessions/device credentials/invites before traffic, and verify all product journeys. Required RPO is at most 24 hours and RTO at most 4 hours. Same-host rollback is separately labeled and cannot satisfy separate-host recovery. Upgrade validates predecessor/schema compatibility and uses verified pre-upgrade backup, one-shot migrations, atomic activation and bounded readiness. Incompatible rollback restores data instead of blindly switching binaries.

Capacity target is 100 registered members, 25 concurrent authenticated users and 10 chat/commentary events per second, with Phase 4's latency/error/resource limits and legal media fixtures. Monitoring, host exposure, controlled egress and candidate supply-chain checks are observed, sanitized evidence.

## Evidence and execution strategy

There are three distinct milestones: implemented and locally tested source; non-certifying infrastructure rehearsal; final immutable candidate certification. A missing host, key, browser or runtime does not prevent unrelated coding, but cannot count as passed evidence.

Reconcile Phase 1 first. Implement Phase 5 schema/runner contracts early, and its builder tooling alongside browser/operations work. Complete all Phase 2–4 product changes and remaining release tooling before freezing. This avoids requiring operational fixtures to consume a builder that cannot yet be implemented.

The hash graph is acyclic: artifact payload/SBOMs → index → manifest → candidate lock → evidence → signed certification. Payload generation metadata contains no downstream candidate digest; installation writes a separate receipt. One runner writes envelope v2; producers write validated result/subject sidecars. Runtime subjects reflect inspected containers, not a hash of desired configuration. The aggregate certificate is excluded from its own input evidence.

Only one clean reviewed source and frozen artifact set may be certified. Relevant source/artifact fixes produce a new candidate. Source tests may compile scratch output, but never replace frozen release bytes. Publishing promotes exactly the tested artifacts after freshness, signature and publisher checks.

## Implementation and acceptance

The [cold handoff](../plans/2026-10-13-standalone-beta-cold-handoff.md) defines first action, work order, external prerequisites and resume records. The [master plan](../plans/2026-08-20-standalone-beta-readiness-master.md) links the five detailed phase tasks. The shared contracts own cross-phase interfaces; detailed plans own files, tests and commands.

Success is a real clean Ubuntu installation, complete browser/Linux client journeys, secure credential/media behavior, proven encrypted recovery and upgrade, and one candidate with every required gate fresh and passed. Existing code volume or fixture success alone cannot establish that outcome. The concrete seams and bounded tests make the project implementable; live operational and browser verification will determine when it is deliverable.
