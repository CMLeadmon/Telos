# Standalone Client/Server Beta Readiness — Design

**Date:** 2026-08-20

**Status:** Approved

## Objective

Make Telos ready for an invite-only beta without reversing the standalone
client/server architecture. The beta ships one Linux server distribution, one
separately built browser client hosted by that server, and unsigned Tauri v2
desktop clients for Linux, Windows, and macOS. Completion means one immutable
candidate has passed real installation, member-journey, recovery, security,
capacity, exposure, browser, and desktop-client gates; fixture success or a
missing external environment never counts as candidate evidence.

The reference server platform is Ubuntu 24.04 LTS on x86-64 with Podman 5 or
newer. Other Linux distributions remain best-effort for this beta. Native iOS
and Android packages, Windows-hosted backend deployment, public desktop signing
and notarization, self-signed-certificate support, and high availability are
outside this program.

## Current-State Findings

Telos already has the foundations this program should preserve:

- Migration discovery requires contiguous numbering and computes a checksum for
  every file (`backend/migrations.go:70-111`); applied history must remain an
  immutable prefix (`backend/migrations.go:114-130`).
- Linux storage uses `openat2` with `RESOLVE_BENEATH`, `RESOLVE_NO_SYMLINKS`, and
  `RESOLVE_NO_MAGICLINKS` (`backend/storage.go:34-43`) and refuses to fall back
  when the kernel cannot provide that boundary (`backend/storage.go:65-86`).
- Production security configuration requires an exact HTTPS public origin and
  sets bounded request sizes and concurrency (`backend/security.go:96-121`).
- Device refresh credentials are hashed in the database, rotated
  transactionally, and replay-revoked (`backend/devices.go:183-266`).
- The frontend already centralizes HTTP base URL and bearer handling in
  `frontend/src/lib/api.ts:153-180`, while WebSocket ticketing lives in
  `frontend/src/lib/deviceAuth.ts:103-119`.

The current branch is not a releasable integration of those parts:

- `telos init` deliberately leaves the Jellyfin token blank and the two library
  allowlists for later provisioning (`cli/commands/init.sh:168-181`), while the
  production gateway rejects a missing Jellyfin token
  (`backend/main.go:613-628`) and missing Jellyfin or Grimmory library IDs
  (`backend/main.go:345-367`).
- `.env.example` names cursor and HLS files under `/run/telos`
  (`.env.example:33-41`), but the gateway service passes only those path strings
  and mounts no secret directory (`docker-compose.yml:148-161`).
- The backend image is headless by default (`backend/Dockerfile:1-4`), Compose
  does not select the embedded variant (`docker-compose.yml:114-118`), and the
  headless frontend registration is intentionally empty
  (`backend/frontend_noembed.go:7-9`). No separate web-client service fills the
  resulting root route.
- Device registration contains the required password exchange
  (`backend/auth.go:504-555`) but its route is behind `withAuth`
  (`backend/main.go:457`), whose middleware rejects a request before that
  exchange runs (`backend/main.go:881-887`). The client login store still uses
  cookie login unconditionally (`frontend/src/stores/useAuthStore.ts:63-70`).
- Cross-origin preflight permits `Content-Type` but not `Authorization`
  (`backend/security.go:277-291`), and Compose does not pass
  `TELOS_ALLOWED_ORIGINS` to `telos-core` (`docker-compose.yml:134-157`).
- The native shell stores refresh credentials and certificate pins in
  `telos-secrets.json` through `tauri-plugin-store`
  (`frontend/src-tauri/src/lib.rs:6-7`, `frontend/src-tauri/src/lib.rs:59-86`),
  contrary to the approved OS-keychain requirement
  (`docs/superpowers/specs/2026-08-06-s4-connection-layer-design.md:11-14`).
- The connection page performs an ordinary WebView health fetch before its
  independent permissive TLS fingerprint probe
  (`frontend/src/app/connect/page.tsx:82-96`), and application requests continue
  through WebView `fetch` (`frontend/src/lib/api.ts:180`). The stored fingerprint
  therefore does not enforce the transport used by credentials or content.
- WebKit native HLS assigns the stream URL directly to the media element
  (`frontend/src/components/stream/HlsPlayer.tsx:44-56`), while the locator route
  still requires session or bearer authentication (`backend/main.go:526`). A
  media element cannot attach the bearer header, so token-mode video fails on
  macOS WebKit.
- The product matrix claims encrypted off-node backups are shipping
  (`documentation/product/beta-feature-status.md:27`), but the runbook explicitly
  says encryption, retention, atomicity, and proven RPO/RTO remain incomplete
  (`documentation/operations/backup-and-restore.md:6-11`). The current plaintext
  cleanup trap performs no cleanup (`scripts/backup.sh:17-24`).
- Certification, hardening, capacity, exposure, browser, release, and evidence
  scripts can print success without doing their declared work
  (`scripts/certify-beta.sh:18-24`, `scripts/verify-runtime-hardening.sh:22-33`,
  `scripts/run-capacity-drill.sh:18-24`,
  `scripts/certify-browser-matrix.sh:18-24`).

## Locked Product and Delivery Decisions

| Decision | Beta choice |
|---|---|
| Server architecture | Headless `telos-core` API and internal services on one Linux node |
| Browser delivery | Separate `telos-client` static container on the same public origin |
| Desktop delivery | Tauri v2 packages for Linux, Windows, and macOS |
| Mobile delivery | Responsive browser support only; native iOS/Android packages deferred |
| Server host support | Ubuntu 24.04 LTS, x86-64, Podman 5+ is the certified reference |
| Operator interface | Existing Bash `telos` CLI is authoritative for Linux beta |
| Upstream setup | Guided manual Jellyfin/Grimmory first run plus automated validation |
| TLS | Platform-trusted HTTPS only; public ACME or an operator-installed private CA |
| Native credentials | Refresh token in OS credential storage; access token in memory only |
| Desktop signing | Unsigned invite-only beta packages with checksums and explicit OS instructions |
| Support envelope | 100 registered members, 25 concurrent authenticated users, 10 chat/commentary events per second |
| Recovery | Encrypted off-node backups, 14 daily and 8 weekly retention, RPO at most 24 hours, RTO at most 4 hours |

These decisions supersede the TOFU/self-signed-certificate portions of
`docs/superpowers/specs/2026-08-06-s4-connection-layer-design.md` and the native
mobile distribution portions of
`docs/superpowers/specs/2026-08-06-s5-tauri-shell-design.md`. They do not remove
the headless build, API contract, device-token model, single frontend codebase,
or desktop Tauri shell established by that program.

## Delivery Strategy

Work proceeds as five dependency-ordered programs:

1. Restore trustworthy product and gate truth.
2. Produce a complete clean-Linux-node browser deployment.
3. Complete the desktop device-authentication and media path.
4. Complete operational safety and runtime hardening.
5. Freeze and certify one immutable candidate.

The first two programs form the browser beta lane. The desktop program begins
only after the server's registration, CORS, and API contracts are stable. Work
inside a program may run in parallel when it does not share a contract or
candidate artifact, but certification never begins against partially integrated
outputs.

The execution documentation is a plan family rather than one unreviewable
checklist: one master plan fixes phase dependencies, shared contracts, and the
candidate boundary, while each of the five programs receives its own detailed
task plan. Each phase ends in independently testable software or a deliberately
fail-closed gate and can be reviewed before the next phase consumes it.

## Server and Browser Architecture

### Static client service

Add a dedicated `telos-client` image that performs `npm ci` and `npm run build`
from the committed frontend lockfile, then serves only the static export from an
unprivileged, read-only runtime. It joins `telos-ingress` and has no database,
cache, storage, Docker socket, or backend-network access.

The existing high-priority Traefik API routers continue to send authentication,
streaming, uploads, and other `/api/v1/*` paths to `telos-core`
(`config/dynamic/routes.yaml:3-53`). The current catch-all router sends every
remaining path to `telos-core` (`config/dynamic/routes.yaml:55-66`); it changes
to the static client service. Browser API calls remain same-origin, so the
browser keeps strict cookies and does not depend on production CORS. Direct
container ports remain unpublished; Traefik is the only public listener.

The gateway keeps both build variants. Headless remains the default for API
tests and standalone backend artifacts, and the embedded build remains an
explicit compatibility variant. The production Compose distribution uses the
standalone static-client service.

### Node configuration and signing keys

`telos init` creates a mode-`0700` configuration directory beneath the installed
node, writes independently generated cursor and HLS secrets as mode `0600`, and
places only their container paths in `.env`. Compose mounts the containing
directory read-only at `/run/telos`. Initialization validates that generated
keys differ and refuses to overwrite an existing node. `telos doctor` reports a
specific repair command for a missing file, bad mode, placeholder, incomplete
integration, or unreadable mount; it never recommends regenerating all secrets
on a node with durable volumes.

### Guided upstream configuration

The beta does not automate third-party account or library creation. A
loopback-only setup overlay exposes Jellyfin and Grimmory administration ports
to `127.0.0.1`; a remote operator reaches them through an SSH tunnel. The
operator completes each upstream's first-run UI and creates the gateway
credential and libraries manually.

`telos configure integrations` then prompts without echo for the Jellyfin token
and Grimmory password, discovers accessible libraries through the upstream HTTP
APIs, requires the operator to select at least one stable ID for each service,
and validates the final tuple from the same container networks the gateway will
use. It writes `.env` atomically without placing secrets on command lines or in
logs. The setup overlay is stopped before normal production start.

Normal `telos start` refuses incomplete integration configuration. Runtime loss
of an already configured upstream does not take down authentication, Chat, or
Files: readiness and affected handlers report the existing explicit degraded
or 502/503 states, and the client identifies the unavailable module.

## Desktop Client Architecture

### Device authentication

`POST /api/v1/auth/devices/register` becomes deliberately public at the route
table while retaining its handler-owned password verification, Redis-backed
rate limiting, device metadata validation, and audit event. Cookie-authenticated
browser registration remains supported by the same handler. Integration tests
exercise the production middleware chain so an accidentally restored
`withAuth` wrapper fails immediately.

The frontend auth store branches on configured mode:

- cookie mode calls `/api/v1/auth/login`, then `/api/v1/auth/me`;
- token mode calls `registerDevice`, stores the refresh token through the native
  credential bridge, keeps the access token in process memory, then calls
  `/api/v1/auth/me` with the bearer token;
- startup rotates a stored refresh token before entering the shell;
- a refresh rejection deletes the credential and returns to login, while a
  network or 5xx failure retains it for retry;
- logout revokes the current device credential and clears both memory and OS
  storage.

Production CORS remains deny-by-default. The operator must list the exact Tauri
origins in `TELOS_ALLOWED_ORIGINS`; Compose passes that value to the gateway,
and preflight allows `Authorization` and `Content-Type` only for an approved
origin. Wildcards remain invalid. Browser-cookie requests retain exact-origin
CSRF checks.

### Credential storage and TLS truth

Replace the JSON store commands with a Rust credential adapter backed by Windows
Credential Manager, macOS Keychain, and Linux Secret Service. The adapter keys
entries by normalized node origin and account/device identity. Refresh tokens
never enter local storage, IndexedDB, frontend logs, command-line arguments, or
package artifacts. Access tokens remain memory-only.

Remove the independent accept-any-certificate probe, certificate-pin store, and
UI claims. `/connect` validates a node through an ordinary HTTPS health request;
the platform trust store is authoritative. Certificate failures remain hard
connection failures with guidance to use ACME or install the operator's private
CA into the operating-system trust store. The beta does not expose an override.

### Token-safe media

HLS resource locators become self-authorizing bearer URLs. Spending a locator
verifies its signature, item, user, device/session binding, resource, expiry,
current account/device state, and `view_media` permission before proxying bytes.
The route no longer requires a separate cookie or header. Locator expiry covers
the maximum supported playback session while spend-time authorization ensures
logout, account disable, device revocation, and permission loss terminate later
requests. HLS and audiobook signing keys remain distinct.

The hls.js path continues to attach bearer headers. WebKit native HLS uses the
signed URL path and therefore needs no header. Regression tests cover expiry,
tampering, cross-user/device replay, revocation, logout, and long playback.

### Desktop build and distribution

CI runs Rust formatting, linting, tests, and a desktop Tauri build on native
Ubuntu, Windows, and macOS runners. Each runner produces only formats native to
that host. Packages remain unsigned for this invite-only beta; the release
bundle includes SHA-256 checksums, source/candidate identity, and instructions
for the operating-system warning that invited testers will encounter. Public
distribution, Authenticode, Apple signing, and notarization require a later
release decision.

## Product and Evidence Truth

Before implementing new release machinery, every placeholder command that can
claim success changes to a nonzero fail-closed result with a machine-readable
`not_run` reason. Provisioning scripts either perform and verify their declared
operation or direct the operator to the guided CLI without printing completion.

Update the product matrix to the four implemented modules in
`frontend/src/components/AppShell.tsx:31-36`, the completed audiobook surface,
the standalone client/server delivery model, the honest backup state, and the
desktop beta boundary. Remove the stale LiveKit target and proxy remaining in
`frontend/dev-server.mjs:15-33`. Extend product-truth checks so these structural
claims are derived from module, route, Compose-service, dependency, and script
inventories rather than prohibited-word scans alone.

Certification evidence has exactly three states: `passed`, `failed`, and
`not_run`. Only `passed` satisfies a gate. Every report records the source
commit, candidate-lock digest, command arguments, start/end time, exit status,
output digest, tool versions, and inspected artifact or runtime identity.
Evidence is written atomically only after the declared check finishes and never
repairs or rewrites an earlier record.

## Operational Safety and Runtime Hardening

### Container and supply-chain policy

Replace mutable production tags such as ClamAV, Jellyfin, and Grimmory
(`docker-compose.yml:248-270`, `docker-compose.yml:308-310`) with reviewed image
digests from `release/images.lock`. Normal builds and certification consume the
committed lock; refreshing a digest is an explicit reviewed operation.

The gateway and static-client images run as explicit non-root identities. Their
Compose services use `no-new-privileges`, drop all capabilities, use read-only
roots plus bounded tmpfs/writable mounts, define resource/PID/FD/log limits, and
retain health checks and shutdown grace periods. Static and disposable-runtime
verification compares effective identities, privileges, mounts, networks,
listeners, image digests, and public ports with policy.

### Backup, retention, and recovery

Backup generation writes database dumps and file/config snapshots only into a
mode-`0700` temporary directory. It creates and verifies an authenticated,
encrypted restic snapshot in an operator-selected off-node repository, removes
plaintext on every exit path, and returns nonzero if creation, upload, or
verification fails. Retention keeps 14 daily and 8 weekly snapshots. The timer
invokes the encrypted snapshot workflow rather than the current local plaintext
stage.

A separate Ubuntu reference host restores one candidate-bound snapshot, applies
the release/schema compatibility rules, invalidates unsafe session/invite state,
and passes external HTTPS, authentication, Chat, Stream, Library, Files, and
commentary probes. Measured RPO must not exceed 24 hours and RTO must not exceed
4 hours. Fixture-mode timestamp arithmetic tests the policy but never certifies
the live recovery requirement.

### Upgrade and rollback

Install and upgrade consume a verified release generation, never a mutable
checkout. Upgrade acquires the maintenance lock, verifies predecessor and
migration compatibility, creates a verified encrypted backup, installs the new
generation, runs the one-shot migrator, activates it atomically, and waits for
readiness. Binary rollback reactivates the previous generation only when schema
compatibility permits; otherwise it uses the verified restore path. Interrupted
operations leave a recoverable journal.

## Verification Strategy

Every implementation task follows red-green-refactor and ends in an
independently reviewable commit. CI rejects a candidate unless all locally
automatable gates pass:

- backend unit, race, vet, integration, migration, API-contract drift, and
  headless-build checks;
- frontend lint, TypeScript, unit, static build, bundle budget, origin-leak,
  accessibility, and Playwright checks;
- Rust format, lint, unit, and native Tauri builds on Ubuntu, Windows, and
  macOS;
- production Compose rendering, image-lock completeness, container-hardening,
  secret-mount, product-truth, release-truth, and clean-checkout checks;
- operations fixture tests, release reproducibility, source/image SBOM,
  vulnerability, license, checksum, and evidence-schema validation.

The frozen candidate additionally passes these external gates:

1. Clean install on Ubuntu 24.04 LTS x86-64 with Podman 5+.
2. Guided Jellyfin/Grimmory setup followed by Owner bootstrap and invite
   acceptance.
3. Browser member journeys for Chat, Stream, Library, Files, commentary,
   settings, administration, and failure recovery on current Chrome, Firefox,
   Edge, automated WebKit, desktop Safari, iOS Safari, and Android Chrome.
4. The essential connect, login, Chat, Stream, Library, Files, logout, restart,
   refresh, and revocation journeys in unsigned Tauri packages on Ubuntu,
   Windows, and macOS.
5. Encrypted backup, separate-host restore, predecessor upgrade, compatible
   rollback, and incompatible-schema restore.
6. Capacity at 100 registered members, 25 concurrent authenticated users, and
   10 chat/commentary events per second.
7. Host exposure and controlled-egress audits proving that only Traefik exposes
   public ports and internal services cannot bypass the egress proxy.
8. Candidate-bound SBOM, vulnerability/license results, artifact checksums, and
   operator-signed certification evidence.

## Error Handling

- Missing tools, configuration, secrets, integrations, images, external
  environments, or evidence return nonzero and name the corrective action.
- Secrets are never printed; sensitive prompts disable echo and atomic writes
  preserve the last valid configuration.
- A configured upstream outage degrades only its dependent module. No handler
  fabricates catalog or media data.
- Native certificate, credential, version, and network failures have distinct
  states. Credential rejection clears the rejected secret; transport and 5xx
  failures preserve it for retry.
- Applied migrations remain immutable. Startup refuses checksum drift, future
  migrations, or gaps.
- Certification does not continue after a required failure and does not convert
  fixture, stale, mismatched, or `not_run` evidence into `passed`.

## Completion Criteria

This program is complete only when:

- a clean reference host can install, configure, start, and operate the browser
  beta through the authoritative Linux CLI;
- the same candidate's Tauri packages complete the required journeys on Linux,
  Windows, and macOS using device tokens and OS credential storage;
- platform-trusted HTTPS is the only native trust path and no pinning or
  self-signed-certificate guarantee remains visible;
- encrypted off-node backup, retention, separate-host restore, upgrade, and
  rollback evidence meets the declared RPO/RTO contract;
- all production images and build inputs are immutable and the effective
  runtime satisfies the hardening policy;
- every beta product claim maps to fresh candidate-bound `passed` evidence;
- unavailable external gates remain `not_run`, preventing certification; and
- one immutable candidate, and no rebuilt substitute, is the artifact admitted
  to the invite-only beta.
