# Standalone Beta Readiness Master Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship one immutable Telos candidate that supports a clean Ubuntu 24.04 LTS x86-64 server installation, the browser client hosted by that server, and unsigned Tauri desktop clients for Linux, Windows, and macOS, with truthful release evidence and proven recovery.

**Architecture:** Preserve the standalone client/server split: Traefik terminates platform-trusted HTTPS, routes API traffic to the headless Go gateway, and routes non-API traffic to an unprivileged static Next.js container. The Bash `telos` CLI remains the authoritative server installer and operator interface. Desktop clients use bearer/device authentication and operating-system credential storage. Five dependency-ordered phase plans establish gate truth, browser deployment, desktop behavior, operational safety, and immutable-candidate certification.

**Tech Stack:** Go 1.26.5; PostgreSQL 16.14; Redis 7; Next.js 16.2.10; React 19.2.7; TypeScript 5.9.3; Vitest 4.1.10; Playwright 1.61.1; Tauri CLI 2.11.4 / Rust crate 2.11.5; Rust stable 1.88 or newer; Podman 5+; Traefik; Jellyfin; Grimmory; ClamAV; restic; Bash; Python 3 standard library for release/evidence orchestration.

**Spec:** `docs/superpowers/specs/2026-08-20-standalone-beta-readiness-design.md`

## Global Constraints

- The certified server target is Ubuntu 24.04 LTS on x86-64 with Podman 5 or newer. Other server platforms are best-effort and do not satisfy the beta gate.
- The beta client matrix is the hosted browser client plus unsigned Tauri packages built natively on Ubuntu, Windows, and macOS. Native mobile packages, public desktop signing, and backend hosting on Windows are out of scope.
- Only platform-trusted HTTPS is supported. Remove the non-enforcing TLS fingerprint probe and all self-signed/TOFU claims; do not replace them with an override.
- The Bash CLI in `telos` and `cli/` is authoritative for the beta. The incomplete Go CLI under `backend/cmd/telos/` remains experimental and must not be advertised as a supported operator path.
- Go is not installed on the host. Run Go commands through `podman` with `docker.io/library/golang:1.26.5` or through `scripts/test-backend.sh`.
- `backend/` is one flat Go package. Extend the matching sibling file; keep route wiring and process-mode selection in `backend/main.go` small.
- Applied migrations are immutable. Current history ends at `backend/db/migrations/0023_device_tokens.sql`; every schema change uses the next numbered, idempotent migration. Never edit an applied migration.
- Credentials come from `.env` interpolation or mounted secret files. Never put credentials in argv, logs, evidence, fixtures, generated artifacts, browser storage, or repository content.
- Preserve the Jellyfin and Grimmory process boundary. Telos talks to each service only over HTTP and never links their code into the Go gateway.
- Never hand-edit `frontend/src/styles/tokens/` or `frontend/src/styles/foundations/`.
- Do not alter dependencies, lockfiles, host packages, or gates merely to make a check pass. A missing external environment produces `not_run`, and `not_run` blocks candidate certification.
- Each implementation task starts with a failing behavior test, makes the narrowest production change, runs the focused test, runs the phase-level regression subset, and commits an independently reviewable result.
- Each phase must merge and pass its exit gate before a downstream phase consumes its contract. Do not certify a candidate from unmerged worktrees or a dirty checkout.

---

## Plan Family and Dependency Order

| Order | Detailed plan | Primary output | May begin when | Exit condition |
|---|---|---|---|---|
| 1 | `2026-08-20-standalone-beta-phase-1-truth-and-gates.md` | Fail-closed evidence and a truthful beta contract | Immediately | No placeholder can emit success; local phase gates are executable and honest |
| 2 | `2026-08-20-standalone-beta-phase-2-clean-linux-deployment.md` | Clean server plus hosted browser client | Phase 1 merged | Fresh reference node reaches all four modules over HTTPS through the Bash CLI |
| 3 | `2026-08-20-standalone-beta-phase-3-desktop-client.md` | Secure and functional desktop clients | Phase 2 server/API contracts stable | Native packages pass device-auth and media journeys on all three desktop OSes |
| 4 | `2026-08-20-standalone-beta-phase-4-operational-safety.md` | Pinned/hardened runtime and proven encrypted recovery | Phase 2 merged; desktop work may overlap | Runtime, backup, restore, upgrade, rollback, exposure, and capacity gates pass |
| 5 | `2026-08-20-standalone-beta-phase-5-candidate-certification.md` | One frozen beta candidate and complete evidence | Phases 1–4 merged | Every required candidate-bound gate is `passed`; none is stale or `not_run` |

Phase 3 and Phase 4 may execute concurrently only after Phase 2 freezes the server registration, CORS, routing, and release-layout contracts. Phase 5 begins after both are integrated.

## Shared Contracts

### Evidence envelope v2

Every local or external gate writes the same JSON shape to a temporary file and atomically renames it only after the command finishes:

```json
{
  "schemaVersion": 2,
  "gateId": "clean-install",
  "status": "passed",
  "reason": "all assertions passed",
  "sourceCommit": "40 lowercase hexadecimal characters",
  "candidateLockDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "command": ["bash", "scripts/install.sh", "--release", "/srv/telos/releases/0.1.0-beta.1"],
  "startedAt": "2026-08-20T14:00:00Z",
  "finishedAt": "2026-08-20T14:08:00Z",
  "exitCode": 0,
  "outputDigest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "toolVersions": {"podman": "5.x"},
  "subject": {"kind": "runtime", "digest": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}
}
```

`status` is exactly `passed`, `failed`, or `not_run`. A missing prerequisite or unavailable external host is `not_run` with a corrective `reason`; it is never `passed`. `failed` records a completed check whose assertions failed. Only `passed` satisfies a required gate.

The implementation owner is `scripts/lib/evidence.py`; the schema owner is `release/evidence-envelope.schema.json`; shell commands call the helper and never construct partial envelopes themselves.

### Gate catalog and runner

`ci/phase-gates.json` is a declarative catalog, not a cached success marker. Each gate has a stable ID, phase, scope (`local` or `external`), argv array, timeout, required evidence kind, and prerequisites. `scripts/run-gates.py` executes argv without a shell, captures combined output to a candidate evidence directory, and emits an evidence envelope. `scripts/verify-accumulated-suite.sh` is a compatibility wrapper around that runner.

The catalog never contains credentials, shell fragments, mutable URLs, or `gatesPassed`. A candidate is acceptable only when its required gate IDs exactly equal the set of verified `passed` envelopes.

### Beta product contract

`documentation/product/beta-contract.json` is the machine-readable product boundary. It fixes:

- capability-gated modules: `chat`, `stream`, `library`, `files`, plus the separate `settings` surface;
- server architecture: `standalone-client-server`;
- browser route delivery: `telos-client` through Traefik;
- supported server and desktop platforms;
- removed features and prohibited dependencies/routes/services;
- required operational and external certification gate IDs.

`documentation/product/beta-feature-status.md` is the human explanation. `scripts/check-product-truth.sh` compares the JSON contract with the frontend module inventory, Go route inventory, Compose services, package dependencies, and declared evidence—not keyword presence alone.

### Candidate lock

`release/candidate-lock.schema.json` defines the immutable candidate identity. `candidate-lock.json` contains the source commit, release-manifest digest, exact server image digests, exact desktop artifact digests, static-client artifact digest, migration-manifest digest, and dependency-lock digests. Its canonical SHA-256 becomes `candidateLockDigest` in every envelope.

No gate may rebuild the candidate. External tests install or pull only artifacts named by the lock. Any artifact change creates a new lock and invalidates prior evidence.

### Server integration validation

`telos configure integrations` is the only supported beta path for finishing Jellyfin and Grimmory configuration. It sends a JSON request over stdin to the gateway image's `validate-integrations` command mode:

```json
{
  "jellyfinToken": "secret",
  "grimmoryUser": "owner@example.net",
  "grimmoryPassword": "secret"
}
```

The command returns only non-secret choices:

```json
{
  "jellyfin": [{"id": "movies", "name": "Movies"}],
  "grimmory": [{"id": "2", "name": "Books"}]
}
```

It validates Jellyfin through `GET /Library/VirtualFolders` with `X-Emby-Token`, validates Grimmory through its login endpoint followed by `GET /api/v1/libraries`, and runs on the same Compose networks and DNS names as production. The CLI requires at least one ID from each list and atomically rewrites `.env` without printing inputs.

### Desktop credential contract

Frontend TypeScript calls only these Tauri commands:

```typescript
type CredentialKey = {
  origin: string;
  accountId: string;
  deviceId: string;
};

invoke("credential_set", { key, secret: refreshToken });
invoke<string | null>("credential_get", { key });
invoke("credential_delete", { key });
```

Rust normalizes an HTTPS origin, derives a non-secret account name, and stores the refresh token through the OS credential service using `keyring = "=4.1.6"`. JavaScript memory holds access tokens. Web fallback memory storage is test/development-only and may never claim persistence.

### HLS locator contract

`POST /api/v1/stream/items/{id}/hls-token` remains authenticated and returns a signed locator. Requests that spend that locator do not need a cookie or bearer header, but must verify signature, expiry, user/device or session binding, item/resource binding, current account/device status, and current `view_media` permission. A locator is long enough for supported playback, but revocation is rechecked at spend time. HLS and audiobook secrets remain separate mounted files.

## Required Phase Exit Gates

### Phase 1: Truth and fail-closed gates

- All former placeholder scripts reject unknown options and produce `not_run` or a verified outcome.
- Evidence schema positive and negative fixture tests pass.
- Gate runner rejects missing, stale, mismatched, duplicate, and `not_run` evidence.
- Product contract and generated inventories agree.
- Removed-feature scans find no LiveKit/voice/watch-party runtime residue.

### Phase 2: Clean Linux browser deployment

- Production Compose renders from `.env.example` and contains `telos-client` with no published ports or backend/data networks.
- Traefik routes `/api/v1/*` to `telos-core` and browser routes to `telos-client`.
- `telos init` creates distinct mode-correct signing files and refuses overwrite.
- Guided integration validation succeeds from container networks and normal start refuses incomplete inputs.
- A clean Ubuntu reference host completes install, setup, owner bootstrap, invitation, and four-module browser smoke over trusted HTTPS.

### Phase 3: Desktop client

- Registration works through the production middleware chain with password verification and rate limiting.
- Approved Tauri origins pass `Authorization` preflight; unapproved origins and wildcards fail.
- Refresh tokens persist only in OS credential storage; access tokens remain in memory.
- Ordinary platform TLS validation is the sole connection path.
- Native WebKit HLS succeeds without bearer headers while locator replay/revocation tests pass.
- Native packages build and complete required journeys on Ubuntu, Windows, and macOS.

### Phase 4: Operational safety

- Every production image is locked by digest and the runtime identity matches the lock.
- Effective containers satisfy non-root, read-only, privilege, capability, mount, network, listener, resource, PID, FD, and log-limit policy.
- Encrypted off-node backup retains 14 daily and 8 weekly snapshots and removes plaintext on success, interruption, and failure.
- Separate-host restore meets RPO at most 24 hours and RTO at most 4 hours.
- Predecessor upgrade, compatible binary rollback, and incompatible-schema restore paths are proven.
- Exposure, controlled-egress, capacity, SBOM, vulnerability, and license gates pass against candidate artifacts.

### Phase 5: Immutable certification

- Release generation is reproducible and all artifacts match one candidate lock.
- Browser, desktop, clean-host, restore, upgrade/rollback, capacity, exposure, and supply-chain envelopes bind to that lock.
- Candidate verification rejects evidence from another commit, lock, runtime, artifact, tool policy, or expired time window.
- Certification stops at the first missing/failed required gate and cannot turn `not_run` into `passed`.
- Release workflow promotes the already-tested artifacts and publishes checksums plus unsigned-desktop instructions; it does not rebuild.

## Full Verification Commands

Run from the repository root unless a command changes directory:

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -race ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
bash scripts/test-backend.sh

cd frontend
npm ci
npm run lint
npx tsc --noEmit
npm run test:unit
npm run build
npm run check:bundle
npx playwright test
cd ..

podman run --rm -v ./frontend/src-tauri:/app:z -w /app docker.io/library/rust:1.88 \
  sh -lc 'rustup component add rustfmt clippy && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test'

bash scripts/validate-compose.sh --env-file .env.example
bash scripts/check-product-truth.sh
bash scripts/check-release-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only
python3 scripts/run-gates.py --catalog ci/phase-gates.json --scope local --candidate-lock release/candidate-lock.json

grep -rn "ci""te:" documentation/ AGENTS.md
grep -rnE "TO""DO|TB""D" documentation/ AGENTS.md
```

Expected: every command exits `0`; the documentation hygiene commands print nothing. Native Tauri bundle creation and the external gates run on their declared native/reference hosts, not inside the Linux-only local command block.

## Execution Checkpoints

- [ ] **Checkpoint 1:** Execute and merge the Phase 1 plan; archive its local `passed` evidence.
- [ ] **Checkpoint 2:** Execute and merge the Phase 2 plan; review the clean-host browser drill before freezing API contracts.
- [ ] **Checkpoint 3:** Execute Phase 3 and Phase 4 from the merged Phase 2 baseline; reconcile only through reviewed commits.
- [ ] **Checkpoint 4:** Merge Phase 3 and Phase 4; run the complete local suite from a clean checkout.
- [ ] **Checkpoint 5:** Execute Phase 5 once, freeze the candidate lock, run external gates without rebuilding, and promote only if every required gate is `passed`.
