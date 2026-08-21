# Standalone Beta Phase 5: Candidate Certification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build, test, sign, and publish exactly one immutable Telos beta candidate whose server, browser client, desktop packages, recovery artifacts, and evidence all share one verifiable identity.

**Architecture:** Export a signed source tag, build deterministic server/client OCI artifacts, build desktop packages on native runners, assemble them once into a non-circular hash graph, and freeze a candidate lock before testing. Every local and external gate consumes those artifacts without rebuilding and emits a strict Phase 1 envelope bound to the lock. Guided browser/desktop checks produce signed, candidate-bound records. Certification verifies the exact required gate set and signs an evidence index. Publication only promotes a draft release containing the already-certified bytes.

**Tech Stack:** Git signed tags, Podman/Buildah, Tauri native GitHub Actions runners, Python 3 standard library, Bash, OpenSSH `ssh-keygen -Y sign/verify`, GitHub Actions/Releases, SHA-256, Phase 1 evidence/gate runner, Phase 4 SBOM/scanning.

**Spec:** `docs/superpowers/specs/2026-08-20-standalone-beta-readiness-design.md`

## Global Constraints

- Begin only after Phase 1–4 implementations are merged and reviewed. Freeze from a clean, signed commit on the release branch; never certify a dirty checkout, merge preview, unpushed commit, or rebuilt substitute.
- Version format is `0.1.0-beta.N` with positive integer `N`. The same version appears in the Git tag, release manifest, frontend package, Cargo package, Tauri config, desktop metadata, and artifact filenames.
- A release build exports the signed tag with `git archive`; it does not build from the caller's working tree. `SOURCE_DATE_EPOCH` is the signed commit time.
- Desktop packages are built on native Linux, Windows, and macOS runners from the same tag. The Linux assembler imports them only after verifying source commit, version, workflow run/attempt, platform, and raw SHA-256 sidecars.
- Candidate hash graph is acyclic:
  1. payload bytes produce artifact digests;
  2. artifact index names those digests;
  3. release manifest hashes the artifact index and release inputs;
  4. candidate lock hashes the manifest/index and names every artifact digest;
  5. gate envelopes name the candidate-lock digest;
  6. evidence index hashes immutable envelopes/raw-log digests;
  7. SSH signatures cover the evidence index/certification record.
- Candidate lock, evidence, signatures, and checksum files never claim to hash themselves or a downstream node.
- A frozen candidate is never rebuilt after a failing gate. Fixes create a new commit, version/tag, build, candidate lock, and evidence set.
- Candidate gates install/load only artifacts named by `candidate-lock.json`. Commands reject mutable image tags and paths not present in the artifact index.
- Every required gate is exactly one fresh `passed` envelope. `failed`, `not_run`, duplicate, missing, stale, unknown, wrong-candidate, wrong-source, wrong-runtime, wrong-artifact, or invalidly signed evidence blocks certification.
- Default maximum evidence age is 14 days. Clean install, runtime hardening, backup/restore, upgrade/rollback, exposure/egress, capacity, and supply-chain evidence is at most 7 days old when certification is signed. The catalog owns exact `maxAgeHours`.
- Guided manual records are explicit per assertion and operator-signed. A checkbox left unanswered is `not_run`; a narrative claim cannot replace required fields.
- Required browser matrix: current Chrome, Firefox, Edge, automated WebKit, desktop Safari, iOS Safari, and Android Chrome, with exact observed versions recorded. WebKit automation does not substitute for Safari device checks.
- Required desktop matrix: the exact unsigned Linux AppImage, Windows NSIS installer, and macOS universal DMG. Build success does not substitute for install/restart/keychain/revocation/media journeys.
- Private signing keys never enter the repository, CI logs, evidence archive, release archive, or command arguments as key material. Key file paths may be provided through environment and must be mode `0600`.
- Public trusted signer files contain real OpenSSH public keys and no ellipses/synthetic values. If an authorized operator key has not been enrolled and reviewed, signing/certification is `not_run`.
- Release publication has `contents: write` only in the promotion job. Candidate builds have `contents: read`; no job has `id-token: write` unless an implemented attestation verifier consumes it.
- Release assets are uploaded once without overwrite. Publication promotes the already-verified draft; it does not compile, package, rename, replace, or re-sign payload bytes.

---

## Current-State Baseline

The current evidence and release-manifest schemas accept only a few unconstrained fields (`release/evidence-envelope.schema.json:1-10`, `release/release-manifest.schema.json:1-11`). Release build, staging, and evidence-signing scripts only print completion (`scripts/build-release.sh:1-5`, `scripts/stage-release.sh:1-5`, `scripts/sign-evidence.sh:1-5`), and beta certification can announce success without verifying an artifact graph (`scripts/certify-beta.sh:18-24`). Phase 1 makes these paths fail closed; this phase replaces them with one immutable candidate graph and verified promotion.

---

## File Structure

- Create: `release/candidate-lock.schema.json` — immutable candidate identity contract.
- Modify: `release/artifact-index.schema.json`, `release/release-manifest.schema.json`, `release/hash-graph.md` — strict acyclic release graph.
- Create: `release/evidence-index.schema.json`, `release/certification.schema.json` — signed evidence/certification contracts.
- Delete: `release/telos-release.pub` — invalid unused trust artifact.
- Modify: `release/telos-tag-signers` — real reviewed SSH tag signers.
- Create: `release/evidence-signers` — real reviewed SSH evidence signers.
- Create: `scripts/enroll-release-signer.py`, `tests/operations/signer_trust_test.sh` — safe public-key enrollment/validation.
- Modify: `scripts/build-release.sh`, `scripts/tests/reproducible-build-test.sh`, `tests/operations/release_test.sh` — deterministic one-time candidate assembly.
- Create: `scripts/lib/release_graph.py`, `docker-compose.release.yml` — manifest/hash graph and no-build release override.
- Create: `.github/workflows/candidate.yml` — native multi-OS candidate build/assembly.
- Modify: `scripts/certify-browser-matrix.sh`, `tests/operations/browser_certification_test.sh` — real hosted-browser matrix.
- Create: `scripts/record-guided-smoke.py`, `release/guided-smoke.schema.json`, `tests/operations/guided_smoke_test.sh` — structured browser/desktop manual records.
- Create: `scripts/import-evidence.py` — immutable evidence bundle assembly.
- Modify: `scripts/verify-evidence.sh`, `scripts/sign-evidence.sh`, `scripts/certify-beta.sh`, `tests/operations/beta_certification_test.sh` — strict aggregation/signing/certification.
- Modify: `ci/phase-gates.json`, `scripts/verify-accumulated-suite.sh` — exact candidate gate set and freshness.
- Modify: `scripts/stage-release.sh`, `.github/workflows/release.yml`, `documentation/operations/release.md`, `documentation/operations/beta-certification.md` — draft staging and no-rebuild promotion.
- Modify: `scripts/check-release-truth.sh`, `scripts/check-product-truth.sh`, `scripts/verify-clean-checkout.sh`, `.github/workflows/ci.yml` — final structural enforcement.

---

### Task 1: Define the release graph and enroll real trust anchors

**Files:**
- Create: `release/candidate-lock.schema.json`
- Modify: `release/artifact-index.schema.json`
- Modify: `release/release-manifest.schema.json`
- Create: `release/evidence-index.schema.json`
- Create: `release/certification.schema.json`
- Create: `release/guided-smoke.schema.json`
- Modify: `release/hash-graph.md`
- Delete: `release/telos-release.pub`
- Modify: `release/telos-tag-signers`
- Create: `release/evidence-signers`
- Create: `scripts/enroll-release-signer.py`
- Create: `tests/operations/signer_trust_test.sh`
- Modify: `tests/operations/release_test.sh`

**Interfaces:**
- Produces: closed schemas for artifacts, release manifest, candidate lock, guided records, evidence index, and certification.
- Produces: OpenSSH allowed-signers trust anchors for Git tags and evidence.
- Consumes later: release builder, evidence importer/verifier, certifier, publisher.

- [ ] **Step 1: Write graph/schema/trust mutation tests**

Create a complete tiny fixture graph and assert rejection of unknown fields, missing artifact, wrong path/digest/size/media type/platform, duplicate path/digest, path traversal, absolute path, symlink artifact, mismatched source/version, candidate hashing itself, evidence included in candidate payload, wrong candidate digest, missing required evidence, duplicate gate, invalid status, reversed times, expired record, guided assertion omission, invalid public key, repeated/fabricated key material, unauthorized principal, wrong SSH namespace, and signature over changed bytes.

- [ ] **Step 2: Run tests and expose current permissive schemas/invalid key material**

```bash
bash tests/operations/release_test.sh
bash tests/operations/signer_trust_test.sh
```

Expected: nonzero because existing schemas barely constrain content and committed trust files are not valid usable keys.

- [ ] **Step 3: Define strict schemas**

Use JSON Schema 2020-12 with `additionalProperties: false` throughout. Core shapes:

```json
{
  "schemaVersion": 1,
  "artifacts": [{
    "path": "images/telos-core.oci.tar",
    "kind": "oci-image",
    "platform": "linux/amd64",
    "size": 123,
    "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "contentDigest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  }]
}
```

```json
{
  "schemaVersion": 1,
  "version": "0.1.0-beta.1",
  "sourceCommit": "0000000000000000000000000000000000000000",
  "sourceTag": "v0.1.0-beta.1",
  "sourceDateEpoch": 1787234400,
  "artifactIndexDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "imagesLockDigest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "migrationManifestDigest": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
  "buildInputs": {
    "npmLock": "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
    "cargoLock": "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
    "goSum": "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
  }
}
```

```json
{
  "schemaVersion": 1,
  "version": "0.1.0-beta.1",
  "sourceCommit": "0000000000000000000000000000000000000000",
  "artifactIndexDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "releaseManifestDigest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "artifacts": {"images/telos-core.oci.tar": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}
}
```

Candidate lock repeats the complete artifact digest map so it can be verified without trusting index traversal. It excludes checksums, evidence, signatures, and itself.

- [ ] **Step 4: Implement canonical graph validation**

`scripts/lib/release_graph.py` performs schema/type/pattern/closed-key validation, canonical JSON serialization, raw file hashing, safe relative path resolution, regular-file/no-symlink checks, artifact completeness, cross-document identity/digest equality, and acyclicity assertions. Expose:

```text
release_graph.py build-index --root PATH --manifest-list PATH --out PATH
release_graph.py build-manifest --root PATH --metadata PATH --out PATH
release_graph.py build-candidate-lock --root PATH --out PATH
release_graph.py verify --root PATH
```

Every writer uses sibling temp, `fsync`, mode `0600`, and atomic rename.

- [ ] **Step 5: Implement public-key enrollment and remove invalid trust data**

`enroll-release-signer.py --purpose tag|evidence --principal PRINCIPAL --public-key-file PATH` accepts only one valid `ssh-ed25519` or hardware-backed SSH signing public key, prints its SHA-256 fingerprint, rejects private keys/comments with control characters/duplicate key/fabricated ellipses, and atomically updates the matching allowed-signers file after explicit fingerprint confirmation. `--verify-files` parses both committed allowed-signers files, validates every key with `ssh-keygen`, rejects duplicate principals/keys, and prints fingerprints.

Remove `telos-release.pub`. Before candidate work, an authorized operator runs enrollment with actual public key files and reviews the commit. If no real key is available, stop with `not_run`; do not generate or commit a private key for convenience.

- [ ] **Step 6: Run schema/trust tests**

```bash
bash tests/operations/release_test.sh
bash tests/operations/signer_trust_test.sh
python3 -m py_compile scripts/lib/release_graph.py scripts/enroll-release-signer.py
python3 scripts/enroll-release-signer.py --verify-files
```

Expected: tests exit `0`; both signer files parse and fingerprints match the reviewed change.

- [ ] **Step 7: Commit schemas and real trust anchors**

```bash
git rm release/telos-release.pub
git add release/candidate-lock.schema.json release/artifact-index.schema.json \
  release/release-manifest.schema.json release/evidence-index.schema.json \
  release/certification.schema.json release/guided-smoke.schema.json \
  release/hash-graph.md release/telos-tag-signers release/evidence-signers \
  scripts/lib/release_graph.py scripts/enroll-release-signer.py \
  tests/operations/release_test.sh tests/operations/signer_trust_test.sh
git commit -m "release: define candidate graph and signer trust"
```

---

### Task 2: Build the immutable candidate once on native runners

**Files:**
- Modify: `scripts/build-release.sh`
- Create: `docker-compose.release.yml`
- Modify: `scripts/tests/reproducible-build-test.sh`
- Modify: `tests/operations/release_test.sh`
- Create: `.github/workflows/candidate.yml`
- Modify: `scripts/verify-desktop-artifact.py`
- Modify: `scripts/generate-sbom.sh`
- Modify: `scripts/scan-candidate.sh`
- Modify: `documentation/operations/release.md`

**Interfaces:**
- Produces: `telos-${version}-candidate.tar.zst` and detached SHA-256 from one signed tag.
- Produces payload: source archive, core/client OCI archives, three desktop packages, deploy generation, SBOMs, legal/runbooks, artifact index, manifest, candidate lock, and `SHA256SUMS`.
- Consumes: Phase 3 desktop build contract and Phase 4 image/tool locks.

- [ ] **Step 1: Add deterministic build and input-substitution tests**

Extend release tests with fake native packages/identity sidecars and stub OCI builders. Assert rejection of dirty/unsigned/mismatched tag, version mismatch, desktop wrong commit/run/platform/digest, missing platform, symlink/path escape, mutable image, missing migration/config/runbook, secret/local build output, changed payload after indexing, and caller output inside repository. Build the same fixture twice with different temp paths/time zones/locales/umasks and require identical graph documents and logical OCI content digests.

- [ ] **Step 2: Run release/reproducibility tests and observe placeholder builder**

```bash
bash tests/operations/release_test.sh
bash scripts/tests/reproducible-build-test.sh
```

Expected: nonzero until the builder assembles and verifies the full standalone distribution.

- [ ] **Step 3: Define the no-build deployment override**

`docker-compose.release.yml` removes `build:` for core/client/migrator jobs and requires exact candidate image variables:

```yaml
services:
  telos-core:
    image: ${TELOS_CORE_IMAGE:?candidate core image is required}
  telos-client:
    image: ${TELOS_CLIENT_IMAGE:?candidate client image is required}
  telos-migrate:
    image: ${TELOS_CORE_IMAGE:?candidate core image is required}
```

The generation includes a mode-`0600` non-secret `images.env` containing digest-addressed local images after load. Candidate install/upgrade always layers the override and refuses `--build`.

- [ ] **Step 4: Implement exact-source assembly**

Expose:

```text
scripts/build-release.sh --tag TAG --version VERSION \
  --desktop-dir PATH --out-dir ABSOLUTE_PATH
```

Verify signed annotated tag with `release/telos-tag-signers`, require tag/version/source consistency, export with `git archive`, set deterministic locale/timezone/umask and `SOURCE_DATE_EPOCH`, and build core/client with locked bases plus `podman build --timestamp`. Export OCI archives without pulling after lock verification. Import/verify native artifacts. Copy only the tracked deployment/runtime/legal/runbook inventory. Generate SBOMs, run candidate scan, build graph documents, write deterministic `SHA256SUMS`, verify the graph, and package with normalized owner/group/mtime/order.

Refuse `.env`, `.telos`, secrets, caches, node_modules, build targets, evidence, local volumes, Git metadata, or absolute paths.

- [ ] **Step 5: Make reproducibility semantic and complete**

Build twice from the tag in distinct temporary roots, second with no cache. Require identical Go binary, frontend static tree, OCI index/manifest/config/layer digests, deployment tree, SBOM package inventories (allow only generated-at field normalization), graph documents, candidate lock, and packaged file list. If outer archive compression bytes differ, diagnose and normalize; do not weaken comparison to filenames.

- [ ] **Step 6: Create the native candidate workflow**

Workflow inputs: signed tag and version. Jobs:

1. validate signed tag/locks/schemas on Ubuntu;
2. build Linux AppImage on Ubuntu, Windows NSIS on Windows, macOS universal DMG on macOS;
3. each writes source/version/run/attempt/platform/artifact digest identity and uploads immutable same-run artifact;
4. Linux assembler downloads all three, verifies them, builds core/client once, creates candidate archive/checksum, verifies graph/reproducibility/SBOM/scan, and uploads candidate artifact.

Use `permissions: contents: read`, pinned actions by commit SHA, no signing secrets, and retention long enough for the 14-day certification window. Artifact names include version and candidate-lock digest after assembly.

- [ ] **Step 7: Run local fixture/reproducibility checks**

```bash
bash tests/operations/release_test.sh
bash scripts/tests/reproducible-build-test.sh
bash scripts/verify-release-pins.sh
bash scripts/verify-clean-checkout.sh --inventory-only
```

Expected: all exit `0`.

- [ ] **Step 8: Build one non-release rehearsal tag**

Use an authorized signed rehearsal tag on a clean commit, run the full native workflow, download the assembled archive twice, verify its checksum/graph, load both OCI images, inspect version/source labels, and install each desktop artifact. Delete only the rehearsal workflow artifacts/draft after review; do not reuse them as beta evidence.

- [ ] **Step 9: Commit candidate building**

```bash
git add scripts/build-release.sh docker-compose.release.yml \
  scripts/tests/reproducible-build-test.sh tests/operations/release_test.sh \
  .github/workflows/candidate.yml scripts/verify-desktop-artifact.py \
  scripts/generate-sbom.sh scripts/scan-candidate.sh documentation/operations/release.md
git commit -m "release: assemble one immutable standalone candidate"
```

---

### Task 3: Capture the real browser and desktop matrices through guided evidence

**Files:**
- Modify: `scripts/certify-browser-matrix.sh`
- Modify: `tests/operations/browser_certification_test.sh`
- Create: `scripts/record-guided-smoke.py`
- Create: `tests/operations/guided_smoke_test.sh`
- Modify: `frontend/playwright.config.ts`
- Modify: `frontend/e2e/bootstrap.spec.ts`, `frontend/e2e/chat.spec.ts`, `frontend/e2e/stream.spec.ts`, `frontend/e2e/library.spec.ts`, `frontend/e2e/files.spec.ts`, `frontend/e2e/failure-recovery.spec.ts` — candidate-bound browser journeys.
- Modify: `documentation/operations/browser-certification.md`
- Modify: `documentation/operations/desktop-beta.md`
- Modify: `ci/phase-gates.json`

**Interfaces:**
- Produces automated browser subrecords: Chrome, Firefox, Edge, WebKit.
- Produces guided external subrecords: desktop Safari, iOS Safari, Android Chrome, Linux Tauri, Windows Tauri, macOS Tauri.
- Produces aggregate evidence gates: `browser-matrix`, `desktop-linux`, `desktop-windows`, `desktop-macos`.

- [ ] **Step 1: Add guided-record adversarial tests**

Test exact required assertion IDs, stable ordering, all-answer requirement, observed OS/browser/app version, device/model, artifact/candidate digest, start/end, tester principal, raw attachment digests, and status mapping. Reject prefilled pass, unknown/missing assertion, skipped assertion represented as pass, unsupported platform alias, candidate mismatch, future/reversed time, duplicate record, answer edited after signing, and secrets in notes/attachments metadata.

- [ ] **Step 2: Replace browser existence tests with aggregation behavior**

Fixture automated reports and guided records. Assert browser certification requires all seven browser entries, exact candidate, every required journey, no failed/skipped test, screenshots/traces only on allowed non-secret states, and exact versions. WebKit cannot satisfy either Safari record; desktop Chrome cannot satisfy Android Chrome; responsive emulation cannot satisfy iOS/Android devices.

- [ ] **Step 3: Run tests and observe current completion-only script**

```bash
bash tests/operations/guided_smoke_test.sh
bash tests/operations/browser_certification_test.sh
```

Expected: nonzero because no guided schema/recorder or real aggregator exists.

- [ ] **Step 4: Implement the guided recorder**

Expose:

```text
record-guided-smoke.py --matrix browser|desktop --platform ID \
  --candidate-lock PATH --artifact PATH --out PATH
```

It verifies candidate/artifact first, collects tool-detected OS/app/browser versions where possible, displays one numbered assertion at a time, accepts only `pass`, `fail`, or `not_run` plus bounded notes, hashes declared attachments, redacts credential-like input, and atomically writes a closed record. It cannot write overall `passed`; the aggregator derives it only when all assertions pass.

Browser assertions cover trusted HTTPS, owner/invite/member auth, Chat, Stream video/audio, Library EPUB/PDF/audiobook, Files upload/download/delete, commentary, Settings/Admin, logout/login, upstream degradation, offline/reconnect, and responsive/accessibility-critical interactions. Desktop assertions use the Phase 3 install/connect/keychain/restart/refresh/revocation list plus all four modules.

- [ ] **Step 5: Configure real automated browser projects**

Run Playwright against the frozen candidate URL, never mock APIs. Projects are Chromium/Chrome, Firefox, WebKit, and Edge channel on a compatible runner. Record exact executable versions, candidate health identity, test result JSON, trace only on failure, and console/network error summaries with URL query redaction. Use unique invited accounts and clean them up.

- [ ] **Step 6: Implement browser aggregation**

Expose:

```text
certify-browser-matrix.sh --candidate-lock PATH --records-dir PATH \
  --evidence-out PATH
```

Validate automated/guided schemas, candidate identity, exact required set, journey completeness, version capture, timestamps/freshness, attachment hashes, and no duplicate substitutions. Write one Phase 1 envelope with a subject digest over the canonical matrix index. Any unavailable device/browser returns `3`/`not_run`.

- [ ] **Step 7: Run fixture and automated checks**

```bash
bash tests/operations/guided_smoke_test.sh
bash tests/operations/browser_certification_test.sh
cd frontend
npx playwright test --project=chromium --project=firefox --project=webkit
```

Expected: fixture tests exit `0`; local Playwright passes against the declared candidate-compatible test stack but is not external candidate evidence unless it uses the frozen candidate URL.

- [ ] **Step 8: Complete a rehearsal matrix**

Run automated records against the rehearsal candidate and guided records on physical/hosted Safari, iOS, Android, and three desktop OSes. Aggregate, review redaction, restart/revocation behavior, and platform warning instructions. Any inaccessible platform remains `not_run`; this validates the workflow but cannot certify beta.

- [ ] **Step 9: Commit guided certification tooling**

```bash
git add scripts/certify-browser-matrix.sh scripts/record-guided-smoke.py \
  tests/operations/browser_certification_test.sh tests/operations/guided_smoke_test.sh \
  frontend/playwright.config.ts frontend/e2e documentation/operations/browser-certification.md \
  documentation/operations/desktop-beta.md ci/phase-gates.json
git commit -m "test: capture guided browser and desktop beta evidence"
```

---

### Task 4: Verify, import, sign, and certify the exact gate set

**Files:**
- Create: `scripts/import-evidence.py`
- Modify: `scripts/verify-evidence.sh`
- Modify: `scripts/sign-evidence.sh`
- Modify: `scripts/certify-beta.sh`
- Modify: `scripts/verify-accumulated-suite.sh`
- Modify: `tests/operations/beta_certification_test.sh`
- Modify: `tests/operations/accumulated_suite_test.sh`
- Modify: `ci/phase-gates.json`
- Modify: `documentation/operations/beta-certification.md`

**Interfaces:**
- Produces: immutable evidence bundle, canonical evidence index, detached SSH signature, certification JSON/signature.
- Consumes: frozen candidate lock and every required Phase 1–4/local/external envelope.

- [ ] **Step 1: Add complete-set adversarial tests**

From a valid fixture bundle, mutate one dimension at a time: missing/extra/duplicate gate, failed/not_run status, wrong candidate/source/subject, altered output log, unknown tool/runtime/artifact, expired evidence, dependency completed later than dependent, unsigned guided record, unauthorized signer/principal, invalid namespace, changed index after signature, dirty candidate graph, and certification created before last evidence. Every mutation must fail.

Assert certification stops after the first required gate failure and never writes a `passed` certification or signature in the output path.

- [ ] **Step 2: Run tests and observe placeholder verification/certification**

```bash
bash tests/operations/accumulated_suite_test.sh
bash tests/operations/beta_certification_test.sh
```

Expected: nonzero until exact-set and signature logic exists.

- [ ] **Step 3: Add freshness and subject requirements to the gate catalog**

Every gate entry has `maxAgeHours`, `subjectKind`, `requiredPlatforms` where applicable, and prerequisite IDs. The complete required set includes all master-plan local gates plus:

```text
clean-install browser-matrix desktop-linux desktop-windows desktop-macos
runtime-hardening monitoring encrypted-backup separate-host-restore
upgrade-rollback capacity host-exposure controlled-egress candidate-supply-chain
```

No cached `gatesPassed` or phase-level boolean exists.

- [ ] **Step 4: Implement immutable evidence import/indexing**

`import-evidence.py --candidate-lock PATH --catalog PATH --source-dir PATH --bundle-dir PATH` verifies each envelope/raw log/attachment/signature, copies regular files with safe deterministic names using exclusive creation, records raw digests, sets bundle files read-only, and writes `evidence-index.json` canonically. It refuses an existing nonempty bundle, symlinks, special files, path escape, secret-bearing keys, and records not tied to the candidate.

- [ ] **Step 5: Implement strict evidence signing/verification**

`sign-evidence.sh` requires `TELOS_EVIDENCE_SIGNING_KEY` path mode `0600`, approved principal, verified candidate/index, and empty signature output. It signs raw canonical index bytes with:

```bash
ssh-keygen -Y sign -f "$TELOS_EVIDENCE_SIGNING_KEY" \
  -n telos-beta-evidence-v1 evidence-index.json
```

`verify-evidence.sh` uses `ssh-keygen -Y verify` against `release/evidence-signers`, verifies index and every immutable file/digest/envelope/freshness/dependency, and never repairs the bundle. Capture the signer fingerprint/principal separately without private data.

- [ ] **Step 6: Implement final certification**

Expose:

```text
certify-beta.sh --candidate PATH --evidence-bundle PATH \
  --catalog ci/phase-gates.json --out-dir ABSOLUTE_PATH
```

Verify candidate graph/checksums/tag, exact gate set, evidence signature, freshness/order, source/artifact/runtime/platform identities, supply-chain results, and no required `not_run`. Write canonical `certification.json` with candidate digest, required gate-to-envelope digest map, evidence-index digest, signer principal/fingerprint, certification time, and status `passed`; then sign it with namespace `telos-beta-certification-v1`. If any check fails, write only a non-certifying diagnostic outside the candidate/evidence bundle and exit nonzero.

- [ ] **Step 7: Run certification fixtures**

```bash
bash tests/operations/accumulated_suite_test.sh
bash tests/operations/beta_certification_test.sh
bash tests/operations/evidence_envelope_test.sh
bash tests/operations/signer_trust_test.sh
```

Expected: all exit `0`; negative fixture cases fail for their intended reason.

- [ ] **Step 8: Commit evidence certification**

```bash
git add scripts/import-evidence.py scripts/verify-evidence.sh scripts/sign-evidence.sh \
  scripts/certify-beta.sh scripts/verify-accumulated-suite.sh \
  tests/operations/beta_certification_test.sh tests/operations/accumulated_suite_test.sh \
  ci/phase-gates.json documentation/operations/beta-certification.md
git commit -m "release: certify only complete candidate-bound evidence"
```

---

### Task 5: Stage a draft and publish without rebuilding

**Files:**
- Modify: `scripts/stage-release.sh`
- Modify: `.github/workflows/release.yml`
- Modify: `tests/operations/release_test.sh`
- Modify: `scripts/check-release-truth.sh`
- Modify: `documentation/operations/release.md`
- Modify: `README.md` only for accurate unsigned-beta links/instructions.

**Interfaces:**
- Produces: a draft GitHub prerelease with candidate, checksums, evidence bundle, certification/signatures, SBOM/scan summaries, and unsigned desktop instructions.
- Produces: publication by changing draft state only; asset bytes remain identical.

- [ ] **Step 1: Add staging/promotion tests with a fake GitHub CLI/API**

Assert stage rejects unsigned/nonexistent tag, wrong version/candidate, failed certification, missing asset, asset name collision, existing non-draft release, private key in directory, and checksum mismatch. Assert it invokes create/upload without overwrite. Assert publish workflow contains no build/npm/cargo/go/podman compilation command, has no `id-token: write`, verifies every downloaded asset/signature/digest, requires exact candidate input, and only then edits draft/prerelease state.

- [ ] **Step 2: Run release tests and observe placeholder stage/rebuild workflow**

```bash
bash tests/operations/release_test.sh
```

Expected: nonzero because stage prints success and the release workflow calls the builder.

- [ ] **Step 3: Implement non-overwriting draft staging**

Expose:

```text
stage-release.sh --tag TAG --candidate PATH --evidence-bundle PATH \
  --certification-dir PATH --notes PATH
```

Verify all inputs locally, require authenticated `gh` identity belongs to the authorized release group documented by operator policy, call `gh release create TAG --verify-tag --draft --prerelease`, and upload each uniquely named asset without `--clobber`. Download assets into a fresh temp directory immediately and reverify bytes/graph/signatures. If upload fails, leave the draft for diagnosis and report exact recoverable state; never replace an existing asset.

Release notes state reference server, browser/desktop matrix, platform-trusted HTTPS, unsigned package warnings, checksum/signature instructions, known limitations, backup/RPO/RTO contract, and upgrade path.

- [ ] **Step 4: Replace release workflow with verification-only promotion**

Workflow inputs: exact tag and candidate-lock digest. Checkout the tag with `contents: read`; promotion job receives `contents: write`. Download every draft asset, verify tag signer, candidate graph/checksums, evidence/certification signatures, exact digest input, required `passed` gate set, and asset list. Then run only:

```text
gh release edit "$TAG" --draft=false --prerelease=true
```

Fetch the published asset metadata/digests afterward and compare with the verified draft inventory. No build matrix, dependency install, package generation, signing, renaming, or asset upload occurs here.

- [ ] **Step 5: Run workflow/release truth tests**

```bash
bash tests/operations/release_test.sh
bash scripts/check-release-truth.sh
bash scripts/check-product-truth.sh
```

Expected: all exit `0`; tests prove no overwrite/rebuild path.

- [ ] **Step 6: Rehearse staging without publication**

Stage the rehearsal candidate/evidence/certification as a draft prerelease, redownload and verify, then cancel before promotion. Remove the rehearsal draft only after retaining test logs and confirming it is not the beta tag. This deletion is limited to the task-owned rehearsal release.

- [ ] **Step 7: Commit staging and promotion**

```bash
git add scripts/stage-release.sh .github/workflows/release.yml \
  tests/operations/release_test.sh scripts/check-release-truth.sh \
  documentation/operations/release.md README.md
git commit -m "release: promote certified bytes without rebuilding"
```

---

### Task 6: Freeze and certify the beta candidate

**Files:**
- Modify: `documentation/product/beta-feature-status.md` only after certification passes.
- Create externally, do not commit: candidate archive, raw evidence, signed evidence bundle, certification directory, release draft assets.

**Interfaces:**
- Consumes: every output of the master plan.
- Produces: one published invite-only beta prerelease and a reviewed evidence archive.

- [ ] **Step 1: Run the final clean-source suite before tagging**

```bash
git status --short
bash scripts/check-product-truth.sh
bash scripts/check-release-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only
bash scripts/verify-release-pins.sh
bash scripts/validate-compose.sh --env-file .env.example
bash scripts/test-backend.sh
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -race ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
cd frontend
npm ci
npm run lint
npx tsc --noEmit
npm run test:unit
npm run build
npm run check:bundle
npx playwright test
cd src-tauri
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test
cd ../..
```

Expected: empty initial status and every command exits `0`. If a gate needs a local running stack, start the documented isolated stack and tear it down afterward.

- [ ] **Step 2: Create and verify the signed beta tag**

Set the chosen `0.1.0-beta.N` consistently, commit the version-only change, create an authorized signed annotated tag `v0.1.0-beta.N`, and verify it with `release/telos-tag-signers`. Push commit/tag only after review. Record tag object and commit IDs.

- [ ] **Step 3: Build and freeze the candidate**

Run `.github/workflows/candidate.yml` for the signed tag. Download the candidate archive, verify detached checksum and internal graph, extract to a read-only external directory, and record `candidateLockDigest`. From this point, do not rerun the build workflow for this version.

- [ ] **Step 4: Run all local catalog gates against the frozen artifacts**

```bash
python3 scripts/run-gates.py --catalog ci/phase-gates.json --scope local \
  --candidate-lock /srv/telos-beta-candidate/candidate-lock.json \
  --evidence-dir /srv/telos-beta-evidence/local
```

Expected: every required local envelope is `passed`, current, and candidate-bound. Any failure invalidates this candidate; fix on a new version.

- [ ] **Step 5: Run all external gates without rebuilding**

Install only the frozen archive/package bytes and complete, in dependency-safe order:

1. clean Ubuntu installation and guided integrations;
2. automated and guided browser matrix;
3. Linux/Windows/macOS desktop guided journeys;
4. runtime hardening and monitoring;
5. encrypted backup and separate-host restore;
6. predecessor upgrade/rollback;
7. controlled egress and external exposure;
8. capacity;
9. final candidate SBOM/vulnerability/license scan.

Write each envelope/raw record into a new external evidence directory. Stop at first `failed`; preserve evidence and create a new candidate after remediation. If any environment is unavailable, record `not_run` and do not certify.

- [ ] **Step 6: Import, sign, and certify evidence**

```bash
python3 scripts/import-evidence.py \
  --candidate-lock /srv/telos-beta-candidate/candidate-lock.json \
  --catalog ci/phase-gates.json \
  --source-dir /srv/telos-beta-evidence/raw \
  --bundle-dir /srv/telos-beta-evidence/bundle

TELOS_EVIDENCE_SIGNING_KEY=/secure/telos-release/id_ed25519 \
TELOS_EVIDENCE_SIGNING_PRINCIPAL=release@telos.local \
bash scripts/sign-evidence.sh \
  --candidate-lock /srv/telos-beta-candidate/candidate-lock.json \
  --evidence-dir /srv/telos-beta-evidence/bundle

bash scripts/certify-beta.sh \
  --candidate /srv/telos-beta-candidate \
  --evidence-bundle /srv/telos-beta-evidence/bundle \
  --catalog ci/phase-gates.json \
  --out-dir /srv/telos-beta-evidence/certification
```

Expected: evidence/certification signatures verify, every required gate is `passed`, and certification names the frozen candidate digest.

- [ ] **Step 7: Conduct a two-person release review**

One reviewer who did not operate the primary candidate run independently verifies tag, archive/checksums, graph, signer fingerprints, exact gate set, evidence freshness/identity, external raw log digests, RPO/RTO, capacity thresholds, ports/egress, SBOM/scan policy, unsigned-client instructions, and absence of secrets. Record reviewer identity/time/signature as a certification attachment; it cannot change certification content.

- [ ] **Step 8: Stage and verify the draft prerelease**

Run `stage-release.sh` with candidate, signed evidence, certification, and reviewed notes. Redownload all draft assets on a separate machine and run release/evidence/certification verification. Expected: byte-for-byte digests match the frozen local copies.

- [ ] **Step 9: Publish by promotion only**

Dispatch `.github/workflows/release.yml` with the exact tag and candidate-lock digest. Expected: workflow verifies and changes draft state; published assets retain the draft digests. If GitHub asset metadata differs, halt and leave the release draft/unpublished.

- [ ] **Step 10: Update product status after publication evidence**

Only after the published asset re-verification succeeds, update `documentation/product/beta-feature-status.md` with the certified beta version/date and evidence reference in a follow-up commit. Do not change the already-published source tag. Announce the invite-only release with checksum/signature verification and unsigned OS warnings.

- [ ] **Step 11: Archive recovery material**

Store the signed evidence/certification bundle, candidate archive, public signer fingerprints, release metadata, off-node snapshot ID, and restore/upgrade journals in the operator's encrypted off-node archive. Verify archive retrieval once. Private signing keys remain in their existing secure custody and are not copied into the archive bundle.
