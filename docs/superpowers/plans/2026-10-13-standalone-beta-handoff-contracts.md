# Standalone beta: shared implementation contracts

**Date:** 2026-10-13. **Status:** target contracts for implementation, not claims of implemented behavior.

**Goal:** Give every phase one definition of the interfaces it shares. Read the [cold handoff](2026-10-13-standalone-beta-cold-handoff.md) for order and the [design](../specs/2026-08-20-standalone-beta-readiness-design.md) for rationale.

**Architecture:** Rootful Ubuntu Podman hosts isolated non-root application containers; persistent node state lives outside immutable release generations; one runner owns evidence envelopes.

**Tech stack:** Existing Go/Next.js/Tauri/Bash stack; Python standard library for canonical records and validators. Follow committed package/tool locks; do not infer installed tools from documentation.

**Global constraints:** This file owns cross-phase interfaces. Phase 5 Task 1 owns exact release graph schemas; Phase 5 Task 4a owns per-gate input mapping. Phase plans own task-specific behavior. Changing an interface requires updating its producers, consumers, negative fixtures and this document together.

**Review focus:** Hash cycles, conflicting filesystem ownership, duplicate envelope writers, copied media capabilities, and rehearsal evidence mistakenly accepted as certification.

## 1. Platform and filesystem

[Platform policy](../../../documentation/product/platform-scope.md) is binding. Production server: Ubuntu 24.04+ x86-64, Podman 5+, rootful reference runtime. Initial clients: hosted browsers and Ubuntu-built Linux x86-64 desktop. Windows/macOS native clients are deferred; Windows/macOS/WSL/other-Linux server work is excluded. Automated WebKit supplements actual desktop Safari and iOS Safari checks.

| Path / identity | Contract |
|---|---|
| `TELOS_NODE_PATH` | Production `/var/lib/telos`; development `$REPO_ROOT/.telos`; absolute canonical path, never inferred from current release directory |
| Node environment | `$TELOS_NODE_PATH/node.env`, root-owned mode `0600` in production |
| Signing files | `$TELOS_NODE_PATH/config/`, core UID/GID `10001:10001`, directory `0700`, files `0600`; bind directory read-only at `/run/telos` |
| Durable operator state | `$TELOS_NODE_PATH/state/`, root-owned `0700`; journals, maintenance lock and installed receipt; never in candidate payload |
| Immutable releases | `/srv/telos/generations/<candidate digest hex>/`; `/srv/telos/current` atomically selects active generation |
| Runtime privilege | Production CLI runs as root (`sudo telos`); container core `10001:10001`, static client UID/GID `101:101`; upstream-specific storage groups defined by Phase 4 policy |
| Backup scheduling | Root-owned system service/timer using rootful Podman, not a user unit |

Phase 2 creates `node_env_path` in `cli/lib/config.sh` and replaces every Bash CLI assumption that environment is `$REPO_ROOT/.env`. Inventory consumers with `rg` before editing. Compose uses explicit `--env-file` with that helper's result. Templates remain `.env.example`. Existing `.env` is an explicit validated import operation, never an implicit initialization or secret-regeneration trigger. Validate ancestor/path ownership and reject symlink redirection. Provision new writable directories deliberately; do not recursively chown existing media or databases. Initialization must refuse overwrite and doctor must prescribe targeted repair.

**Compose driver contract:** use `podman compose` with `PODMAN_COMPOSE_PROVIDER` set to an absolute, operator-verified `podman-compose` 1.6.0 executable; do not fall through to Docker. Pin its installation artifact/hash in the Phase 2 prerequisite record and Phase 4 tool lock. [Upstream 1.6.0 release](https://github.com/containers/podman-compose/releases/tag/v1.6.0) is the chosen provider baseline; the Ubuntu VM check still must prove rendering, run/stdin, read-only mounts, networks and enforced resource limits. `cli/lib/common.sh` owns argv construction and rootful identity checks; all CLI paths call it. Certification refuses a different provider until a reviewed pin change and repeated compatibility checks.

Only two physical random signing roots are mounted: cursor and HLS. Existing audiobook signing remains domain-separated from the HLS root; direct-audio tickets use a distinct domain label. Do not create an unrelated third physical secret or reuse cursor key material for media.

## 2. Authentication and media

Cookie browser mode retains same-origin cookies/CSRF. Native mode uses device registration, rotating refresh credentials in Linux Secret Service, and access tokens in memory. Keyring adapter identity is `{origin, accountId, deviceId}`; normalize trusted HTTPS origins before deriving the entry name. Phase 3 owns locked-keyring, write failure, deletion failure and legacy plaintext retirement behavior. No silent persistent browser-storage fallback.

| Interface | Required behavior |
|---|---|
| `POST /api/v1/auth/devices/register` | Reachable without an existing bearer/session; handler verifies password or valid browser session, rate limits and validates metadata |
| Tauri `credential_set/get/delete` | Arguments `{key:{origin,accountId,deviceId}, secret?}`; secret only for set; get returns string or null; structured errors, never secret-bearing messages |
| `POST /api/v1/stream/video/{id}/playback-locator` | Authenticated issuer for HLS URL |
| `POST /api/v1/stream/audio/{id}/stream-ticket` | Authenticated issuer for direct audio URL |
| `GET /api/v1/hls/{locator}` | Capability-authorized media request; no separate cookie or bearer required |
| `GET /api/v1/stream/audio-ticket/{ticket}` | Capability-authorized audio/range request; no separate cookie or bearer required |

A signed URL is a bearer capability: anyone possessing it can spend it until expiry/revocation. Binding signed claims to the issuing user/device/session prevents claim substitution and enables current-state revocation; it does **not** prove the spending device's identity. Tests reject tampering, wrong resource, expired credentials and revoked/disabled issuers, not a byte-identical copy to another device. Recheck account, session/device and current permission on every spend. Bound expiry to the active session or live unconsumed refresh-credential horizon; the current devices table has no independent expiry field. Phase 3 specifies playback duration/renewal and Range/HEAD behavior. Sanitize access logs so path capabilities never enter evidence or logs.

The authenticated fetch layer must refresh once on eligible access-token expiry, deduplicate concurrent refresh attempts, retry at most once, and preserve credentials on network/5xx failures. Native artwork/image elements must use an authenticated retrieval path with cleanup, not bare unauthenticated image URLs. Platform-trusted TLS is the only transport; remove the non-enforcing fingerprint probe.

## 3. Integration discovery

`telos configure integrations` sends JSON via stdin to a disposable gateway container on production networks. Its command is `podman compose ... run --rm --no-deps -T telos-core ./telos-core validate-integrations` (the image has CMD, not ENTRYPOINT). Request fields are `jellyfinToken`, `grimmoryUser`, `grimmoryPassword`; response has `jellyfin` and `grimmory` arrays of `{id,name}` only. No credentials in argv, output, temporary evidence, or shell tracing.

Phase 2 Task 4 must capture a sanitized request/response fixture from the exact locked Grimmory version before implementing its decoder. An unavailable upstream blocks that verification step; do not guess endpoint/response fields from a historical example. Local mock/error tests can proceed. Atomic persistence targets `node.env` through the common helper, after selected library IDs have been validated.

## 4. Canonical bytes and immutable graph

Canonical JSON is UTF-8, recursively sorted object keys, compact separators, no NaN/Infinity, exactly one trailing LF. Reject duplicate keys when parsing. Dates use UTC RFC3339 `YYYY-MM-DDTHH:MM:SSZ`. Digests use `sha256:` followed by 64 lowercase hex digits unless a schema explicitly names a raw `sha256` field; source commits are 40 lowercase hex digits. Reject traversal, symlinks and path escape for indexed files. One serializer/validator lives in `scripts/lib/release_graph.py`; evidence helpers call it where required without breaking existing envelope v2 validation.

The directed graph is: payload and artifact-bound SBOM bytes → artifact index → release manifest → candidate lock → gate results/envelopes → evidence index → signatures/certification. Exact fields, raw-vs-prefixed digest rules and exclusion sets are in [Phase 5 Task 1](2026-08-20-standalone-beta-phase-5-candidate-certification.md#task-1-define-the-release-graph-and-enroll-real-trust-anchors).

`generation.json` inside the payload must not contain `candidateLockDigest`, hash itself, or inventory downstream graph metadata. Its source/version, images and migration compatibility fields are sufficient to build it before candidate identity exists. Installation writes a separate receipt in node state with `schemaVersion`, `candidateLockDigest`, `generationManifestDigest`, `generationPath`, `installedAt`. The receipt is observed evidence, not payload. Candidate-bound scanning evidence is generated after freeze; artifact-bound SBOMs are generated before indexing.

A frozen artifact is never replaced by a check. Source tests may compile scratch output, but external gates load/install only indexed frozen bytes. Any relevant source/artifact fix requires a new candidate and new candidate-bound evidence.

## 5. Gate runner and producer contracts

Preserve envelope schema **v2** and its existing fields: `schemaVersion`, `gateId`, `status`, `reason`, `sourceCommit`, `candidateLockDigest`, `command`, `startedAt`, `finishedAt`, `exitCode`, `outputDigest`, `toolVersions`, `subject{kind,digest}`. `scripts/run-gates.py` is the sole envelope writer. Retain process-tree containment, atomic writes, log binding, timeout handling, clean-tree/source checks and all negative fixtures.

Phase 5 Task 4a migrates catalog v1 to **v2** before producers consume new fields. Existing gate fields remain; add `subjectSelector` (string), `maxAgeHours` (positive integer), and `requiredPlatforms` (array, empty allowed). Preserve optional `terminationGraceSeconds`. Update validator, checked-in catalog, fixture catalogs and schema consumers together. Expand the closed tool-version key policy explicitly for tools used by the final catalog; do not accept arbitrary keys.

| `subjectKind` | `subjectSelector` | Actual identity |
|---|---|---|
| source | `sourceCommit` | SHA-256 of the commit string's UTF-8 bytes, no newline |
| fixture | `completed-output` | Final captured log digest |
| artifact | `artifact:<index-relative-path>` or `artifact-index` | Verified frozen artifact bytes or canonical index bytes |
| runtime | `observed-runtime` | Canonical observed runtime descriptor below |
| external-host | `observed-host` | Canonical host descriptor below |

Source and fixture commands need no producer sidecar. Other producers take `--result-out PATH` and exactly one mode: rehearsal (`--rehearsal --source-commit SHA`, plus `--rehearsal-lock PATH` for installed/artifact drills) or final candidate (`--candidate-lock PATH`). Source-only mock rehearsals may omit a lock; installing a generation or exercising backup/upgrade requires a fully verified non-final rehearsal graph/lock. Its digest addresses the generation and populates the same installed receipt schema. Results/descriptors keep that digest with `recordKind=rehearsal`; final import rejects them by recordKind, even if their graph is valid. The two lock flags are mutually exclusive. The terminal certifier is the explicit exception described in Section 7. They write a closed result record atomically:

```text
schemaVersion: 1
recordKind: rehearsal | candidate
gateId: catalog ID
sourceCommit: commit
candidateLockDigest: digest (null only in rehearsal)
status: passed | failed | not_run
reason: sanitized, nonempty explanation
subjectFile: relative file path (null allowed only for failed/not_run)
attachments: [{path: relative path, sha256: raw lowercase hex, kind: nonempty string}]
toolVersions: same closed key policy as envelope
```

All paths resolve beneath that producer's output directory. Reject unlisted attachment files at import, traversal, symlinks, changed bytes, unknown fields and duplicate paths. `subjectFile` must also occur in attachments with matching bytes; require attachment kinds from the schema enum defined by Phase 5 Task 1. Producers emit sanitized stdout summaries, never envelopes. Runner owns times, captured log, exit code and candidate digest; it checks result gate/source/lock, statuses and actual exit code (`0` passed, `3` not_run, nonzero other than `3` failed). Crashes/malformed records become runner-owned failed envelopes. A missing prerequisite yields `not_run`; neither state certifies.

For catalog v2 the runner creates `<evidence-dir>/<gate-id>.d/` (mode `0700`, empty) for each producer, appends the reserved options `--candidate-lock PATH --result-out <that dir>/result.json` (plus fixed input options below), and exports `TELOS_GATE_ID=<catalog gate id>`; a producer must emit exactly that `gateId`, which is how scripts shared by several gates stay unambiguous. Catalog commands may not contain the reserved options. A producer exits `0` only with a `passed` result and a verified subject descriptor, `3` for `not_run` (a result is optional), and anything else is `failed`. A prerequisite in another scope is never executed: it must arrive through `--prior-evidence-dir`, whose envelopes are re-validated against the current candidate, log digest, producer result and the gate's `maxAgeHours`; stale, failed or `not_run` prior records satisfy nothing and their dependents are `not_run`. After the first required `failed` gate every remaining gate is recorded `not_run` with a blocked reason.

Runner uses `--inputs PATH` for an external JSON manifest with `gateInputs` keyed by gate ID; values are named file/directory paths mapped to fixed options by Phase 5 Task 4a. No arbitrary argv extension, command override, secret value, or user-supplied `--candidate-lock`/`--result-out`. Unknown gates/keys fail. Protected operator files live outside checkout/evidence; evidence contains sanitized observations only. Repeatable `--prior-evidence-dir PATH` allows verified prior required gates from the same candidate without rerunning destructive operations. Reject duplicate IDs across current/prior sets. Rehearsal results can never enter this reuse path.

## 6. Observed subjects

Runtime descriptor closed shape:

```text
schemaVersion: 1
kind: runtime
sourceCommit, candidateLockDigest, observedAt
hostId: sha256 of raw /etc/machine-id bytes
bootId: value from /proc/sys/kernel/random/boot_id
runtime: {name: podman, version: observed version string, rootless: false}
generationReceiptDigest: sha256 digest
services: [{name, containerId, imageId, manifestDigest}] sorted by name
```

Record actual image/config ID separately from OCI manifest digest. Compare each manifest digest with the candidate image lock and runtime image inspection. Never hash a claimed Compose configuration as proof of observed runtime. Mount/network/security/resource assertions are sanitized attachments, not raw `podman inspect` or rendered Compose containing secrets.

External-host descriptor closed shape:

```text
schemaVersion: 1
kind: external-host
sourceCommit, candidateLockDigest, observedAt
purpose: gate ID
hosts: [{hostId, identityMethod, os, osVersion, arch}] sorted by hostId
records: [{path, sha256}] sorted by path
```

`identityMethod` is `machine-id-sha256` or `tester-assigned`; machine IDs are hashed, never published raw. Linux recovery nodes require machine-derived identity; browser/mobile tester identities are stable nonsecret IDs. Record hashes are raw hex, paths refer to sanitized attachments. A separate-host restore must demonstrate different source and target machine identities. Same-host rollback is separately labeled and cannot satisfy it. Rehearsal descriptors alone permit null candidate digest.

## 7. Final certification boundary

Required evidence means required catalog IDs **excluding `beta-certification` itself**. The reserved aggregate ID remains declarative in the catalog but is never scheduled by the runner, under any scope or explicit gate selection. Invoke the standalone `certify-beta.sh --candidate PATH --evidence-bundle PATH --catalog PATH --out-dir PATH` after verification; it writes signed certification, not a gate-result or envelope. The runner must reject explicit attempts to schedule this reserved ID with a corrective invocation message. Certification is outside its input index and cannot depend on itself. Phase 5 updates product contract, checker and catalog required external set together.

Default freshness: 168 hours; runtime hardening, host exposure, separate-host restore and upgrade/rollback: 72 hours; candidate supply-chain: 24 hours. Check again at promotion. Missing, duplicate, stale, unsigned where required, mismatched, failed or not_run evidence blocks certification. Guided records, evidence index, release review and certification have distinct defined signing namespaces in Phase 5. Private keys and secret-bearing traces never enter artifacts.

## 8. External prerequisite records

The implementer must validate, not silently assume: an Ubuntu 24.04 x86-64 host with a verified Podman 5+ installation/provider; second distinct restore host; scanner/load host; trusted HTTPS domain and cutover access; legal media/library fixtures; off-node restic storage and credentials; Linux desktop with Secret Service; actual browser/device matrix; approved public signer enrollment and private signing access; buildable predecessor and reviewed publisher identity.

Ubuntu 24.04 package availability must be checked against the [Ubuntu package index](https://packages.ubuntu.com/search?keywords=podman&searchon=names&suite=noble) and [Podman installation documentation](https://podman.io/docs/installation). Do not assume `apt install podman` supplies the required major version or mix distribution repositories. Phase 2 records an operator-provided pinned installation until a supported provisioning recipe is verified. Missing resources block their affected live actions and final certification, while unrelated source/fixture work continues.
