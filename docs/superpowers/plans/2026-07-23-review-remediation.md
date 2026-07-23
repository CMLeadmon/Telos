# Review Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace fabricated release/certification behavior, make the production stack boot securely, and restore the storage, deletion, event, chat, and Watch Party durability contracts identified by review.

**Architecture:** Build trustworthy release inputs and fixture-testable operational state machines first, then harden and wire the runtime before repairing backend concurrency/authorization and frontend reconnect behavior. Every behavior change begins with a failing regression test; unavailable external certification prerequisites fail explicitly and cannot be represented as local passes.

**Tech Stack:** Bash, JSON, Podman/Docker Compose, Cosign, Syft, restic, GitHub Actions, Go 1.26, PostgreSQL, Redis Lua, Next.js, Zustand, TypeScript, Vitest, Playwright

## Global Constraints

- The normative specification in `documentation/` wins when code and docs differ.
- Secrets come from `.env` interpolation or mounted files; no private key or credential may be committed.
- Jellyfin and Grimmory remain separate containerized processes and are never linked into the Go gateway.
- `.env.example` remains the canonical environment inventory and `docker compose --env-file .env.example config --quiet` must pass.
- External clean-node, off-node restore, DNS, capacity, alert-delivery, and physical-device gates report incomplete when unavailable.
- Existing untracked files belong to the user and must not be added, edited, or removed unless this plan names them.

---

### Task 1: Valid release trust roots and immutable image pins

**Files:**
- Modify: `.gitignore`
- Modify: `release/images.lock`
- Modify: `release/telos-release.pub`
- Modify: `release/telos-tag-signers`
- Modify: `scripts/verify-release-pins.sh`
- Test: `tests/operations/release_test.sh`
- Create locally, never track: `.telos-operator/release/cosign.key`
- Create locally, never track: `.telos-operator/release/telos-release-ssh`

**Interfaces:**
- Consumes: registry image names from `docker-compose.yml`, build bases from `backend/Dockerfile`, operator key password from `TELOS_COSIGN_PASSWORD`.
- Produces: `verify-release-pins.sh [--lock PATH]`; complete Cosign public PEM; OpenSSH allowed-signers entry; lock records formatted as `<logical-name> <image>@sha256:<64 lowercase hex>`.

- [ ] **Step 1: Add failing release-input tests**

Extend `tests/operations/release_test.sh` so it copies the lock and trust files to a temporary directory and proves that verification rejects repeated-digit/example digests, missing image coverage, duplicate logical names, ellipses, malformed PEM, and private-key paths tracked by Git.

Run: `bash tests/operations/release_test.sh`

Expected: FAIL because the current placeholders are accepted.

- [ ] **Step 2: Generate operator bootstrap keys**

Create `.telos-operator/release` with mode `0700`; generate an encrypted Cosign key pair and an Ed25519 SSH signing key with private files mode `0600`; copy only the complete public key material into `release/`.

Run: `git check-ignore .telos-operator/release/cosign.key && test "$(stat -c %a .telos-operator/release/cosign.key)" = 600`

Expected: both checks exit 0.

- [ ] **Step 3: Resolve and record real registry digests**

Resolve every production/build/test image with `skopeo inspect docker://IMAGE` or `podman image inspect`; write the returned immutable references to `release/images.lock`. Do not synthesize digests.

Run: `bash scripts/verify-release-pins.sh`

Expected: PASS with every required image covered once and every digest structurally and remotely verified when `--online` is supplied.

- [ ] **Step 4: Implement strict pin/trust validation**

Implement parsing without `eval`; require unique logical names, exact digest syntax, complete Compose/Dockerfile coverage, valid Cosign PEM through `cosign verify-blob --key`, valid SSH public-key parsing through `ssh-keygen -lf`, and absence of tracked private material.

Run: `bash tests/operations/release_test.sh`

Expected: PASS.

- [ ] **Step 5: Record focused changes**

Run: `git diff --check -- .gitignore release scripts/verify-release-pins.sh tests/operations/release_test.sh`

Expected: exit 0 with no whitespace errors.

### Task 2: Reproducible release artifacts and candidate binding

**Files:**
- Modify: `scripts/build-release.sh`
- Modify: `scripts/sign-evidence.sh`
- Modify: `scripts/verify-evidence.sh`
- Modify: `scripts/stage-release.sh`
- Modify: `release/hash-graph.md`
- Modify: `release/release-manifest.schema.json`
- Modify: `release/artifact-index.schema.json`
- Modify: `.github/workflows/release.yml`
- Test: `tests/operations/release_test.sh`
- Test: `scripts/tests/reproducible-build-test.sh`

**Interfaces:**
- Consumes: `TELOS_RELEASE_VERSION`, `SOURCE_DATE_EPOCH`, `TELOS_RELEASE_OUT`, `TELOS_COSIGN_KEY`, clean Git commit, `release/images.lock`.
- Produces: deterministic release directory containing `telos-core.oci.tar`, `compose.yaml`, SBOMs, `telos-ops.tar.zst`, `artifacts.json`, `release-manifest.json`, `SHA256SUMS`, signature bundles, `candidate-lock.json`, and its bundle.

- [ ] **Step 1: Add failing artifact-graph tests**

Test missing tools/inputs, dirty release inputs, command failure propagation, two-build byte equality, manifest source/tree binding, complete checksums, absence of self-hashes/private keys, invalid signatures, and candidate-lock mismatch.

Run: `bash tests/operations/release_test.sh && bash scripts/tests/reproducible-build-test.sh`

Expected: FAIL because `build-release.sh` creates no output.

- [ ] **Step 2: Implement deterministic payload creation**

Use temporary output plus atomic rename. Normalize archive order, owner/group, permissions, and mtime to `SOURCE_DATE_EPOCH`. Render Compose from locked image references. Export the built gateway as OCI and generate source/image SBOMs with the pinned Syft tool.

Run: `TELOS_RELEASE_VERSION=0.1.0-test SOURCE_DATE_EPOCH=1700000000 TELOS_RELEASE_OUT="$(mktemp -d)" scripts/build-release.sh`

Expected: all declared payload files exist and are nonempty.

- [ ] **Step 3: Implement the directed hash graph**

Generate `artifacts.json` over payloads, bind it from `release-manifest.json`, generate `SHA256SUMS` over payloads plus both JSON indexes, then create a candidate lock over those bytes and signature bundles without recursive edges.

Run: `sha256sum -c "$TELOS_RELEASE_OUT/SHA256SUMS"`

Expected: every entry reports `OK`.

- [ ] **Step 4: Implement signing and verification**

`sign-evidence.sh` signs exact blobs with the operator key. `verify-evidence.sh` verifies the bundle, committed public key, configured fingerprint, expected version/source/schema, and candidate-lock digest. Neither command accepts a key or output inside the payload.

Run: `bash tests/operations/release_test.sh`

Expected: PASS for genuine artifacts and FAIL for each tampered fixture.

- [ ] **Step 5: Make release CI upload real artifacts**

Pin actions by full commit SHA, set minimum permissions/timeouts, invoke the builder, independently verify artifacts, and upload the exact release directory with its digest.

Run: `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/release.yml")'`

Expected: exit 0.

### Task 3: Executable accumulated gates and honest certification

**Files:**
- Modify: `ci/phase-gates.json`
- Modify: `scripts/verify-accumulated-suite.sh`
- Modify: `scripts/certify-beta.sh`
- Modify: `scripts/check-release-truth.sh`
- Test: `tests/operations/accumulated_suite_test.sh`
- Test: `tests/operations/beta_certification_test.sh`

**Interfaces:**
- Consumes: a JSON registry whose command is an argument array, optional `--candidate-lock`, required external `--evidence-out`.
- Produces: canonical report with source commit/tree, candidate-lock digest, per-gate timing/exit/output hash, and overall status.

- [ ] **Step 1: Add failing command-registry tests**

Create temporary registries with one success, one failure, an unknown command, shell metacharacters, duplicate gate IDs, and a candidate mismatch. Assert the failure command is executed, its status propagates, and no passing report/signature is created.

Run: `bash tests/operations/accumulated_suite_test.sh`

Expected: FAIL because no registry command runs.

- [ ] **Step 2: Implement the safe runner**

Parse JSON with Python or `jq`, validate the schema, allowlist executable paths, execute argument arrays from the repository root, stream output through a temporary log, and atomically write evidence after all gates finish.

Run: `bash tests/operations/accumulated_suite_test.sh`

Expected: PASS.

- [ ] **Step 3: Make beta certification aggregate evidence**

Require every declared source/candidate/external record, validate age and candidate identity, preserve `not_run` as nonpassing, sign only an all-passed aggregate, and remove the success-print fallback.

Run: `bash tests/operations/beta_certification_test.sh`

Expected: PASS and fixtures missing external evidence return nonzero.

### Task 4: Runtime-hardening verifier and CI gates

**Files:**
- Modify: `scripts/verify-runtime-hardening.sh`
- Modify: `tests/operations/runtime_hardening_test.sh`
- Modify: `scripts/ci/run-local.sh`
- Modify: `scripts/ci/validate-sbom.sh`
- Modify: `tests/operations/ci_gates_test.sh`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `--compose PATH --env-file PATH --evidence-out PATH`, plus optional `--runtime`.
- Produces: redacted evidence bound to rendered Compose digest and, in runtime mode, effective container/image state.

- [ ] **Step 1: Add failing hardening tests**

Assert nonexistent inputs, root users, writable roots, missing security options, unbounded services, missing key mounts, unapproved published ports, and runtime/source mismatch fail without writing `status: passed`.

Run: `bash tests/operations/runtime_hardening_test.sh`

Expected: FAIL because nonexistent paths currently pass.

- [ ] **Step 2: Implement rendered-model validation**

Call `docker compose ... config --format json` or the Podman-compatible equivalent, validate policies with a standalone parser, hash the rendered model, redact environment values, and write evidence atomically only after all checks.

Run: `bash tests/operations/runtime_hardening_test.sh`

Expected: PASS.

- [ ] **Step 3: Add security, dependency, license, SBOM, Compose, operations, and accumulated CI jobs**

Pin all actions, use job-specific permissions and timeouts, invoke repository scripts, upload content-hashed reports, and make each job independently required by workflow structure.

Run: `bash tests/operations/ci_gates_test.sh`

Expected: PASS.

### Task 5: Install, upgrade, rollback, and encrypted backups

**Files:**
- Modify: `scripts/install.sh`
- Modify: `scripts/upgrade.sh`
- Modify: `scripts/rollback.sh`
- Modify: `scripts/drills/clean-node-install.sh`
- Modify: `scripts/drills/upgrade-rollback.sh`
- Modify: `scripts/backup.sh`
- Modify: `scripts/drills/recovery.sh`
- Modify: `scripts/verify-restored-node.sh`
- Modify: `tests/operations/install_test.sh`
- Modify: `tests/operations/upgrade_rollback_test.sh`
- Modify: `tests/operations/backup_restore_test.sh`

**Interfaces:**
- Consumes: verified release directory, `TELOS_INSTALL_ROOT`, shared maintenance lock, Compose adapter, restic repository/password.
- Produces: versioned generations, atomic `current`/`previous` pointers, operation journal, readiness result, encrypted restic snapshot ID.

- [ ] **Step 1: Add failing fixture state-machine tests**

Use fake compose/restic/readiness commands and a temporary install root. Cover invalid release, failed signature, extraction, migration, readiness, interrupted activation, compatible rollback, restore-required rollback, plaintext cleanup on success/failure, and lock contention.

Run: `bash tests/operations/install_test.sh && bash tests/operations/upgrade_rollback_test.sh && bash tests/operations/backup_restore_test.sh`

Expected: FAIL against print-only scripts and plaintext backup behavior.

- [ ] **Step 2: Implement clean-node installation**

Verify release inputs, create mode-bounded directories, extract into a new generation, render config, start migration then services, poll readiness with timeout, and atomically activate only after success.

Run: `bash tests/operations/install_test.sh`

Expected: PASS.

- [ ] **Step 3: Implement journaled upgrade and rollback**

Acquire `maintenance-lock.sh`, write and fsync operation stages, create the pre-change encrypted backup, activate candidate, recover interrupted states, and choose binary rollback or verified restore from schema policy.

Run: `bash tests/operations/upgrade_rollback_test.sh`

Expected: PASS.

- [ ] **Step 4: Encrypt backups before unconditional plaintext cleanup**

Create plaintext material under `mktemp -d`, install an EXIT trap immediately, call restic backup, verify the snapshot, publish metrics only after verification, and never retain the temporary generation.

Run: `bash tests/operations/backup_restore_test.sh && bash scripts/tests/backup_test.sh`

Expected: PASS, including injected restic failure with no plaintext residue.

### Task 6: Harden Compose, mount secrets, and deploy monitoring

**Files:**
- Modify: `backend/Dockerfile`
- Modify: `backend/main.go`
- Modify: `docker-compose.yml`
- Modify: `.env.example`
- Modify: `config/prometheus/prometheus.yml`
- Modify: `config/alertmanager/alertmanager.yml`
- Modify: `scripts/verify-monitoring.sh`
- Modify: `tests/operations/monitoring_test.sh`
- Modify: `backend/metrics_test.go`

**Interfaces:**
- Produces: gateway/migrator UID/GID 10001; mounted `/run/telos/cursor.key` and `/run/telos/hls.key`; internal `:9090/metrics`; Prometheus and Alertmanager services.

- [ ] **Step 1: Add failing Docker/Compose/monitoring tests**

Assert `USER 10001:10001`, required key mounts, `no-new-privileges`, `cap_drop: [ALL]`, read-only roots, bounded tmpfs/resources, internal-only metrics listener, matching Prometheus target, and presence of monitoring services.

Run: `bash tests/operations/runtime_hardening_test.sh && bash tests/operations/monitoring_test.sh`

Expected: FAIL on the current root image, missing mounts, and absent services.

- [ ] **Step 2: Harden the gateway image and services**

Create the runtime account in the final stage, set ownership explicitly, declare `USER 10001:10001`, and update gateway/migrator Compose security, volumes, tmpfs, resources, health, and stop-grace settings.

Run: `docker compose --env-file .env.example config --quiet`

Expected: exit 0.

- [ ] **Step 3: Add the internal metrics server**

Start a separate `http.Server{Addr: ":9090", Handler: metricsHandler()}` under the same shutdown context; do not register metrics on the public mux. Return startup errors and drain both listeners.

Run: `scripts/test-backend.sh unit -run 'TestMetrics|TestServer'`

Expected: PASS.

- [ ] **Step 4: Deploy and validate monitoring**

Add digest-pinned Prometheus/Alertmanager, monitoring network, retention/storage bounds, configs, readiness, and alert-rule validation.

Run: `bash tests/operations/monitoring_test.sh && bash scripts/verify-monitoring.sh --source --evidence-out "$(mktemp)"`

Expected: PASS.

### Task 7: Wire confined uploads and physical asset deletion

**Files:**
- Modify: `backend/main.go`
- Modify: `backend/upload.go`
- Modify: `backend/account_lifecycle.go`
- Modify: `backend/reconciliation.go`
- Modify: `backend/settings.go`
- Test: `backend/upload_integration_test.go`
- Test: `backend/account_lifecycle_integration_test.go`
- Test: `backend/reconciliation_integration_test.go`
- Test: `backend/main_test.go`

**Interfaces:**
- Produces: `initializeStorageRuntime() (*StorageRuntime, error)` where `StorageRuntime` owns confined storage, quota, scanner, upload pipeline, physical checker/remover, and reconciler; `StorageRuntime.Close()`.
- Produces: `confinedAssetRemover.Remove(ctx, area, key) error`.

- [ ] **Step 1: Add failing startup and route tests**

Assert startup fails for missing/unusable roots, constructs nonnil upload/reconciliation/removal dependencies for valid roots, and both file/book routes invoke an injected `UploadProcessor` rather than legacy filesystem code.

Run: `scripts/test-backend.sh unit -run 'TestInitializeStorage|TestUploadRouteUsesPipeline'`

Expected: FAIL because the runtime is not constructed.

- [ ] **Step 2: Add failing deletion retry test**

Delete an account with a private asset using a remover that fails once. Assert the job stays pending after failure, succeeds on retry, and only then becomes done. Assert unsafe keys never reach an unconstrained OS path.

Run: `scripts/test-backend.sh integration -run 'TestAccountDeletion.*Asset'`

Expected: FAIL when the production default is a no-op.

- [ ] **Step 3: Implement the storage runtime**

Open all configured roots, create `QuotaGuard`, scanner, `UploadPipeline`, `FileService`, physical adapter, catalog adapter, and reconciler. Assign globals only after complete construction; close descriptors during shutdown.

Run: `scripts/test-backend.sh unit -run 'TestInitializeStorage|TestUploadRouteUsesPipeline'`

Expected: PASS.

- [ ] **Step 4: Route uploads through the pipeline**

Parse and bound multipart input once, map file/book/avatar purposes and visibility, call `UploadPipeline.Process`, and preserve existing response shapes. Remove direct `os.Create`, copy, scan, move, and catalog insertion from public handlers.

Run: `scripts/test-backend.sh integration -run 'TestUploadPipeline|TestUploadRoute'`

Expected: PASS.

- [ ] **Step 5: Wire confined deletion**

Map lifecycle job areas to `StorageArea`, call `ConfinedStorage.Remove`, treat `ENOENT` as success, and leave failed jobs pending with error/attempt metadata.

Run: `scripts/test-backend.sh integration -run 'TestAccountDeletion|TestReconciliation'`

Expected: PASS.

### Task 8: Operation-specific security outbox idempotency

**Files:**
- Modify: `backend/auth.go`
- Modify: `backend/outbox.go`
- Modify: `backend/main.go`
- Modify: `backend/channel_admin.go`
- Modify: `backend/account_lifecycle.go`
- Modify: `backend/settings.go`
- Modify: `backend/annotations.go`
- Create: `backend/db/migrations/0019_security_mutation_ids.sql`
- Test: `backend/outbox_integration_test.go`
- Test: `backend/account_lifecycle_integration_test.go`
- Test: `backend/chat_integration_test.go`

**Interfaces:**
- Changes: `SecurityEventIntent` adds `OperationID string`.
- Key format: `security:<kind>:<operationID>` after validating nonempty durable operation ID.

- [ ] **Step 1: Add failing distinct/replay tests**

Within integration fixtures, perform two password changes and two role changes for the same actor/subject/resource. Assert four distinct outbox rows. Replay one durable operation ID and assert its row count remains one.

Run: `scripts/test-backend.sh integration -run 'TestSecurityMutation.*Distinct|TestSecurityMutation.*Replay'`

Expected: FAIL because tuple-derived keys collide.

- [ ] **Step 2: Persist durable operation identifiers**

Add a migration column/table only where an existing mutation row cannot supply a unique ID. Generate the identifier at the transaction boundary and pass it to mutation and outbox writes in the same transaction.

Run: `scripts/test-backend.sh integration -run 'TestSecurityMutation'`

Expected: PASS.

- [ ] **Step 3: Update every call site**

Use the owner user ID for the one-time bootstrap, invitation ID for acceptance,
deletion-request ID for account deletion, override audit ID for channel
overrides, annotation audit ID for moderation, and a UUID inserted into
`security_mutations` for repeatable password/role/status/session operations.
Reject empty IDs; do not fall back to the old tuple.

Run: `scripts/test-backend.sh integration -run 'TestSecurityMutation|TestNotifications|TestAccountDeletion'`

Expected: PASS.

### Task 9: Atomic and authorized Watch Party state

**Files:**
- Modify: `backend/watchparty.go`
- Modify: `backend/watchparty_handlers.go`
- Modify: `backend/watchparty_test.go`
- Modify: `backend/watchparty_integration_test.go`

**Interfaces:**
- Produces: `AuthorizePartyRead(ctx, partyID, actor) (WatchParty, error)`.
- Produces: Redis Lua compare-and-set result codes `ok`, `not_found`, `stale`, and `lease_expired`.

- [ ] **Step 1: Add failing concurrent control test**

Start two goroutines at a barrier with expected version N. Assert exactly one returns N+1 and one returns `errWPStaleVersion`; final Redis state equals the successful control.

Run: `scripts/test-backend.sh integration -run 'TestWatchPartyConcurrentControl'`

Expected: FAIL because both requests can pass the separate GET.

- [ ] **Step 2: Add failing read-authorization tests**

Test anonymous/nonmember/invited/left members, joined members, and joined members who have lost linked-channel access. Apply the same policy to party identity and state handlers.

Run: `scripts/test-backend.sh integration -run 'TestWatchPartyReadAuthorization'`

Expected: FAIL because any global `view_media` user can read.

- [ ] **Step 3: Implement Redis Lua compare-and-set**

Validate Go input first, serialize the next state, and execute one Lua script that verifies current version and lease host/generation before preserving TTL and setting N+1. Map script codes to stable errors.

Run: `scripts/test-backend.sh integration -run 'TestWatchPartyCreateAndControl|TestWatchPartyConcurrentControl|TestWatchPartyHeartbeat'`

Expected: PASS.

- [ ] **Step 4: Make lease-expiry pause atomic**

Use the same expected-version CAS for auto-pause; retry a bounded number of times when another control wins.

Run: `scripts/test-backend.sh integration -run 'TestWatchParty.*Lease'`

Expected: PASS.

- [ ] **Step 5: Enforce party membership and linked-resource access**

Load a current joined membership, authorize media, text channel view, and voice channel access, then return the party/state. Use a uniform not-found/forbidden response that does not disclose party existence.

Run: `scripts/test-backend.sh integration -run 'TestWatchPartyReadAuthorization'`

Expected: PASS.

### Task 10: Root-composer acknowledgement and mutation recovery

**Files:**
- Modify: `frontend/src/stores/useChatSessionStore.ts`
- Modify: `frontend/src/app/(shell)/chat/page.tsx`
- Modify: `frontend/src/stores/useChatSessionStore.test.ts`
- Create: `frontend/src/app/(shell)/chat/page.test.tsx`

**Interfaces:**
- State adds: `pendingRootMutationId: string | null`, `rootDraftError: string | null`.
- Changes: `send(content, embed) -> Promise<ChatMessage | void>` sends `clientMutationId`.

- [ ] **Step 1: Add failing store tests**

Reject the first API attempt, capture its mutation ID, retry, and assert the same ID is used. Resolve acknowledgement and assert the ID/error clear. Verify a later draft receives a different ID.

Run: `cd frontend && npm test -- --run src/stores/useChatSessionStore.test.ts`

Expected: FAIL because root sends omit `clientMutationId`.

- [ ] **Step 2: Add failing composer tests**

Submit a draft with deferred `send`; assert text/embed remain until resolution. Reject and assert both remain with an error. Resolve and assert only the matching acknowledged draft clears.

Run: `cd frontend && npm test -- --run 'src/app/(shell)/chat/page.test.tsx'`

Expected: FAIL because the page clears immediately.

- [ ] **Step 3: Implement store acknowledgement state**

Allocate one mutation ID per pending root draft, send it in the JSON body, merge the acknowledged message by ID, clear pending state on success, and rethrow while preserving it on failure.

Run: `cd frontend && npm test -- --run src/stores/useChatSessionStore.test.ts`

Expected: PASS.

- [ ] **Step 4: Await submission in the page**

Make `submit` async, prevent concurrent submission of the same draft, snapshot draft/embed, await `send`, clear only if current input still matches the snapshot, and render a retryable error.

Run: `cd frontend && npm test -- --run 'src/app/(shell)/chat/page.test.tsx'`

Expected: PASS.

### Task 11: Deterministic channel-change reconciliation

**Files:**
- Modify: `frontend/src/stores/useChatSessionStore.ts`
- Modify: `frontend/src/stores/useChatSessionStore.test.ts`
- Modify: `backend/main.go`
- Modify: `backend/chat_threads.go`
- Modify: `backend/chat_integration_test.go`

**Interfaces:**
- State adds: `channelSequences: Record<string, number>`.
- Consumes: `GET /api/v1/channels/{id}/changes?after=<n>&through=<n>&limit=100`.
- Produces: `reconcileChannel(channelId, generation) Promise<void>`.
- Changes: `ChannelChange` adds `message?: WSMessage`; live `WSEvent` adds
  `sequence?: number`.

- [ ] **Step 1: Add failing reconnect tests**

Mock two paginated change pages containing create/edit/delete/reaction/pin events plus socket events arriving during catch-up. Assert ordered, deduplicated final state and high-water persistence. Switch channels mid-request and assert the stale response is ignored.

Run: `cd frontend && npm test -- --run src/stores/useChatSessionStore.test.ts`

Expected: FAIL because reconnect resets state and never calls `/changes`.

- [ ] **Step 2: Define full change payloads**

Materialize current message state for every changed message ID in one bounded
query per change page; deleted messages return a tombstone. Record and return
the transaction-created change sequence from every create/edit/delete/reaction/
pin mutation, and include it in the published live event so both streams share
one ordering domain.

Run: `scripts/test-backend.sh integration -run 'TestChannelChange'`

Expected: PASS with sequence and bounded pagination represented.

- [ ] **Step 3: Implement ordered reconciliation**

Keep per-channel high-water, buffer live events while reconciling, page to the sampled `highWater`, apply each sequence once, merge messages by ID, refresh referenced records/pins as needed, then drain the sorted live buffer.

Run: `cd frontend && npm test -- --run src/stores/useChatSessionStore.test.ts`

Expected: PASS.

- [ ] **Step 4: Handle expired cursors**

On the backend cursor-expired response, perform an authorized full history/pins resync, set the returned high-water, then resume live application without silently keeping stale edits/deletions.

Run: `cd frontend && npm test -- --run src/stores/useChatSessionStore.test.ts`

Expected: PASS.

### Task 12: Full verification and product-truth reconciliation

**Files:**
- Modify only when verification exposes an in-scope defect: files named by the failing test.
- Review: `documentation/product/beta-feature-status.md`
- Review: `documentation/operations/*.md`

**Interfaces:**
- Produces: fresh command evidence; no external gate is marked passed from fixtures.

- [ ] **Step 1: Run placeholder and clean-checkout scans**

Run:

```bash
grep -rn "ci""te:" documentation/ AGENTS.md ; echo "exit=$?"
grep -rnE "TO""DO|TB""D" documentation/ AGENTS.md ; echo "exit=$?"
bash scripts/verify-clean-checkout.sh --inventory-only
```

Expected: scans report no prohibited placeholder; inventory passes.

- [ ] **Step 2: Run operations and release tests**

Run:

```bash
for test in tests/operations/*.sh scripts/tests/*.sh; do bash "$test"; done
bash scripts/verify-release-pins.sh
docker compose --env-file .env.example config --quiet
```

Expected: all exit 0.

- [ ] **Step 3: Run backend verification**

Run:

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: all Go packages pass with zero failures.

- [ ] **Step 4: Run frontend verification**

Run:

```bash
cd frontend
npm run lint
npm test -- --run
npm run build
```

Expected: lint, unit suite, and production build exit 0.

- [ ] **Step 5: Run browser verification**

Start the frontend development server on port 3000, wait for readiness, then run:

```bash
cd frontend
npx playwright test
```

Expected: Playwright exits 0; otherwise record exact failing tests and do not claim the browser gate.

- [ ] **Step 6: Run the accumulated suite against the current source**

Run: `scripts/verify-accumulated-suite.sh --registry ci/phase-gates.json --evidence-out "$(mktemp)"`

Expected: every registered local gate executes and passes. External prerequisites remain explicitly incomplete until run in their designated environments.

- [ ] **Step 7: Review the final diff**

Run:

```bash
git diff --check
git status --short
git diff --stat
```

Expected: no whitespace errors; only planned files and pre-existing user-owned untracked files appear.
