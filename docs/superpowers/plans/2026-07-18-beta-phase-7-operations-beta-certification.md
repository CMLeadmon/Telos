# Telos Beta Phase 7: Operations and Beta Certification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the release-engineering surface, freeze one immutable Telos beta candidate, and certify that exact candidate for reproducibility, hardening, security, observability, lifecycle, recovery, browser/device support, capacity, and host exposure.

**Architecture:** Tasks P7-T1 through P7-T10 implement and commit every release input. P7-T11 then freezes the resulting clean commit, builds and operator-signs content-addressed artifacts, and creates the candidate lock. Tasks P7-T11 through P7-T17 are source-immutable certification/publishing tasks: they build, execute drills, aggregate evidence, pause for approval, and publish against that lock without changing tracked source or an enumerated release input. Any source, dependency, configuration, migration, image, script, test, or normative-document change after the freeze invalidates the candidate and returns work to its owning implementation task.

**Tech Stack:** Podman/Docker Compose, GitHub Actions, Cosign, Trivy, Syft, govulncheck, Gitleaks, Prometheus, Alertmanager, node-exporter textfile collector, restic, Playwright 1.61.1, k6, LiveKit load tools, shell/JSON evidence

## Global Constraints

- Execution mode is Fable 5 Medium inline: execute one task per run and stop at its stated checkpoint.
- P7-T1 through P7-T10 are source-changing tasks and end in focused commits. P7-T11 through P7-T17 are source-immutable certification/publishing tasks: run them from clean exports, keep generated releases/reports outside Git, and prove the frozen tracked tree plus release-input manifest hash remain unchanged.
- The accepted Phase 6 commit is the immutable `0.1.0-alpha.1` predecessor fixture, not the final beta candidate. The final `0.1.0-beta.1` candidate is the clean post-P7-T10 commit frozen by P7-T11.
- Pin every base image, service image, scanner, build tool, load tool, and CI action by OCI digest or full commit SHA.
- Set `TELOS_CERTIFICATION_ROOT` to an absolute mode-`0700` directory outside every Git checkout/export (for example `/var/lib/telos-certification/0.1.0-beta.1`). Store generated releases/evidence there; commit only schemas, scripts, fixtures, public trust roots, and runbooks.
- The operator owns encrypted offline Cosign and SSH tag-signing private keys. Their reviewed public trust roots are committed, while independently distributed expected fingerprints are pinned in `/etc/telos/release-trust.conf`; private keys/passwords never enter Git, CI, release artifacts, ordinary backups, logs, or screenshots.
- Sign each release manifest, checksum index, candidate lock, and certification JSON as an exact blob with a Cosign bundle. Verification requires the committed public key and the independently pinned expected fingerprint. The Git tag requires the committed SSH allowed-signers trust root plus its independent fingerprint. Published OCI/artifact bytes additionally require GitHub OIDC provenance bound to the expected repository, workflow SHA, frozen source commit, and staged artifact digest.
- No gate passes through an unexplained skip, browser substitution, ignored exit status, retry-masked failure, stale report, synthetic result presented as production history, or release/checksum mismatch.
- At final approval, source CI/vulnerability/license/SBOM/reproducibility/runtime evidence is at most 24 hours old; lifecycle/recovery/browser/manual-accessibility/capacity/exposure/alert-drill evidence is at most 7 days old; final aggregation is at most 1 hour old. A rerun must use the same candidate lock.
- External clean-node, physical-device, off-node backup, restore, capacity, and firewall environments are mandatory. If unavailable, certification remains incomplete.
- Production exposes web traffic through Traefik only. TCP 7881 and TURN/media UDP are the only additional Telos public paths.
- Keep `.env`, repository credentials, and signing material mode `0600`; redact credentials, tokens, personal data, filenames, and private content from reports.

## Entry Gate

- [ ] Phase 6 evidence identifies its accepted commit and migration checksum set through `0018`; record that commit as the `0.1.0-alpha.1` predecessor source.
- [ ] The Phase 6 backend/frontend/integration/browser suite, bundle budget, product-truth scan, and common verification gate pass on that commit.
- [ ] The working tree has no unclassified release input, and every pre-existing user change has been handled through Phase 1.
- [ ] Reserve an external certification host, off-node restic repository, versioned object-lock release-staging store with CI read-only access, current physical iOS/Android devices, current desktop Safari host, and operator alert receiver.
- [ ] Provision independently escrowed Cosign and SSH tag-signing private keys plus independently distributed expected fingerprints for both public trust roots, alongside recovery credentials; expose no secret to the repository or command output.

## File Responsibility Map

| Area | Owning tasks |
|---|---|
| CI, scanners, SBOM | P7-T1 |
| Runtime hardening and limits | P7-T2 |
| Metrics, alerts, incident response | P7-T3 |
| Reproducible build, pins, signatures | P7-T4 |
| Clean-node install and upstream provisioning | P7-T5 |
| Upgrade, rollback, predecessor fixtures | P7-T6 |
| Backup, restore, retention drill tooling | P7-T7 |
| Browser/device certification tooling | P7-T8 |
| Capacity and exposure tooling | P7-T9 |
| Final evidence gate and normative docs | P7-T10 |
| Candidate/predecessor freeze, build, and staging | P7-T11 |
| Install/upgrade/rollback evidence | P7-T12 |
| Recovery/retention evidence | P7-T13 |
| Browser/device evidence | P7-T14 |
| Capacity/exposure evidence | P7-T15 |
| Final aggregation and approval pause | P7-T16 |
| Post-approval attestation, tag, and publication | P7-T17 |

---

## P7-T1: Add CI Security, Secret, License, Container, and SBOM Gates

**Closes:** O02, S06, R03

**Files:**

- Create: `.github/workflows/ci.yml`
- Create: `.github/dependency-review-config.yml`
- Create: `ci/tools.lock`
- Create: `ci/license-policy.json`
- Create: `ci/risk-acceptance.schema.json`
- Create: `scripts/ci/run-local.sh`
- Create: `scripts/ci/validate-sbom.sh`
- Create: `tests/operations/ci_gates_test.sh`
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`

**Outputs:** Source CycloneDX, core/image SPDX JSON, vulnerability reports, license report, coverage reports, and checksums under `$TELOS_CERTIFICATION_ROOT/ci`.

**Mode contract:** `scripts/ci/run-local.sh --source --evidence-out <external-json>` validates an explicit exported source tree. `--candidate-lock <signed-lock> --lock-receipt <receipt> --evidence-out <external-json>` verifies the lock/bundle and independently pinned signer, retrieves only immutable staged objects, scans the complete offline candidate image/source set, and emits canonical JSON naming lock/receipt hashes, object versions, tool locks, SBOMs, findings, and statuses. Both modes reject repository-contained output, adjacent-worktree input, missing images, and mixed identities. P7-T11 operator-signs and verifies the exact candidate JSON.

- [ ] Add failing workflow-policy and dual-mode CLI tests for mutable action refs/tool images, excessive permissions, missing concurrency/timeouts/gates, malformed/unsigned/mixed-candidate reports, absent staged images, adjacent-worktree fallback, and stale SBOM input.
- [ ] Pin every GitHub Action by full commit SHA and every local scanner/tool by reviewed digest; give each job the minimum token and repository permissions.
- [ ] Run Go unit/integration/`-race`/vet/coverage/govulncheck, frontend clean install/lint/unit/build/bundle/Chromium smoke, production npm audit, dependency review, Gitleaks, license policy, Trivy config/image scan, and source/image SBOM creation.
- [ ] Fail on any secret, forbidden/incompatible license, malformed/incomplete SBOM, reachable Go vulnerability, production npm moderate/high/critical finding, or applicable critical image finding. An applicable high image finding fails unless no fixed digest exists and a candidate-bound, operator-signed VEX/risk record names the CVE, evidence, mitigation, owner, and expiration no more than 30 days away; blanket/image-wide waivers are invalid.
- [ ] Enforce at least 70% backend statement coverage overall and 90% for auth, authorization, migrations, proxy, upload, deletion, outbox, and Watch synchronization code.
- [ ] Run `bash tests/operations/ci_gates_test.sh && bash scripts/ci/run-local.sh --source --evidence-out "$TELOS_CERTIFICATION_ROOT/ci/source.json" && bash scripts/ci/validate-sbom.sh "$TELOS_CERTIFICATION_ROOT/ci"`; expected result: both CLI modes are contract-tested, all source gates pass, and every dependency/component is represented.
- [ ] Commit with message `ci: add security license and SBOM gates`.

## P7-T2: Harden Runtime Identities, Filesystems, Limits, Secrets, and Exposure

**Closes:** O01, S05, S08

**Files:**

- Create: `documentation/operations/container-hardening.md`
- Create: `scripts/verify-runtime-hardening.sh`
- Create: `tests/operations/runtime_hardening_test.sh`
- Modify: `backend/Dockerfile`
- Modify: `docker-compose.yml`
- Modify: `.env.example`
- Modify: `config/traefik.yaml`
- Modify: `config/livekit.yaml`

**Required resource defaults:**

| Service | CPU | Memory | PIDs | `nofile` |
|---|---:|---:|---:|---:|
| Traefik | 0.5 | 256 MiB | 100 | 65,536 |
| `telos-migrate` | 1 | 512 MiB | 128 | 16,384 |
| `telos-core` | 2 | 1 GiB | 256 | 65,536 |
| PostgreSQL | 2 | 2 GiB | 256 | 65,536 |
| Redis | 1 | 512 MiB | 128 | 65,536 |
| ClamAV | 2 | 2 GiB | 256 | 16,384 |
| Jellyfin | 4 | 4 GiB | 1,024 | 65,536 |
| Grimmory | 2 | 2 GiB | 512 | 16,384 |
| MariaDB | 1.5 | 1,536 MiB | 256 | 65,536 |
| LiveKit | 4 | 2 GiB | 512 | 131,072 |
| Egress proxy | 0.5 | 256 MiB | 128 | 16,384 |
| Prometheus | 1 | 1 GiB | 128 | 16,384 |
| Alertmanager | 0.25 | 128 MiB | 64 | 4,096 |
| node-exporter | 0.25 | 128 MiB | 64 | 4,096 |

All services use explicit non-root users, `no-new-privileges`, `cap_drop: [ALL]`, read-only roots where supported, narrow writable mounts/tmpfs, five rotated 10 MiB logs, and bounded transports/admission limits from Phases 2–4. `telos-core` receives at least 75 seconds to drain; databases and stateful media services receive at least 120 seconds; operational scripts wait at least that long before treating a stop as failed. Public Traefik entrypoints enforce the Phase 2 in-flight/body/slow-client/keepalive limits and strict SNI. HTTPS and `turn.` TLS require TLS 1.2 or 1.3, reviewed TLS-1.2 AEAD suites, valid hostname/chain, and HTTP-to-HTTPS redirect; obsolete protocol/cipher or default-certificate fallback fails.

**Verifier modes:** `scripts/verify-runtime-hardening.sh --compose docker-compose.yml --env-file tests/fixtures/compose.env --evidence-out <external-json>` renders through the secret-safe Phase 2 validator and checks source/static plus disposable live state. `--candidate-lock <lock> --lock-receipt <receipt> --evidence-out <external-json>` retrieves and boots only immutable offline candidate images, inspects the live candidate, and emits canonical candidate-bound JSON. Neither mode prints rendered values; P7-T11 signs and verifies candidate-mode output.

- [ ] Add failing static/runtime/dual-mode tests for UID 0, writable roots, ambient capabilities, missing resource/ulimit/health/restart/stop-grace policy, broad networks/mounts, loose/short/duplicate/example cursor/HLS/session keys, sensitive logs, edge buffering/admission/slow-client gaps, TLS downgrade/default-SNI, unapproved listeners, mixed candidate identity, and secret-valued output.
- [ ] Run `telos-core` as UID/GID 10001 and each upstream under its documented image-managed non-root identity; use bounded init jobs for volume ownership and the Phase 4 Grimmory group/ACL handoff.
- [ ] Apply the resource table, read-only roots, minimal tmpfs/writable mounts, all-capability drop, admission ceilings, and explicit stop grace periods; document and test any unavoidable capability exception.
- [ ] Require mode-`0600` secret/env files in preflight; validate at least 32 random bytes and bytewise separation for cursor/HLS/session keys; reject unchanged examples by variable name only; redact structured/Traefik logs; and keep secrets out of layers/manifests.
- [ ] Bind only TCP 80/443/7881 and UDP 3478/50000–50100 publicly; keep admin, metrics, health-detail, database, cache, ClamAV, Jellyfin, and Grimmory surfaces internal.
- [ ] Run `bash tests/operations/runtime_hardening_test.sh && bash scripts/verify-runtime-hardening.sh --compose docker-compose.yml --env-file tests/fixtures/compose.env --evidence-out "$TELOS_CERTIFICATION_ROOT/runtime/source.json"`; expected result: both verifier modes are contract-tested and static/live identity, mount, network, edge/TLS, limit, secret, drain, log, and listener policies pass without exposing values.
- [ ] Commit with message `ops: harden runtime limits and exposure`.

## P7-T3: Add Bounded Metrics, Alerts, and Incident Runbooks

**Closes:** O03, M06

**Files:**

- Create: `backend/metrics.go`
- Create: `backend/metrics_test.go`
- Create: `config/prometheus/prometheus.yml`
- Create: `config/prometheus/alerts.yml`
- Create: `config/alertmanager/alertmanager.yml`
- Create: `documentation/operations/monitoring.md`
- Create: `documentation/operations/incidents.md`
- Create: `scripts/verify-monitoring.sh`
- Create: `tests/operations/monitoring_test.sh`
- Modify: `backend/main.go`
- Modify: `backend/health.go`
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`
- Modify: `docker-compose.yml`
- Modify: `scripts/backup.sh`
- Modify: `scripts/restore-drill.sh`
- Modify: `config/egress/allowed-domains.txt`
- Modify: `scripts/tests/egress_test.sh`
- Modify: `documentation/operations/controlled-egress.md`

**Metrics:** Dependency/readiness state, auth throttle, socket/subscription counts, voice-seat reservations, outbox count/oldest age, upload admission/rejection/scan result, free bytes/quota, request latency/status, process/container CPU/memory/restart/OOM/PID/FD state, database-pool saturation, app/`turn.` certificate expiry, narrowly scoped maintenance operation/start/deadline, external dead-man heartbeat, and host-produced backup/restore timestamps. No user, channel, file, party, session, query, or request ID is a metric label.

**Alert thresholds:** dependency/readiness failure 2 minutes; auth denials 20 in 5 minutes; sockets above 75; outbox above 100 for 30 seconds or 1,000 for 2 minutes; 20 upload rejections in 5 minutes; free space below 5 GiB; CPU above 90% for 15 minutes; memory/PID/FD/database-pool usage above 80% for 10 minutes; any OOM or unexpected restart; app/`turn.` certificate expiry warning at 30 days/critical at 14; scheduled-backup age warning at 20 hours/critical at 23; restore-drill age above 30 days; missing external dead-man heartbeat for 5 minutes. Prometheus retention is capped at 15 days and 10 GiB.

`scripts/verify-monitoring.sh` has `--source --evidence-out` and `--candidate-lock ... --lock-receipt ... --evidence-out` modes with the same immutable-identity rules as the other verifiers. It emits canonical alert-delivery, recovery, TLS-expiry, maintenance, cardinality, retention, and dead-man results. Only dependency/readiness alerts for the affected node are inhibited during the root-owned maintenance stop subwindow; inhibition ends after 15 minutes, and overrun/restart failure alerts immediately. A receiver outside the Telos node detects total host/network/engine loss; its limited status metadata and egress are documented.

- [ ] Add failing dual-mode, registration/cardinality/redaction/internal-isolation tests, Prometheus rule tests, receiver/dead-man delivery tests, TLS-expiry/saturation/restart/OOM tests, maintenance inhibition/overrun tests, retention-size tests, stale textfile tests, and a required-runbook-link assertion for every alert.
- [ ] Instrument bounded labels and request/event correlation IDs; expose `/metrics` only on an internal listener unreachable through Traefik.
- [ ] Add digest-pinned Prometheus, Alertmanager, and node-exporter with Phase 7 resource and 15-day/10-GiB storage limits; let backup/restore scripts atomically publish verified success plus bounded maintenance timestamps to a root-owned textfile directory.
- [ ] Generate Alertmanager configuration from a mode-`0600` receiver secret; validate the receiver host against the reviewed egress domain allowlist and never interpolate it into source or reports.
- [ ] Publish dependency/total-host loss, compromise/session theft, auth attack, outbox, upload/malware, low storage, backup/restore/maintenance overrun, TLS expiry, restart/OOM/saturation, voice/media, and capacity incident procedures.
- [ ] Run `scripts/test-backend.sh ops -run 'TestMetrics' && bash tests/operations/monitoring_test.sh && bash scripts/verify-monitoring.sh --source --evidence-out "$TELOS_CERTIFICATION_ROOT/monitoring/source.json" && bash scripts/tests/egress_test.sh`; expected result: both modes, rules, delivery/recovery/dead-man simulations, textfile producers, internal isolation, and redaction pass.
- [ ] Commit with message `ops: add monitoring alerts and incident runbooks`.

## P7-T4: Implement Reproducible, Digest-Pinned, Operator-Signed Release Builds

**Closes:** R03, O01, O02

**Files:**

- Create: `release/images.lock`
- Create: `release/telos-release.pub`
- Create: `release/telos-tag-signers`
- Create: `release/release-manifest.schema.json`
- Create: `release/evidence-envelope.schema.json`
- Create: `release/artifact-index.schema.json`
- Create: `release/hash-graph.md`
- Create: `scripts/verify-release-pins.sh`
- Create: `scripts/build-release.sh`
- Create: `scripts/stage-release.sh`
- Create: `scripts/sign-evidence.sh`
- Create: `scripts/verify-evidence.sh`
- Create: `documentation/operations/release.md`
- Create: `tests/operations/release_test.sh`
- Create: `.github/workflows/release.yml`
- Modify: `backend/Dockerfile`
- Modify: `docker-compose.yml`
- Modify: `.gitignore`

**Release payload:** `telos-core.oci.tar`; `compose.yaml`; candidate source/image SBOM archives; operator runbooks; legal/Credits files; and `telos-ops.tar.zst`, which contains the exact referenced `config/`, install/provision/upgrade/rollback/backup/restore scripts, systemd units, `.env.example`, migration inputs, and public verification trust roots. No adjacent Git checkout supplies runtime input. `artifacts.json` hashes only those payload files. `release-manifest.json` records source commit/tree, version, build epoch, Go/Node/dependency/tool locks, image digests, schema/checksum set, and the `artifacts.json` hash. `SHA256SUMS` hashes every payload plus `artifacts.json` and `release-manifest.json`, but never itself or a signature bundle. Detached Cosign bundles sign the manifest and `SHA256SUMS`. `candidate-lock.json` later hashes all of those files and bundles but not itself; its own bundle remains adjacent. This directed graph is the only permitted hash layout.

**Trust/staging contract:** The operator creates and escrows encrypted Cosign and SSH tag keys out of band, reviews and commits only their public trust roots, and independently pins their fingerprints in `/etc/telos/release-trust.conf`. `scripts/verify-evidence.sh` requires the exact blob, valid bundle, committed key, independently pinned fingerprint, expected schema/release/source, and nonrevoked key. `scripts/stage-release.sh` uploads exact candidate bytes to a versioned object-lock/content-addressed staging store and records immutable object version IDs plus hashes; CI receives read-only access and may never rebuild or overwrite them. The release workflow is manual-dispatch only until certification and publishes only those verified staged versions.

- [ ] Add failing tests for mutable pins, tag-only `FROM`/Compose/tool images, `npm install`, dirty/untracked input, nondeterministic timestamps/ownership, missing payload/SBOM/runbook/legal files, circular/self hashes, wrong independently pinned signer, tampered bundle, mutable staging, workflow rebuild, and output in a committable source path.
- [ ] Resolve and review OCI digests for every base/service/tool image and full SHAs for every release action; consume only `release/images.lock`/`ci/tools.lock`.
- [ ] Build from an explicit committed tree with `SOURCE_DATE_EPOCH`, `npm ci`, verified Go modules, stable file metadata, no local cache, and normalized OCI/archive output.
- [ ] Implement the documented noncircular hash DAG, exact-blob signing/verification with disposable test keys in `/tmp`, SSH tag-signer verification, and immutable staging; production commands refuse a repository-contained/private/loose key or a public key supplied only beside the artifact without the expected out-of-band fingerprint.
- [ ] Build an internal fixture commit twice in separate empty directories and compare normalized manifests/artifact checksums byte-for-byte; final beta output remains deferred to P7-T11.
- [ ] Run `bash tests/operations/release_test.sh && bash scripts/verify-release-pins.sh --source`; expected result: build determinism, schemas, pins, payload/SBOM/runbooks/legal files, hash DAG, staging, signer bootstrap, and failure cases pass.
- [ ] Commit with message `build(release): add reproducible signed release tooling`.

## P7-T5: Automate Clean-Node Installation and Upstream Provisioning

**Closes:** O05

**Files:**

- Create: `scripts/install.sh`
- Create: `scripts/provision-jellyfin.sh`
- Create: `scripts/provision-grimmory.sh`
- Create: `scripts/drills/clean-node-install.sh`
- Create: `documentation/operations/install.md`
- Create: `tests/operations/install_test.sh`
- Create: `tests/fixtures/releases/install-fixture.json`
- Modify: `documentation/architecture/02-deployment.md`

**Interface:** Installation accepts a verified release manifest/bundle, mode-`0600` environment file, independently supplied recovery credentials, and the operator's preprovisioned `/etc/telos/release-trust.conf`. It may read the committed public key from the release only after that key matches the independently pinned fingerprint. It refuses mutable/unverified input and never downloads an unpinned executable.

- [ ] Add failing tests for missing/example/loose secrets, wrong digest/signature/source/schema, unsupported kernel, wrong volume ownership, failed migration/readiness, mutable download, and non-idempotent rerun.
- [ ] Implement prerequisites including required `openat2`/`renameat2` kernel support, artifact verification, host directories, secret permissions, database roles/extensions, migrations, boot, readiness, and functional smoke.
- [ ] Idempotently initialize/reconcile the pinned Jellyfin Telos user/libraries/API key and Grimmory admin/library/watch directory through version-checked APIs without exposing admin credentials.
- [ ] Make a repeated install converge without deleting data, changing user-visible identifiers, widening permissions, or rotating credentials unexpectedly.
- [ ] Run `bash tests/operations/install_test.sh && bash scripts/drills/clean-node-install.sh --fixture tests/fixtures/releases/install-fixture.json`; expected result: success and injected failure paths leave a diagnosable closed node.
- [ ] Commit with message `ops: automate clean-node installation`.

## P7-T6: Automate Defined Predecessor Upgrade and Safe Rollback

**Closes:** O05, D02

**Files:**

- Create: `release/predecessor-policy.json`
- Create: `tests/fixtures/schema-0007.manifest.json`
- Create: `tests/fixtures/releases/candidate-fixture.json`
- Create: `tests/fixtures/releases/predecessor-fixture.json`
- Create: `scripts/build-predecessor.sh`
- Create: `scripts/upgrade.sh`
- Create: `scripts/rollback.sh`
- Create: `scripts/drills/upgrade-rollback.sh`
- Create: `documentation/operations/upgrade-and-rollback.md`
- Create: `tests/operations/upgrade_rollback_test.sh`

**Predecessor contract:** `release/predecessor-policy.json` binds `0.1.0-alpha.1` to the accepted Phase 6 application-source commit and migration checksums through `0018`. Because that source predates Phase 7 release tooling, `scripts/build-predecessor.sh` performs a two-tree build: application bytes come only from the recorded Phase 6 commit, while the P7-T11 frozen builder commit supplies reviewed tool/image digests and packaging. The external signed predecessor manifest/lock records both commits, every payload digest, schema/checksums, output location, trust fingerprint, and immutable staging version. P7-T11 creates it; no task pretends the old source already contained the new release recipe.

**Rollback contract:** A prior application artifact may reuse the database only when both manifests declare the same current schema/checksum set compatible. Otherwise rollback restores the verified pre-upgrade backup; down migrations are never run.

- [ ] Add failing tests for absent/mismatched source or builder commit, missing signed predecessor manifest/staging version, same-artifact “upgrade,” future/incompatible schema, failed orchestration lock/backup/migration/readiness, unsafe binary-only rollback, and failure after traffic closes.
- [ ] Implement one node-wide lock shared by install, backup, prune, upgrade, rollback, restore, and drills; include checked writer stops, fresh verified encrypted backup, migration, readiness, and traffic reopen only after success.
- [ ] Implement and fixture-test the two-tree predecessor builder; seed users/chat/threads plus `clientMutationId` and `channel_changes` high-water, notifications plus `user_events` high-water/read state, files/books/progress/annotations, My List positions/revision/stale-cursor behavior, Watch identity/accepted-successor offer, and channel overrides, then upgrade to a distinct candidate fixture.
- [ ] Prove `0007` to `0018` migration separately, and prove compatible application rollback to the exact predecessor preserves the complete seeded `0018` dataset.
- [ ] Require restore-based rollback when compatibility is absent; injected failures must recover the known predecessor or stay edge-closed with an explicit tested recovery command.
- [ ] Run `bash tests/operations/upgrade_rollback_test.sh && bash scripts/drills/upgrade-rollback.sh --predecessor tests/fixtures/releases/predecessor-fixture.json --candidate tests/fixtures/releases/candidate-fixture.json`; expected result: two source/builder identities, real artifact change, full durable-contract preservation, schema upgrade, compatible rollback, and restore fallback all pass.
- [ ] Commit with message `ops: automate predecessor upgrade and rollback`.

## P7-T7: Complete Atomic Recovery, Retention, and Credential-Escrow Drill Tooling

**Closes:** D07, O05

**Files:**

- Create: `scripts/verify-restored-node.sh`
- Create: `scripts/drills/recovery.sh`
- Create: `tests/operations/backup_restore_test.sh`
- Create: `tests/fixtures/backup-retention-clock.json`
- Modify: `scripts/backup.sh`
- Modify: `scripts/prune-backups.sh`
- Modify: `scripts/restore.sh`
- Modify: `scripts/restore-drill.sh`
- Modify: `documentation/operations/backup-and-restore.md`
- Modify: `documentation/operations/restore-drill.md`
- Modify: `documentation/operations/data-retention.md`

**Full restored-node probe:** readiness; new authentication; restored chat/thread plus message mutation IDs and `channel_changes` high-water; notification/read state plus `user_events` high-water; one file; Jellyfin item; EPUB/PDF progress; private/community annotation/reply; My List positions/revision and stale-cursor response; Watch Party identity plus accepted-successor offer and post-restore lease state; channel override; and voice token. Every old session/invite is invalid and Redis starts empty/rebuildable.

**Credential contract:** Repository location, backend credentials, and restic password are escrowed and rotated independently of the backed-up node. The recovery kit is encrypted out of band, inventoried by fingerprint only, and tested from a clean operator environment.

- [ ] Add failure injection after every database/config/storage mutation; partial restore must atomically swap verified alternate state or automatically restore preserved state while Traefik remains closed.
- [ ] Enforce the shared node lock, release/schema/checksum/component verification, recovery-credential bootstrap, empty Redis, session/invite invalidation, and explicit operator confirmation before destructive replacement.
- [ ] Seed one disposable remote repository through the exact production scheduled-backup path across at least 12 weeks, using the same `scheduled` tag and canonical host/path grouping; prune with the deployed command and prove recoverable coverage for 14 distinct daily buckets plus 8 distinct weekly buckets, allowing one snapshot to satisfy both, then restore representative buckets.
- [ ] Keep production-history proof honest: require a successful timer-produced off-node snapshot no older than 24 hours, but never claim eight weeks of real history before it exists and never let a fresh manual backup satisfy the scheduled-RPO gate.
- [ ] Run `bash tests/operations/backup_restore_test.sh && bash scripts/drills/recovery.sh --fixture --retention-clock tests/fixtures/backup-retention-clock.json`; expected result: atomic rollback, escrow bootstrap, complete Phase 5 durable-contract probes, and the exact deployed daily/weekly grouping policy pass.
- [ ] Commit with message `ops: complete atomic recovery drill tooling`.

## P7-T8: Implement Browser and Physical-Device Certification Tooling

**Closes:** F03, F05, O04

**Files:**

- Create: `scripts/certify-browser-matrix.sh`
- Create: `documentation/operations/browser-certification.md`
- Create: `tests/operations/browser_certification_test.sh`
- Modify: `frontend/e2e/stateful/product.spec.ts`

**Evidence cells:** Installed current stable Chrome, Firefox, and Edge; pinned WebKit automation; mobile Chrome and mobile Safari emulation; manual current Safari desktop; physical current iOS Safari and Android Chrome. Each cell records engine/channel/device/OS/browser versions, source/release lock, results, traces, and signer.

- [ ] Add failing tool tests for missing/currentness-unverified browsers, wrong engine/channel/device/touch/viewport, browser substitution, skipped case, stale report, unresolved retry, release mismatch, and missing manual signature.
- [ ] Keep data isolated by worker and source release; make one diagnostic rerun collect trace/video without turning a reproducible failure green.
- [ ] Define physical flows for authentication, navigation, chat/thread/notification, upload, readers/annotations, media/My List/Watch, voice, reconnect, portrait/landscape rotation, safe areas, keyboard/focus, reduced motion, 200% zoom, and contrast.
- [ ] Distinguish automated axe coverage from the signed manual WCAG 2.2 AA audit; neither alone may claim complete conformance.
- [ ] Run `bash tests/operations/browser_certification_test.sh && bash scripts/certify-browser-matrix.sh --validate-only`; expected result: incomplete, substituted, stale, or unsigned matrices fail.
- [ ] Commit with message `test(beta): add browser certification tooling`.

## P7-T9: Implement Capacity and External-Exposure Certification Tooling

**Closes:** O06

**Files:**

- Create: `tests/load/telos.js`
- Create: `tests/load/watch-parties.js`
- Create: `tests/load/livekit-rooms.yaml`
- Create: `scripts/run-capacity-drill.sh`
- Create: `scripts/audit-host-exposure.sh`
- Create: `documentation/operations/capacity.md`
- Create: `documentation/operations/network-exposure.md`
- Create: `tests/operations/capacity_exposure_test.sh`

**Load:** 100 invite-created members; 25 concurrent authenticated users for 30 minutes; 10 combined chat/annotation durable mutations per second; five active Watch Parties; 25 microphone-only voice participants across rooms.

**Thresholds:** At least 99% request/mutation success; REST p95 at most 750 ms; durable acknowledgement p95 at most 1 second; outbox oldest age below 5 seconds; Watch drift convergence at most 2 seconds; voice join p95 at most 3 seconds; packet loss below 3%; zero restart/OOM; resource/goroutine/socket counts within 10% of baseline after a five-minute cooldown.

The beta reference node is 12 dedicated x86-64 vCPUs, 32 GiB RAM, SSD/NVMe with at least 200 GiB free before fixtures, 1 Gbit/s LAN, and at least 250 Mbit/s symmetric WAN; workload generators run on separate time-synchronized hosts. Evidence records CPU model/count, RAM, storage device/type/free bytes, kernel, container runtime/version, network path/throughput, hardware acceleration, and generator placement. A pass certifies this profile and larger hosts only; operators below it must run and pass the same drill before beta admission.

- [ ] Add a failing dry run that validates the reference-node/generator inventory, exact population/rates/duration/thresholds/tool digests, unique fixture identities, cleanup, metrics availability, and a positive certification-host allow token.
- [ ] Implement HTTP/event, five-Watch-host participant resynchronization, and 25-publisher/subscriber LiveKit workloads with sanitized Prometheus/resource plus exact target/generator inventory evidence; use reviewed direct-play fixtures and report hardware acceleration separately.
- [ ] Make each threshold machine-enforced; a missing metric, partial workload, early exit, or unresolved saturation is failure.
- [ ] Audit listeners/firewall from an external host before and after reboot; allow only TCP 80/443/7881 and UDP 3478/50000–50100, and reject every internal/admin/data listener or Traefik admin route.
- [ ] Run `bash tests/operations/capacity_exposure_test.sh && bash scripts/run-capacity-drill.sh --validate-only && bash scripts/audit-host-exposure.sh --validate-only`; expected result: malformed workloads, targets, evidence, and allowlists fail.
- [ ] Commit with message `test(beta): add capacity and exposure tooling`.

## P7-T10: Implement the Final Evidence Gate and Reconcile Normative Documentation

**Closes:** R02, O02, O03, O04, O05, O06

**Files:**

- Create: `scripts/certify-beta.sh`
- Create: `scripts/verify-accumulated-suite.sh`
- Create: `ci/phase-gates.json`
- Create: `documentation/operations/beta-certification.md`
- Create: `tests/operations/beta_certification_test.sh`
- Create: `tests/operations/accumulated_suite_test.sh`
- Modify: `documentation/README.md`
- Modify: `documentation/architecture/01-system-overview.md`
- Modify: `documentation/architecture/02-deployment.md`
- Modify: `documentation/architecture/03-gateway-and-api.md`
- Modify: `documentation/architecture/04-frontend-architecture.md`
- Modify: `documentation/architecture/05-roadmap-and-licensing.md`
- Modify: `documentation/product/beta-feature-status.md`
- Modify: `documentation/operations/release-inputs.pathspec`

**Output contract:** `beta-certification-pretag.json` validates all candidate/drill evidence before approval; final `beta-certification.json` adds workflow provenance, verified tag object/signature, and published object identities. Both hash/link release/signature, CI/security/license/SBOM, runtime, monitoring, lifecycle, recovery, browser/accessibility, capacity, and exposure evidence and map every stable finding/requirement to a fresh report.

- [ ] Add failing schema/evidence tests for every completion criterion/finding ID, the 24-hour source/runtime and 7-day drill freshness classes, 1-hour final aggregation, required signer/hash, source/version/image set, migration checksum, and candidate-lock mismatch.
- [ ] Rebuild `documentation/operations/release-inputs.pathspec` from reviewed final-tree inclusion/exclusion classes and register every Phase 1–6 exit command in `ci/phase-gates.json`; fail for a missing runtime/config/script/test/legal/normative input, included generated/local/secret path, untraceable operations-bundle payload, or missing/skipped phase command, and make `verify-accumulated-suite.sh` emit a signed candidate-bound report.
- [ ] Reconcile endpoints, permissions, themes, services, controlled egress/privacy, support matrix, resource defaults, RPO/RTO, feature status, licensing/Credits, and explicit OIDC exclusion across runtime and normative docs.
- [ ] Make product-truth/source scans reject Oracle/AI surface, secret, generated output, local link, false privacy/license claim, or visible inert control.
- [ ] Make `scripts/certify-beta.sh` read only immutable candidate/evidence paths, verify every signature/hash/freshness/source relationship, and never rewrite or repair evidence.
- [ ] Run `bash tests/operations/accumulated_suite_test.sh && bash tests/operations/beta_certification_test.sh && bash scripts/check-product-truth.sh && bash scripts/certify-beta.sh --validate-fixtures`; expected result: complete registered fixture evidence passes and every missing/stale/tampered/mixed-release fixture fails.
- [ ] Commit with message `docs(beta): add certification gate and reconcile product truth`.

## P7-T11: Freeze, Build, and Sign the Immutable Beta Candidate

**Closes:** R03

**Source changes:** None. This is the freeze boundary.

**Output:** Signed `candidate-lock.json`, signed `predecessor-lock.json`, complete release/predecessor payloads, immutable staging receipts, and separately signed post-freeze accumulated-suite/CI/runtime/alert reports. The locks record only artifact identity: source/builder trees, release-input manifest hash, hash-DAG nodes, payload/image/SBOM/runbook digests, schema/checksums, trust fingerprints, build logs, tool locks, and staging object version IDs. Evidence references the final lock; the lock never hashes evidence that depends on itself.

- [ ] Require the P7-T10 commit, a clean tracked/untracked tree, no generated release input, and an exact release-input manifest hash; export that commit into isolated work directories and record it as the frozen builder/candidate source.
- [ ] Build the beta payload twice from the frozen commit and the `0.1.0-alpha.1` payload twice from the recorded Phase 6 source plus frozen builder; compare each pair's normalized indexes, manifests, and checksums byte-for-byte.
- [ ] Validate the noncircular hash DAG and sign/verify each manifest and `SHA256SUMS`; generate unsigned `predecessor-stage.json` and `candidate-stage.json` descriptors containing exact payload identities but no future staging receipt or self-dependent evidence.
- [ ] Upload exact bytes with `bash scripts/stage-release.sh --immutable --stage-descriptor <descriptor> --output-receipt <external-path>`; read them back by object version, verify every hash, and grant CI read-only access.
- [ ] Generate and sign final predecessor/candidate locks from the verified descriptors plus receipts; verify both locks and ensure neither contains accumulated-suite or other lock-dependent evidence.
- [ ] Regardless of pre-freeze report age, run and sign `bash scripts/verify-accumulated-suite.sh --candidate-lock <lock>`, `bash scripts/ci/run-local.sh --candidate-lock <lock>`, `bash scripts/verify-runtime-hardening.sh --candidate-lock <lock>`, and `bash tests/operations/monitoring_test.sh --candidate-lock <lock>` against the staged/read-back candidate; require Phase 1–6, vulnerability/SBOM/image, live runtime-hardening, and alert-delivery reports to name that lock.
- [ ] Run `bash scripts/verify-release-pins.sh --candidate <lock> && bash tests/operations/release_test.sh --candidate <lock>`, prove the tracked tree/release-input hash is unchanged, and stop. Any release-input change discards both locks, staged objects, and evidence.

## P7-T12: Execute Clean Installation, Defined Upgrade, and Rollback Certification

**Closes:** D02, O05

**Source changes:** None. Run from clean exports against only the P7-T11 locks.

- [ ] On external node A, install the signed candidate from zero using the independently pinned trust fingerprint, rerun installation for idempotency, and execute the complete functional smoke.
- [ ] On external node B, install the exact signed/staged `0.1.0-alpha.1` predecessor, seed every durable Phase 5 contract, take a verified encrypted backup, and upgrade to the distinct beta candidate.
- [ ] Exercise compatible application rollback and incompatible restore-based rollback; prove users, change high-waters, mutation IDs, notification reads, annotations, My List revision/order, Watch successor state, and channel overrides remain correct.
- [ ] Apply the final migrator to the verified schema-`0007` fixture and prove checksum-contiguous upgrade through `0018` without data loss.
- [ ] Generate and operator-sign `node-lifecycle.json` with source/builder identities, complete data probes, failure injections, timings, and lock hashes.
- [ ] Run `bash scripts/drills/clean-node-install.sh --candidate-lock <lock> --execute && bash scripts/drills/upgrade-rollback.sh --candidate-lock <lock> --predecessor-lock <predecessor-lock> --execute`; expected result: install, real artifact upgrade, schema upgrade, and both rollback modes pass. Recheck the frozen tree/input hash and stop.

## P7-T13: Execute Encrypted Recovery and Retention Certification

**Closes:** D07, O05

**Source changes:** None. Run from clean exports against only the P7-T11 candidate lock.

- [ ] Select the latest successful timer-produced off-node snapshot without taking a substitute manual backup; require incident-time snapshot age at most 86,400 seconds and verify repository/checksums/components before restore.
- [ ] Restore it to a clean external node and run every Phase 5 restored-node probe, including event/change high-waters, mutation IDs, My List revision/stale cursor, Watch successor/lease recovery, and deletion/retention behavior.
- [ ] Verify old sessions/invites fail, Redis begins empty, pre-snapshot sentinels exist, post-snapshot sentinels do not, RTO is at most 14,400 seconds, and both escrow paths bootstrap a disposable recovery.
- [ ] Run `restic check` on the real repository and record only elapsed real scheduled history; do not label a fresh backup or time-shifted fixture as real RPO/retention history.
- [ ] In a separate repository, execute the exact production scheduler/tag/host/path grouping over the time-shift fixture and prove 14 daily plus 8 weekly recoverable buckets, with overlaps counted once, then restore representative buckets.
- [ ] Generate/sign `restore.json`, then run `bash scripts/drills/recovery.sh --candidate-lock <lock> --execute`; expected result: atomic recovery, RPO/RTO, session invalidation, complete durable state, escrow, and deployed retention policy pass. Recheck frozen inputs and stop.

## P7-T14: Execute the Supported Browser and Physical-Device Matrix

**Closes:** F03, F05, O04

**Source changes:** None. Run from clean exports against only the P7-T11 candidate lock.

- [ ] Verify installed Chrome, Firefox, Edge, Safari, iOS Safari, and Android Chrome are current stable releases on certification day; record exact versions without changing source.
- [ ] Run all automated projects against the immutable candidate with isolated users/data and server-side cleanup; reject unexpected skips, substitutions, stale reports, and correctness retries.
- [ ] Use one rerun only to capture a failure trace/video; a reproducible failure remains failed and invalidates certification.
- [ ] Execute signed desktop Safari and physical iOS/Android flows, including portrait-to-landscape state/safe-area checks and the complete manual keyboard/focus/motion/zoom/contrast/reader audit.
- [ ] Generate and operator-sign `browser-matrix.json` with every required cell, trace hash, source/release lock, device/OS/browser version, and manual signer.
- [ ] Run `bash scripts/certify-browser-matrix.sh --candidate-lock <lock> --evidence <browser-matrix.json>`; expected result: every automated/manual cell passes. Recheck frozen inputs and stop.

## P7-T15: Execute Capacity and External Host-Exposure Certification

**Closes:** O06

**Source changes:** None. Run from clean exports against only the P7-T11 candidate lock.

- [ ] Verify and record the approved reference-node and external-generator hardware/runtime/network inventory before applying load.
- [ ] Run the exact 30-minute HTTP/event, five-Watch-Party, and 25-participant LiveKit workload while collecting candidate-bound Prometheus/resource evidence.
- [ ] Enforce every success, latency, outbox, drift, join, packet-loss, restart/OOM, and cooldown threshold; no partial workload or missing metric is acceptable.
- [ ] From an external network, scan TCP/UDP and compare firewall/listeners before and after reboot; require exactly the approved web/media allowlist and no internal/admin/data/health-detail reachability.
- [ ] Generate and operator-sign `capacity.json` and `exposure.json`, including target/generator inventory and sanitized raw evidence hashes.
- [ ] Run `bash scripts/run-capacity-drill.sh --candidate-lock <lock> --execute && bash scripts/audit-host-exposure.sh --candidate-lock <lock> --execute`; expected result: both pass. Recheck frozen inputs and stop.

## P7-T16: Aggregate Fresh Evidence and Pause for Operator Approval

**Closes:** R02, R03, O02, O03, O04, O05, O06

**Source changes:** None. This task never creates or pushes a tag.

- [ ] Rerun any source/runtime report older than 24 hours or drill report older than 7 days on the same lock, then verify the accumulated suite, CI, runtime, alert, lifecycle, recovery, browser, capacity, and exposure envelopes.
- [ ] Run `bash scripts/certify-beta.sh --pretag --candidate-lock <lock> --evidence-dir "$TELOS_CERTIFICATION_ROOT"`; require one frozen source/input hash, predecessor, payload/image set, trust fingerprint set, schema checksum set, and immutable staging receipt.
- [ ] Generate/operator-sign `beta-certification-pretag.json` no more than one hour before presentation; map every finding/requirement to fresh passing evidence and list residual operator assumptions.
- [ ] Independently read back every staged object version and compare it to the candidate lock; prove the release workflow can download but cannot overwrite or rebuild it.
- [ ] Present the frozen commit, staged payload hashes, reference-node scope, recovery/browser assumptions, and pre-tag certification hash to the operator.
- [ ] Stop and wait for explicit approval that names `0.1.0-beta.1`, the frozen commit, candidate-lock hash, and pre-tag certification hash. A later “continue” without that approval does not authorize P7-T17.

## P7-T17: Attest, Sign, Tag, and Publish the Approved Immutable Release

**Closes:** R02, R03, O02, O03, O04, O05, O06

**Source changes:** None. Entry requires the explicit P7-T16 approval record.

- [ ] Verify the approval identifiers, candidate/predecessor locks, freshness windows, tracked tree/release-input hash, Cosign fingerprint, and SSH tag-signer fingerprint before any external publication.
- [ ] Manually dispatch the pinned release workflow at the frozen commit in attest-only mode; it downloads exact immutable staged object versions, verifies hashes/signatures, publishes only content-addressed private blobs, and emits GitHub OIDC provenance without rebuilding or assigning the public beta tag.
- [ ] Verify provenance against repository, workflow SHA, frozen commit, and every staged digest; generate/operator-sign `release-attestation-pretag.json` binding that provenance, the candidate lock, staged objects, and intended tag.
- [ ] Create an SSH-signed annotated `v0.1.0-beta.1` tag at the frozen commit whose message binds the candidate lock, `beta-certification-pretag.json`, and `release-attestation-pretag.json` hashes; verify it through `release/telos-tag-signers` and the independently pinned fingerprint.
- [ ] Push the verified tag and dispatch promote mode to attach the already-attested exact assets and assign public version tags; if promotion fails, admit no beta member and retry promotion without rebuilding.
- [ ] After promotion identities exist, generate/operator-sign the separate final `beta-certification.json`, then run `bash scripts/certify-beta.sh --final --tag v0.1.0-beta.1 --candidate-lock <lock> --evidence-dir "$TELOS_CERTIFICATION_ROOT"`; expected result: tag signature, operator signatures, CI provenance, published bytes, final evidence, and source agree without any self-hash cycle. Recheck frozen inputs and stop.

## Phase 7 and Program Exit Gate

- [ ] Every source-changing operational task is committed before P7-T11; the frozen tracked tree and release-input manifest hash remain unchanged through P7-T17.
- [ ] Reproducible digest-pinned payload/operations bundles, legal files, runbooks, SBOMs, noncircular hashes, independent trust pins, immutable staging receipts, operator signatures, tag signature, and OIDC provenance identify the same candidate.
- [ ] The signed accumulated-suite report proves every Phase 1–6 exit gate passed again on the frozen candidate within the source-evidence freshness window.
- [ ] CI reports zero blocked vulnerability, secret, license, container, dependency, coverage, or SBOM finding.
- [ ] Runtime identities, capabilities, filesystems, resources, stop grace, admission limits, secrets, networks, logs, and exposure pass static and live inspection.
- [ ] Every required metric, host-produced backup/restore timestamp, alert, receiver, and runbook is exercised.
- [ ] Clean install, two-tree signed predecessor upgrade, schema-`0007` migration, compatible rollback, restore fallback, and injected failure paths pass with every Phase 5 durable contract.
- [ ] A scheduled encrypted off-node backup meets RPO at most 24 hours; clean-node recovery meets RTO at most 4 hours; the deployed grouping policy proves 14 daily/8 weekly recoverable buckets without misrepresenting elapsed real history.
- [ ] Automated current Chrome/Firefox/Edge plus WebKit/mobile projects and signed current Safari/iOS/Android physical checks pass without unresolved exception.
- [ ] Capacity meets the approved population/concurrency/rate/voice/Watch targets on the recorded reference node/external generators without leak, restart, or OOM.
- [ ] External firewall/listener scans expose only approved Telos web/media ports and no unrelated/admin/data service.
- [ ] Every stable finding and approved beta requirement maps to fresh, candidate-bound, signed evidence.
- [ ] Normative documentation, runtime, UI, legal/product truth, service inventory, privacy model, and feature status agree.
- [ ] P7-T16 records explicit approval of exact hashes; P7-T17 attests exact staged bytes before creating the verified SSH-signed tag and public release, with no rebuild.
