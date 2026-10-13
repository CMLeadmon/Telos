# Standalone Beta Phase 5: Candidate Certification Implementation Plan

> **Active plan, remediated 2026-10-13:** [Platform policy](../../../documentation/product/platform-scope.md) is binding: Ubuntu 24.04+ server only; browsers and Linux desktop first; Windows/Mac native clients deferred. Read the cold handoff and shared contracts before execution.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build, test, sign, and publish exactly one immutable Telos beta candidate whose server, browser client, desktop packages, recovery artifacts, and evidence all share one verifiable identity.

**Architecture:** Export a signed source tag, build deterministic server/client OCI artifacts, build desktop packages on native runners, assemble them once into a non-circular hash graph, and freeze a candidate lock before testing. Every local and external gate consumes those artifacts without rebuilding and emits a strict Phase 1 envelope bound to the lock. Guided browser/desktop checks produce signed, candidate-bound records. Certification verifies the exact required gate set and signs an evidence index. Publication only promotes a draft release containing the already-certified bytes.

**Tech Stack:** Git signed tags, Podman/Buildah, Tauri native GitHub Actions runners, Python 3 standard library, Bash, OpenSSH `ssh-keygen -Y sign/verify`, GitHub Actions/Releases, SHA-256, Phase 1 evidence/gate runner, Phase 4 SBOM/scanning.

**Spec:** `docs/superpowers/specs/2026-08-20-standalone-beta-readiness-design.md`

## Global Constraints

- Execute Task 1a schemas and Task 4a runner/result contracts immediately after Phase 1, and Task 2a builder implementation as its product inputs become available. These support Phase 2–4 fixtures and rehearsals. Only Task 6 final freeze/certification waits for all Phase 1–4 implementations and Phase 5 tooling to be merged and reviewed. Freeze from a clean signed tag on the release branch; never certify a dirty checkout, merge preview, unpushed commit, or rebuilt substitute.
- Version format is `0.1.0-beta.N` with positive integer `N`. The same version appears in the Git tag, release manifest, frontend package, Cargo package, Tauri config, desktop metadata, and artifact filenames.
- A release build exports the signed tag with `git archive`; it does not build from the caller's working tree. `SOURCE_DATE_EPOCH` is the signed commit time.
- The Linux AppImage is built on an Ubuntu native runner from the same tag. The Ubuntu assembler imports it only after verifying source commit, version, workflow run/attempt, platform, and raw SHA-256 sidecars.
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
- For final certification, every required gate is exactly one fresh `passed` envelope. `failed`, `not_run`, duplicate, missing, stale, unknown, wrong-candidate, wrong-source, wrong-runtime, wrong-artifact, or invalidly signed evidence blocks certification.
- Default maximum evidence age is 168 hours. Runtime hardening, host exposure, separate-host restore and upgrade/rollback expire at 72 hours; candidate supply-chain evidence expires at 24 hours. The catalog owns these exact `maxAgeHours`; compare freshness at certification and again at promotion.
- Guided manual records are explicit per assertion and operator-signed. A checkbox left unanswered is `not_run`; a narrative claim cannot replace required fields.
- Required browser matrix: current Chrome, Firefox, Edge, automated WebKit, desktop Safari, iOS Safari, and Android Chrome, with exact observed versions recorded. WebKit automation does not substitute for Safari device checks.
- Required desktop matrix: the exact unsigned Linux x86-64 AppImage on Ubuntu. Windows/macOS native packages and gates are deferred and must not be indirect prerequisites. Build success does not substitute for install/restart/keychain/revocation/media journeys.
- Private signing keys never enter the repository, CI logs, evidence archive, release archive, or command arguments as key material. Key file paths may be provided through environment and must be mode `0600`.
- Public trusted signer files contain real OpenSSH public keys and no ellipses/synthetic values. If an authorized operator key has not been enrolled and reviewed, signing/certification is `not_run`.
- Release publication has `contents: write` only in the promotion job. Candidate builds have `contents: read`; no job has `id-token: write` unless an implemented attestation verifier consumes it.
- Release assets are uploaded once without overwrite. Publication promotes the already-verified draft; it does not compile, package, rename, replace, or re-sign payload bytes.

---


## Cold-handoff subparts and ownership (2026-10-13)

Read [shared contracts](2026-10-13-standalone-beta-handoff-contracts.md) and the [execution entry](2026-10-13-standalone-beta-cold-handoff.md) first. Existing Task numbers remain stable:

| Subpart | When | Deliverable |
|---|---|---|
| Task 1a | After Phase 1 | Graph/result/guided/index/certification/generation schemas and fixture validators; no production key prerequisite |
| Task 1b | When operator supplies approved keys | Reviewed real trust enrollment |
| Task 4a | After Task 1a, before downstream result producers | Catalog v2 and envelope v2 integration, runner/result/subject adapters |
| Task 2a | Schemas ready; integrate product paths as they land | Builder and non-certifying fixture/rehearsal support |
| Tasks 3, 4b, 5 | May implement with fixtures while Phases 2–4 proceed | Collectors, import/sign/certify and stage/promote tools |
| Task 6 | All product/tooling merged; external inputs supplied | One final freeze, candidate gates, signed certification and publication |

Tooling implementation is not beta certification. Missing signer, second host, browser device, off-node repository or predecessor blocks that affected external action and final certification only. Run fixture suites and unrelated implementation work meanwhile. Do not fake a live result to complete an implementation checkpoint.

**Review focus:** circular graph fields; runner/producers emitting duplicate evidence; local/external prerequisite duplication; secret-bearing attachments; production key enrollment blocking ordinary fixtures. Task 1 schema mutation tests, Task 4a adapter tests, Task 4b import tests and Task 3 secret-canary tests own these cases.

## Current-State Baseline

Phase 1 now supplies a strict envelope v2 (`scripts/lib/evidence.py:17-26,79-155`) and catalog v1 runner (`scripts/run-gates.py:27-33`). Preserve its fail-closed checks. Artifact/release schemas remain minimal and the complete graph/builder/certifier is this phase's work. Earlier step labels describing success-only placeholders are historical failure modes, not an instruction to restore a stub; check the present baseline and write a regression for the missing behavior. Task 4a explicitly migrates the implemented runner instead of assuming it already accepts artifact/runtime evidence.

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
- Create: `.github/workflows/candidate.yml` — Ubuntu candidate build/assembly.
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

Create a complete tiny fixture graph and assert rejection of unknown fields, missing artifact, wrong path/digest/size/media type/platform, duplicate path, path traversal, absolute path, symlink artifact, mismatched source/version, candidate hashing itself, evidence included in candidate payload, wrong candidate digest, missing required evidence, duplicate gate, invalid status, reversed times, expired record, guided assertion omission, invalid public key, repeated/fabricated key material, unauthorized principal, wrong SSH namespace, and signature over changed bytes.

- [ ] **Step 2: Run tests and expose current permissive schemas/invalid key material**

```bash
bash tests/operations/release_test.sh
bash tests/operations/signer_trust_test.sh
```

Expected: new graph/trust mutation tests fail for missing strict graph behavior; existing envelope v2 regressions continue passing. Never weaken existing envelope checks.

- [ ] **Step 3: Define strict schemas**

Use JSON Schema 2020-12 with `additionalProperties: false` throughout. Implement every required field and invariant in the [shared contracts](2026-10-13-standalone-beta-handoff-contracts.md), including generation descriptor/installed receipt, result/subject, guided record, evidence index, and certification; add round-trip fixtures for each. Core graph shapes:

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

Candidate lock repeats the complete artifact digest map so it can be verified without trusting index traversal. It excludes checksums, evidence, signatures, and itself. `generation.json` contains no candidate digest or installed receipt. Artifact-bound SBOMs contain only payload artifact identity, never candidate identity. The examples above fix the graph field sets; the following table fixes remaining records. All objects are closed and all listed fields required unless explicitly nullable.

### Closed record inventory (Task 1a)

Implement schemas and semantic validators together. `D` below means prefixed SHA-256; `H` means raw 64-character lowercase hex; `T` means UTC timestamp; `P` means safe relative regular-file path; `C` means 40-character source commit. Collections are sorted by their identity key and reject duplicate identities. No array of hashes may include its own file or a downstream record.

| File/schema | Required fields and rules |
|---|---|
| `artifact-index.json` | `schemaVersion:1`, `artifacts:[{path:P,kind,platform,size:nonnegative integer,sha256:H,contentDigest:D|null}]`. Kind enum `oci-image,desktop,static-client,source,sbom,config,generation,migrations`; platform `linux/amd64,linux-x86_64,any`. OCI contentDigest is the verified manifest digest; other kinds use null. Distinct files may have identical bytes; reject duplicate paths, not legitimate equal digests. |
| `release-manifest.json` | Exact example above; `buildInputs` keys exactly `npmLock,cargoLock,goSum`, all D. Version/tag relation is `sourceTag = "v" + version`; epoch equals signed source commit time. |
| `candidate-lock.json` | Exact example above; artifacts map is the complete index path→D mapping. Verify both manifest and index bytes, source/version agreement and every payload digest. |
| `generation.json` / `generation-manifest.schema.json` | `schemaVersion:1,version,sourceCommit:C,artifactIndexSubset:[{path:P,sha256:H}],images:{service:D},migrations:{min:integer,current:integer,max:integer,checksumSetDigest:D},predecessorIds:[D],rollbackCompatibleThrough:integer`. Subset contains operational payload files only; excludes generation itself, outer index/manifest/lock, checksums, SBOMs, evidence and signatures. `min<=current<=max`; rollback bound is explicit and validated against predecessor. |
| installed receipt / `installed-generation.schema.json` | `schemaVersion:1,candidateLockDigest:D,generationManifestDigest:D,generationPath:absolute path,installedAt:T`. Stored in node `state/installed-generation.json`; not release payload. |
| `gate-result.schema.json` | Exact shared contract Section 5. Attachment kind enum `subject,report,guided-record,signature,artifact-identity`. Signed-record signatures are separate declared attachments. Subject file is declared with kind subject. |
| `observed-subject.schema.json` | Runtime/external-host shapes in shared Section 6; artifact variant `{schemaVersion:1,kind:"artifact",sourceCommit:C,candidateLockDigest:D,selector:string,artifactDigest:D}`. Verify selector/digest against frozen index; result subjectFile holds this descriptor, while artifact envelope subject digest is actual selected artifact/index bytes. |
| `artifact-identity.schema.json` | `schemaVersion:1,sourceCommit:C,version,platform:"linux-x86_64",size:integer,sha256:H,workflowRunId:positive integer,workflowRunAttempt:positive integer`. Produced with the Linux package; no downstream candidate digest. |
| `guided-smoke.schema.json` | `schemaVersion:1,sourceCommit:C,candidateLockDigest:D,matrix:"browser"|"desktop",platform,artifactDigest:D,hostId,os,osVersion,applicationVersion,deviceModel,testerPrincipal,startedAt:T,finishedAt:T,assertions:[{id,answer:"pass"|"fail"|"not_run",notes:string}],attachments:[{path:P,sha256:H}]`. All identity/version strings nonempty; notes ≤1000 chars. No overall status field. Exact assertion IDs come from Task 3's checklist committed as `release/guided-assertions.json`; recorder and verifier consume that one inventory. |
| `evidence-index.schema.json` | `schemaVersion:1,sourceCommit:C,candidateLockDigest:D,createdAt:T,gates:[{gateId,envelope:{path:P,sha256:H},log:{path:P,sha256:H},result:{path:P,sha256:H}|null,files:[{path:P,sha256:H,kind}]}]`. Source/fixture result null, others required. Files contain result attachments and signed records; exclude index/signatures/certification. Each path owned by one gate. |
| `certification.schema.json` | `schemaVersion:1,sourceCommit:C,candidateLockDigest:D,evidenceIndexDigest:D,certifiedAt:T,status:"passed",signerPrincipal,signerFingerprint,requiredGates:{gateId:D}`. Map hashes envelope bytes; excludes beta-certification. Separate detached signature; no self/signature digest. |
| `release-review.schema.json` | `schemaVersion:1,candidateLockDigest:D,evidenceIndexDigest:D,certificationDigest:D,reviewerPrincipal,reviewedAt:T,decision:"approved"`. Detached signature namespace `telos-beta-review-v1`; reviewer differs from certifier. |

Create all schema files above under `release/`; add them to the Task 1 file list and commit. Tests must reject missing/extra fields, invalid enums and graph relationships as well as malformed JSON. Implement the bounded schema subset in Python standard library plus explicit semantic checks; merely declaring JSON Schema without executing validation is insufficient. Preserve existing evidence-envelope v2 as its own schema.

- [ ] **Step 4: Implement canonical graph validation**

`scripts/lib/release_graph.py` performs schema/type/pattern/closed-key validation, canonical JSON serialization, raw file hashing, safe relative path resolution, regular-file/no-symlink checks, artifact completeness, cross-document identity/digest equality, and acyclicity assertions. Expose:

```text
release_graph.py build-index --root PATH --manifest-list PATH --out PATH
release_graph.py build-manifest --root PATH --metadata PATH --out PATH
release_graph.py build-candidate-lock --root PATH --out PATH
release_graph.py verify --root PATH
```

Every writer uses sibling temp, `fsync`, mode `0600`, and atomic rename. Canonical bytes and digest semantics come from the shared contract; assert the emitted candidate file is canonical because `scripts/lib/evidence.py:229-239` hashes its raw bytes. `release_graph.py verify` validates schemas plus cross-document relationships, not just JSON syntax.

- [ ] **Step 5: Implement public-key enrollment and remove invalid trust data**

`enroll-release-signer.py --purpose tag|evidence --principal PRINCIPAL --public-key-file PATH` accepts only one valid `ssh-ed25519` or hardware-backed SSH signing public key, prints its SHA-256 fingerprint, rejects private keys/comments with control characters/duplicate key/fabricated ellipses, and atomically updates the matching allowed-signers file after explicit fingerprint confirmation. `--verify-files` parses both committed allowed-signers files, validates every key with `ssh-keygen`, rejects duplicate principals/keys, and prints fingerprints.

Remove `telos-release.pub`. Before candidate work, an authorized operator runs enrollment with actual public key files and reviews the commit. Keep Task 1a schema/tool fixture work independent of real keys. Tests generate ephemeral keys only under mode-`0700` test temp directories and remove them on exit; never commit them. Task 1b production enrollment and later signed rehearsal/final release actions return `not_run` if no reviewed operator key exists; unrelated implementation continues.

- [ ] **Step 6: Run schema/trust tests**

```bash
bash tests/operations/release_test.sh
bash tests/operations/signer_trust_test.sh
python3 -m py_compile scripts/lib/release_graph.py scripts/enroll-release-signer.py
python3 scripts/enroll-release-signer.py --verify-files
```

Expected: fixture tests exit `0` using temporary trust anchors. Production `--verify-files` passes only after Task 1b real-key enrollment; until then record `not_run` for enrollment without blocking Task 1a.

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
# Commit production trust-file changes only when separately enrolled/reviewed.
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
- Produces payload: source archive, all required server OCI archives, one Linux AppImage, candidate-independent deploy generation, artifact-bound SBOMs, legal/runbooks, artifact index, manifest, candidate lock, and `SHA256SUMS`.
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

The payload includes a candidate-independent `generation.json` and mode-`0600` non-secret `images.env` containing digest-addressed images. The installed receipt is written outside payload only after lock verification and is never indexed back into the candidate. Candidate install/upgrade always layers the override and refuses `--build`. Test merged Compose, not the overlay text: generate a complete no-build manifest from the validated base, remove every service `build` key and replace every image with its locked digest; use the shared pinned provider to validate the result. Every core/client/migrate/validate-integrations/sanitize-restore service must use the correct candidate image; no inherited build key may survive.

- [ ] **Step 4: Implement exact-source assembly**

Expose:

```text
scripts/build-release.sh --tag TAG --version VERSION \
  --desktop-dir PATH --out-dir ABSOLUTE_PATH
```

Verify signed annotated tag with `release/telos-tag-signers`, require tag/version/source consistency, export with `git archive`, set deterministic locale/timezone/umask and `SOURCE_DATE_EPOCH`, and build core/client with locked bases plus `podman build --timestamp`. Export OCI archives without pulling after lock verification. Import/verify native artifacts. Copy only the tracked deployment/runtime/legal/runbook inventory. Write the content-only payload index, generate artifact-bound SBOMs, then build artifact index → release manifest → candidate lock. Write deterministic `SHA256SUMS`, verify the graph, and package with normalized owner/group/mtime/order. Do not run candidate-bound scan before the lock exists; run it after freeze and store scan evidence outside the candidate.

Refuse `.env`, `.telos`, secrets, caches, node_modules, build targets, evidence, local volumes, Git metadata, or absolute paths.

- [ ] **Step 5: Make reproducibility semantic and complete**

Before freezing, build twice from the tag in distinct temporary roots, second with no cache; only one verified output becomes the candidate. This is builder reproducibility validation, not permission to substitute a rebuild after freeze. Require identical Go binary, frontend static tree, OCI index/manifest/config/layer digests, deployment tree, canonical SBOM bytes (set generated timestamps from SOURCE_DATE_EPOCH before indexing), graph documents, candidate lock, and packaged file list. If outer archive compression bytes differ, diagnose and normalize; do not weaken comparison to filenames.

- [ ] **Step 6: Create the native candidate workflow**

Workflow inputs: signed tag and version. Jobs:

1. validate signed tag/locks/schemas on Ubuntu;
2. build the Linux x86-64 AppImage on Ubuntu;
3. the native build writes the exact artifact-identity schema from Task 1a (including workflowRunId/workflowRunAttempt) and uploads immutable same-run artifact;
4. Ubuntu assembler downloads the Linux package, verifies it, builds core/client, archives the exact third-party platform images, generates artifact-bound SBOMs, verifies pre-freeze reproducibility, creates candidate graph/archive/checksum, and uploads the frozen candidate. Candidate-bound vulnerability/secret/license scan evidence is generated only after this lock exists.

Use `permissions: contents: read`, pinned actions by commit SHA, no signing secrets, and retention of at least 14 days (retention does not extend evidence freshness). Artifact names include version and candidate-lock digest after assembly.

- [ ] **Step 7: Run local fixture/reproducibility checks**

```bash
bash tests/operations/release_test.sh
bash scripts/tests/reproducible-build-test.sh
bash scripts/verify-release-pins.sh
bash scripts/verify-clean-checkout.sh --inventory-only
```

Expected: all exit `0`.

- [ ] **Step 8: Build one signed rehearsal tag when operator trust is available**

Use an authorized signed rehearsal tag `v0.1.0-beta.N` on a clean commit with its matching version, reserve that N permanently, and mark the output non-certifying in the external run record and pass its fully verified lock through --rehearsal-lock (recordKind remains rehearsal) (do not invent an unsupported tag syntax), run the full native workflow, download the assembled archive twice, verify its checksum/graph, load both OCI images, inspect version/source labels, and install each desktop artifact. Delete only the rehearsal workflow artifacts/draft after review; do not reuse them as beta evidence.

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
- Produces guided external subrecords: desktop Safari, iOS Safari, Android Chrome, Linux Tauri.
- Produces aggregate evidence gates: `browser-matrix`, `desktop-linux`.

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

It verifies candidate/artifact first, collects tool-detected OS/app/browser versions where possible, displays one numbered assertion at a time, accepts only `pass`, `fail`, or `not_run` plus bounded notes, hashes declared attachments, redacts credential-like input, and atomically writes a closed record. It cannot write overall `passed`; the aggregator derives it only when all assertions pass. Finish by signing canonical record bytes with namespace `telos-guided-smoke-v1` using `TELOS_EVIDENCE_SIGNING_KEY`; verify against the approved tester principal in `release/evidence-signers`. Without an enrolled tester/key, save an unsigned draft outside importable evidence and return `3`. Fixture tests use ephemeral keys. Guided browser `--artifact` selects the hosted-client artifact, while desktop selects the Linux AppImage; both must exist in the candidate index.

Browser assertions cover trusted HTTPS, owner/invite/member auth, Chat, Stream video/audio, Library EPUB/PDF/audiobook, Files upload/download/delete, commentary, Settings/Admin, logout/login, upstream degradation, offline/reconnect, and responsive/accessibility-critical interactions. Desktop assertions use the Phase 3 install/connect/keychain/restart/refresh/revocation list plus all four modules.

- [ ] **Step 5: Configure real automated browser projects**

Run Playwright against the frozen candidate URL, never mock APIs. Projects are Chromium/Chrome, Firefox, WebKit, and Edge channel on a compatible runner. Record exact executable versions, candidate health identity, test result JSON, tracing/video/HAR disabled for credential-bearing journeys, and allowlisted console/network error summaries with full URL query/header/cookie/body redaction. Failure is not permission to publish a secret-bearing trace. Use unique invited accounts and clean them up.

- [ ] **Step 6: Implement browser aggregation**

Expose:

```text
certify-browser-matrix.sh --candidate-lock PATH --records-dir PATH \
  --result-out PATH
```

Validate automated/guided schemas, candidate identity, exact required set, journey completeness, version capture, timestamps/freshness, attachment hashes, and no duplicate substitutions. Write a producer result whose subjectFile is the shared external-host descriptor and whose attachments include the canonical matrix index plus signed records. The runner hashes that descriptor into envelope.subject.digest; do not hash the matrix index directly as a competing subject format. Any unavailable device/browser returns `3`/`not_run`.

- [ ] **Step 7: Run fixture and automated checks**

```bash
bash tests/operations/guided_smoke_test.sh
bash tests/operations/browser_certification_test.sh
cd frontend
npx playwright test --project=chromium --project=firefox --project=webkit
```

Expected: fixture tests exit `0`; local Playwright passes against the declared candidate-compatible test stack but is not external candidate evidence unless it uses the frozen candidate URL.

- [ ] **Step 8: Complete a rehearsal matrix**

Run automated records against the rehearsal candidate and guided records on physical/hosted Safari, iOS, Android, and Ubuntu Linux desktop. Aggregate, review redaction, restart/revocation behavior, and platform warning instructions. Any inaccessible platform remains `not_run`; this validates the workflow but cannot certify beta.

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


#### Task 4a: Extend the existing runner before downstream collectors are implemented

**Files:** modify `scripts/run-gates.py`, `scripts/lib/evidence.py`, `release/evidence-envelope.schema.json`, `ci/phase-gates.json`, `tests/operations/run_gates_test.py`, `tests/operations/evidence_envelope_test.sh`; create `release/gate-result.schema.json`, `release/observed-subject.schema.json` and focused Python fixtures if the shell harness needs them. Task 1a owns schema shapes; this subpart owns their execution.

- [ ] **4a.1 Pin current incompatibilities with failing tests.** Catalog v1 currently rejects extra fields (`scripts/run-gates.py:28-33,128-130`), forces artifact gates to fail (`:767-775`), and hashes runtime argv rather than runtime identity (`:580-589`). Add tests `catalog_v2_accepts_closed_fields`, `artifact_gate_verifies_indexed_bytes`, `runtime_gate_rejects_wrong_config_digest`, `result_gate_id_mismatch_fails`, `missing_result_is_not_success`, and `cross_scope_prerequisite_is_reused_once`.
- [ ] **4a.2 Implement catalog schema v2.** Retain v1 gate fields and `subjectKind`; add required `subjectSelector`, positive integer `maxAgeHours`, and unique `requiredPlatforms` array. Selectors are `sourceCommit`, `completed-output`, `artifact:<index-relative-path>`, `artifact-index`, `observed-runtime`, `observed-host` with the kind combinations in the shared contract. Reject unknown fields/selectors/platforms, duplicate/cyclic dependencies and deferred native gates. Keep explicit legacy v1 parsing for Phase 1 fixture tests; v1 artifacts still cannot certify and final certification requires v2.
- [ ] **4a.3 Implement producer input/result plumbing.** Add `--inputs PATH` and repeatable `--prior-evidence-dir PATH`. Inputs are a closed JSON object `{ "gateInputs": { "gate-id": { "targetFile": "/secure/target.json" } } }`; allowed keys per gate are fixed below, never arbitrary argv. Require absolute regular-file/directory paths, reject symlinks, unknown gates/keys and command/credential overrides. Reserve candidate-lock/result-out flags to the runner. Producers implement exactly the shared result/mode contract, writing `--result-out` while the runner alone emits envelopes and sanitized logs. Source/fixture commands need no sidecar.

| Gate IDs | Allowed input keys → fixed option |
|---|---|
| `clean-install` | `targetFile`→`--target-file`, `fixtureManifest`→`--fixture-manifest` |
| `browser-matrix`, `desktop-linux` | `recordsDir`→`--records-dir` |
| `runtime-hardening`, `monitoring`, `encrypted-backup`, `separate-host-restore`, `upgrade-rollback`, `capacity`, `controlled-egress` | `targetFile`→`--target-file` |
| `host-exposure` | `targetFile`→`--target-file`, `policyFile`→`--policy` |
| `candidate-supply-chain` | `artifactRoot`→`--artifact-root`, `sbomDir`→`--sbom-dir` |

Each listed input is required for that gate; source/fixture gates accept no injected input. Create `release/gate-inputs.schema.json` and `release/drill-target.schema.json`. Drill target is closed: `schemaVersion:1,origin:HTTPS origin,nodePath:absolute path,hostId:D,sshTarget:string|null,sshConfigFile:absolute path|null,credentialsFile:absolute path,restoreTargetFile:absolute path|null,predecessorGeneration:absolute path|null,capacityTargetFile:absolute path|null,confirmation:string|null`. Remote SSH targets require protected SSH config and known-host verification; restore requires a distinct target descriptor; upgrade requires predecessor; capacity requires its protected marker. Credentials file contains operator-local inputs only, is mode0600 and is never copied into evidence. Reject embedded passwords in origin/SSH strings. Destructive confirmation must match the task's exact token and declared host. A target descriptor is operational authorization input, not observed evidence. Verify machine identity before action. Add adapter support to each existing Phase 2/4 drill wrapper, translating this descriptor to its task-specific flags; do not broaden backup/restore primitives to execute arbitrary commands. Missing required inputs produce not_run; malformed or unsafe supplied inputs fail.

Migrate catalog commands from placeholders to real collectors in their owning phase. `desktop-linux` gets `scripts/certify-desktop.sh` (same closed guided-record validation as browser aggregation, one Linux platform); `encrypted-backup` calls the verified backup collector; `controlled-egress` calls its live drill; `candidate-supply-chain` calls `scripts/scan-candidate.sh`, which includes SBOM validation. The runner supplies verified candidate-lock and fresh result-out paths to each. Add these adapters and a `gate_input_adapter_test.py` mutation suite to Task 4a's file inventory. Unknown/reserved option injection, wrong host and secret-canary tests must fail before runtime mutation.

**Local artifact and policy mapping (same Task 4a migration):**

| Gate | Kind / selector | Candidate command and assertions |
|---|---|---|
| `static-client-image` | artifact / `artifact:images/telos-client.oci.tar` | `python3 scripts/verify-frozen-artifact.py --gate-id static-client-image`; verify indexed OCI digest, static files, non-root image config, no secrets/source/dependencies and no published application port. Load/run the exact archive for HTTP smoke; never build it. |
| `desktop-builds` | artifact / `artifact-index` | `python3 scripts/verify-frozen-artifact.py --gate-id desktop-builds`; require exactly one initial Linux AppImage artifact and matching artifact-identity sidecar; use Phase 3 inspection without executing input. Windows/Mac absence is valid. |
| `image-lock` | source / `sourceCommit` | `python3 scripts/verify-image-lock.py --source`; inspect committed locks/Compose/external Dockerfile bases and local alias resolution. Effective images are checked by runtime-hardening separately. |
| `supply-chain-policy` | source / `sourceCommit` | `bash scripts/check-supply-chain-policy.sh`; validate tool/license/risk-acceptance schemas, expiry and exact scanner policy fixtures; no live scan claim. Actual frozen scans belong to candidate-supply-chain. |

Create the two named adapters and `tests/operations/frozen_artifact_test.py`. Frozen-artifact producer receives runner-owned `--candidate-lock` and `--result-out`, derives artifact root from the verified lock location, and emits the artifact descriptor from Task 1a. Require mutation cases for absent artifact, wrong digest/platform, substituted sidecar and attempted rebuild. Source adapters exit nonzero on failed assertions and require no sidecar. Add every new command to the catalog, CI inventory and structural checker in the same commit. Keep Phase 2/3 direct build tests as source development checks; they do not replace frozen-artifact verification.

- [ ] **4a.4 Validate actual subjects.** Source hashes the verified source commit as today; fixture hashes its completed sanitized output; artifact hashes exact indexed bytes (or canonical full artifact index); runtime/host hashes a validated observed descriptor. Match host/runtime/service image identities to candidate graph and installed receipt. A missing/invalid result, nonzero producer exit, mismatch between result status and exit, wrong image/candidate/source, unsafe attachment path or absent declared file fails; missing external input yields `not_run`. Never convert command exit zero to a passed artifact/runtime gate without identity verification.
- [ ] **4a.5 Preserve envelope v2 and extend known tools.** Update the helper and schema together because `scripts/lib/evidence.py:175-208` compares exact property definitions. Extend `toolVersions` to the agreed allowlist (adding exactly restic, k6, syft, trivy, gitleaks, skopeo, promtool, ssh-keygen, cargo, rustc, go, buildah and gh to existing keys); reject arbitrary keys. Attachments live in result/index, not envelope. Secret-key name checking alone is insufficient: test canary values in argv/logs/notes/attachment bytes before import.
- [ ] **4a.6 Make prerequisite reuse deterministic.** Verify prior envelopes/results/subjects/logs against current candidate, freshness, exact gate ID and finish-before-dependent-start. Reuse each prerequisite once, without copying duplicate gate records into new output. Never run an external prerequisite implicitly from `--scope local`; report unavailable cross-scope input as `not_run`. Stop execution on first required failure; record remaining gates `not_run` with blocked reason. For certification, union verified prior/local/external roots once and reject duplicate IDs.
- [ ] **4a.7 Verify the adapter independently.** Run `python3 tests/operations/run_gates_test.py` and `bash tests/operations/evidence_envelope_test.sh`; include one tiny indexed artifact whose exit-zero producer succeeds and whose one-byte mutation fails, and a runtime fixture whose container config ID differs legitimately from its manifest digest. Confirm no external environment/key is needed. Commit runner/schema/catalog changes as this subpart before collectors consume them.

#### Task 4b: Import, sign and certify completed runner outputs

The following numbered steps belong to Task 4b and consume Task 4a's closed contracts.

- [ ] **Step 1: Add complete-set adversarial tests**

From a valid fixture bundle, mutate one dimension at a time: missing/extra/duplicate gate, failed/not_run status, wrong candidate/source/subject, altered output log, unknown tool/runtime/artifact, expired evidence, dependency completed later than dependent, unsigned guided record, unauthorized signer/principal, invalid namespace, changed index after signature, dirty candidate graph, and certification created before last evidence. Every mutation must fail.

Assert certification stops after the first required gate failure and never writes a `passed` certification or signature in the output path.

- [ ] **Step 2: Run tests and observe placeholder verification/certification**

```bash
bash tests/operations/accumulated_suite_test.sh
bash tests/operations/beta_certification_test.sh
```

Expected: nonzero until exact-set and signature logic exists.

- [ ] **Step 3: Use Task 4a catalog v2 freshness and subject requirements**

Every gate entry has `maxAgeHours`, `subjectKind`, `subjectSelector`, `requiredPlatforms` (empty array when inapplicable), and prerequisite IDs under catalog schema v2. Set default `maxAgeHours=168`; use `72` for runtime-hardening, host-exposure, separate-host-restore and upgrade-rollback, and `24` for candidate-supply-chain. Keep these values in one shared contract table and assert catalog equality. No Windows/macOS native IDs may occur, including in prerequisite closure. The complete required set includes all master-plan local gates plus:

```text
clean-install browser-matrix desktop-linux
runtime-hardening monitoring encrypted-backup separate-host-restore
upgrade-rollback capacity host-exposure controlled-egress candidate-supply-chain
```

Update `documentation/product/beta-contract.json`, the checker's exact expected contract, its mutation fixtures, and `ci/phase-gates.json` in the same implementation commit so the required external set is exactly the list above.

No cached `gatesPassed` or phase-level boolean exists. `beta-certification` is the terminal aggregator, never a required imported evidence input: excluding it from the evidence input set prevents certification depending on itself. The catalog retains only its declarative terminal ID/prerequisites. Every runner scope excludes `beta-certification`; explicit selection is rejected with guidance to the standalone certifier command in Step 6. That standalone command produces certification JSON/signature, not a gate result/envelope. Add runner and certifier tests for this exception and absence of self-reference.

- [ ] **Step 4: Implement immutable evidence import/indexing**

`import-evidence.py --candidate-lock PATH --catalog PATH --source-dir PATH --bundle-dir PATH` verifies each envelope/raw log/attachment/signature, copies regular files with safe deterministic names using exclusive creation, records raw digests, sets bundle files read-only, and writes `evidence-index.json` canonically. Import runner envelopes/logs together with producer result, observed subject and all declared attachments; envelope v2 remains closed, so attachment references belong to result/index, never extra envelope keys. Resolve local/external directory input through one raw evidence root; reject duplicate gate IDs across subdirectories. It refuses an existing nonempty bundle, symlinks, special files, path escape, secret-bearing keys, and records not tied to the candidate.

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

Verify candidate graph/checksums/tag, exact gate set, evidence signature, freshness/order, source/artifact/runtime/platform identities, supply-chain results, and no required `not_run`. Write canonical `certification.json` with candidate digest, required gate-to-envelope digest map, evidence-index digest, signer principal/fingerprint, certification time, and status `passed`; require `TELOS_EVIDENCE_SIGNING_KEY` and `TELOS_EVIDENCE_SIGNING_PRINCIPAL` for this invocation too, then sign it with namespace `telos-beta-certification-v1`. If any check fails, write only a non-certifying diagnostic outside the candidate/evidence bundle and exit nonzero.

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
- Create: `release/publisher-policy.json` — reviewed exact repository/login allowlist; tests use fixture values.
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

Expected: new staging/no-rebuild assertions fail until implemented; existing fail-closed stubs may correctly return `not_run` and must not be changed to success merely for this test.

- [ ] **Step 3: Implement non-overwriting draft staging**

Expose:

```text
stage-release.sh --tag TAG --candidate PATH --evidence-bundle PATH \
  --certification-dir PATH --review PATH --notes PATH
```

Verify all inputs locally including the signed review sidecar, require authenticated `gh api user --jq .login` matches an entry in reviewed `release/publisher-policy.json` (`schemaVersion:1`, `githubRepository:owner/name`, `allowedLogins:[nonempty unique logins]`), verify target repository equality, call `gh release create TAG --verify-tag --draft --prerelease`, and upload each uniquely named asset without `--clobber`. Download assets into a fresh temp directory immediately and reverify bytes/graph/signatures. If upload fails, leave the draft for diagnosis and report exact recoverable state; never replace an existing asset.

Release notes state reference server, browser/desktop matrix, platform-trusted HTTPS, unsigned package warnings, checksum/signature instructions, known limitations, backup/RPO/RTO contract, and upgrade path.

- [ ] **Step 4: Replace release workflow with verification-only promotion**

Workflow inputs: exact tag and candidate-lock digest. Checkout the tag with `contents: read`; promotion job receives `contents: write`. Download every draft asset, verify tag signer, candidate graph/checksums, evidence/certification/review signatures, authorized distinct reviewer, exact digest input, required `passed` gate set, and asset list. Then run only:

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
git add scripts/stage-release.sh .github/workflows/release.yml release/publisher-policy.json \
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

Expected: empty initial status and every command exits `0`. Use Phase 2's documented isolated fixture stack for source-level Playwright and Phase 3's pinned Rust/native prerequisites; these are pre-freeze implementation checks and may build disposable outputs. Stop/clean task-owned resources after checks. Final candidate local catalog gates verify frozen bytes or source/fixture contracts only; they must not rebuild/replace candidate artifacts. Version edits require repeating affected version/graph tests before tagging, not an untested final mutation.

- [ ] **Step 2: Create and verify the signed beta tag**

Set the chosen `0.1.0-beta.N` consistently, commit the version-only change, create an authorized signed annotated tag `v0.1.0-beta.N`, and verify it with `release/telos-tag-signers`. Push commit/tag only after review. Record tag object and commit IDs.

- [ ] **Step 3: Build and freeze the candidate**

Run `.github/workflows/candidate.yml` for the signed tag. Download the candidate archive, verify detached checksum and internal graph, extract to a read-only external directory, and record `candidateLockDigest`. From this point, do not rerun the build workflow for this version. Save expected public signer files/fingerprints from the reviewed release branch separately and verify the tag against that trust, not an untrusted signer file obtained solely from the candidate being verified.

- [ ] **Step 4: Run all local catalog gates against the frozen artifacts**

```bash
python3 scripts/run-gates.py --catalog ci/phase-gates.json --scope local \
  --candidate-lock /srv/telos-beta-candidate/candidate-lock.json \
  --evidence-dir /srv/telos-beta-evidence/raw/local
```

Expected: every required local envelope is `passed`, current, and candidate-bound. Any failure invalidates this candidate; fix on a new version.

- [ ] **Step 5: Run all external gates without rebuilding**

Install only the frozen archive/package bytes and complete, in dependency-safe order:

1. clean Ubuntu installation and guided integrations;
2. automated and guided browser matrix;
3. Linux desktop guided journeys;
4. runtime hardening and monitoring;
5. encrypted backup and separate-host restore;
6. predecessor upgrade/rollback;
7. controlled egress and external exposure;
8. capacity;
9. final candidate SBOM/vulnerability/license scan.

Use `--inputs /secure/telos-beta/gate-inputs.json` plus repeatable `--prior-evidence-dir` for completed local/earlier external roots, with the runner's declared per-gate inputs file and producer result adapters; write all external gate directories beneath `/srv/telos-beta-evidence/raw/external`. Producers never forge their own successful envelopes. Stop at first `failed`; preserve evidence and create a new candidate after remediation. If any environment is unavailable, record `not_run` and do not certify.

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

TELOS_EVIDENCE_SIGNING_KEY=/secure/telos-release/id_ed25519 \
TELOS_EVIDENCE_SIGNING_PRINCIPAL=release@telos.local \
bash scripts/certify-beta.sh \
  --candidate /srv/telos-beta-candidate \
  --evidence-bundle /srv/telos-beta-evidence/bundle \
  --catalog ci/phase-gates.json \
  --out-dir /srv/telos-beta-evidence/certification
```

Expected: evidence/certification signatures verify, every required gate is `passed`, and certification names the frozen candidate digest.

- [ ] **Step 7: Conduct a two-person release review**

One reviewer who did not operate the primary candidate run independently verifies tag, archive/checksums, graph, signer fingerprints, exact gate set, evidence freshness/identity, external raw log digests, RPO/RTO, capacity thresholds, ports/egress, SBOM/scan policy, unsigned-client instructions, and absence of secrets. Write a separate canonical `release-review.json` containing candidate-lock digest, certification digest, reviewer principal, reviewedAt, and decision `approved`; sign with namespace `telos-beta-review-v1`. The reviewer must differ from the primary operator. This sidecar is not inserted into the already-signed evidence index or certification; stage/promotion verifies and requires it using approved evidence principals.

- [ ] **Step 8: Stage and verify the draft prerelease**

Run `stage-release.sh` with candidate, signed evidence, certification, and reviewed notes. Redownload all draft assets on a separate machine and run release/evidence/certification verification. Expected: byte-for-byte digests match the frozen local copies.

- [ ] **Step 9: Publish by promotion only**

Dispatch `.github/workflows/release.yml` with the exact tag and candidate-lock digest. Expected: workflow verifies and changes draft state; published assets retain the draft digests. Check metadata before promotion; mismatch must leave the release draft. If the post-publication readback differs, fail the job, preserve the inventory/diagnostics and record publication as failed verification; do not falsely claim the already-promoted release stayed unpublished. Recovery requires an explicit operator action under release policy.

- [ ] **Step 10: Update product status after publication evidence**

Only after the published asset re-verification succeeds, update `documentation/product/beta-feature-status.md` with the certified beta version/date and evidence reference in a follow-up commit. Do not change the already-published source tag. Announce the invite-only release with checksum/signature verification and unsigned OS warnings.

- [ ] **Step 11: Archive recovery material**

Store the signed evidence/certification bundle, candidate archive, public signer fingerprints, release metadata, off-node snapshot ID, and restore/upgrade journals in the operator's encrypted off-node archive. Verify archive retrieval once. Private signing keys remain in their existing secure custody and are not copied into the archive bundle.
