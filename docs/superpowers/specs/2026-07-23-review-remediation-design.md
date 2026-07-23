# Review Remediation Design

**Date:** 2026-07-23

**Status:** Approved

## Objective

Remediate the release-engineering, runtime-hardening, storage, deletion,
monitoring, event-idempotency, chat-recovery, and Watch Party findings from the
beta-readiness review. Every executable gate must report results derived from
the candidate it actually inspected. A missing dependency or required external
environment must produce an explicit failure instead of fabricated passing
evidence.

The normative architecture and operations documentation remain authoritative.
Where an external certification environment is unavailable on the development
host, Telos will ship executable tooling and fixture-backed contract tests but
will not claim that the external gate passed.

## Delivery Strategy

Work proceeds in four dependency-ordered stages:

1. Establish trustworthy release inputs and operational tooling.
2. Harden and wire the deployable runtime.
3. Repair backend durability, authorization, and concurrency contracts.
4. Repair frontend acknowledgement and reconnect recovery, then run the
   accumulated verification suite.

This order prevents later evidence from being generated against a release
candidate that cannot be built, installed, or started.

## Release and Certification Architecture

### Accumulated gate registry

`ci/phase-gates.json` is the machine-readable registry of Phase 1–6 commands.
The accumulated-suite runner validates the registry, executes every enabled
gate from the repository root, records command, start/end timestamps, exit
status, and output digest, and stops with a nonzero status when any command
fails. Reports bind to the Git source commit and tree plus an optional
candidate-lock digest. A report is signed only after all registered gates pass.

No shell interpolation is accepted from registry entries. Commands are stored
as argument arrays and dispatched through a small allowlisted runner. This
avoids turning a versioned JSON document into an arbitrary shell-evaluation
surface.

### Runtime-hardening verification

The hardening verifier has a static source mode and a disposable runtime mode.
Both require existing Compose and environment files. Static checks operate on
the rendered Compose model and verify:

- explicit non-root identities;
- `no-new-privileges`, dropped capabilities, and read-only roots;
- bounded writable mounts, tmpfs paths, resources, ulimits, health checks,
  restart policies, and stop grace periods;
- secret-file mounts matching configured `/run/telos/...` paths;
- internal-only database, cache, administration, health-detail, and metrics
  listeners;
- approved public ports only.

Runtime checks inspect the started containers and compare effective identity,
privileges, mounts, and listeners with the source policy. Evidence is written
atomically only after validation completes and includes the rendered-model
digest and inspected container-image digests. Secret values and rendered
environment contents never appear in evidence.

### Reproducible release bundle

The release builder consumes a clean source commit, version, build epoch, and
`release/images.lock`. It creates:

- `telos-core.oci.tar`;
- the rendered, digest-pinned production Compose file;
- source and image SBOMs;
- `telos-ops.tar.zst` with exact operational inputs;
- `artifacts.json`;
- `release-manifest.json`;
- `SHA256SUMS`;
- detached Cosign bundles for the manifest and checksum index;
- a content-addressed `candidate-lock.json` and its signature bundle.

Every archive uses sorted entries, normalized owners and permissions, and the
declared build epoch. Hash edges follow `release/hash-graph.md`; recursive or
self-referential hashes are rejected.

`release/images.lock` contains resolved registry digests rather than examples.
Compose generation consumes the lock, and verification confirms that every
production service and build base is covered exactly once. Network resolution
is used only to refresh pins explicitly; normal builds use the committed lock.

### Signing trust

A new Cosign key pair and SSH tag-signing key pair will be generated. Complete
public trust roots and signer allowlist material are committed. Private keys are
created with mode `0600` in a gitignored operator bootstrap directory and are
also supported from an external operator directory. Release tooling refuses to
continue if private material is tracked, group/world-readable, or copied into a
release payload.

The bootstrap directory is transitional: the operator must escrow the private
keys outside the checkout before production release and may then remove the
local copy. Verification uses the committed public roots plus independently
configured fingerprints.

### Installation, upgrade, and rollback

Operational commands consume a verified release directory or candidate lock.
Installation performs preflight validation, verifies signatures/checksums,
extracts operations inputs into a versioned generation, creates required
directories with explicit modes, renders configuration, starts the migration
job and services, and waits for readiness.

Upgrade acquires the shared maintenance lock, verifies predecessor
compatibility, creates an encrypted pre-change backup, installs the candidate
as a new generation, runs migrations, activates it atomically, and verifies
readiness. Rollback reactivates the prior compatible generation or invokes the
verified backup restore path when schema compatibility forbids binary-only
rollback. Interrupted operations retain a journal that a subsequent run can
recover.

Fixture mode exercises the same state machine and filesystem transitions with
fake container and network adapters. It never emits production certification.

### Backup confidentiality

Plaintext database dumps and file snapshots exist only inside a mode-`0700`
temporary directory. The backup script writes them to an authenticated,
encrypted restic repository, verifies the resulting snapshot, and removes the
temporary generation on every exit path. If upload or verification fails, the
script returns nonzero after cleanup. Retention operates on encrypted snapshots
only.

### CI

CI adds independently failing jobs for:

- backend tests, race checks, vet, and vulnerability checks;
- frontend lint, unit, build, audit, and browser smoke;
- dependency review, secret scanning, and license policy;
- source/image SBOM generation and validation;
- Compose validation and runtime-hardening policy tests;
- operations fixture tests;
- the accumulated suite and candidate-bound certification-policy checks.

Actions and tool containers are immutable pins. Jobs use minimum permissions,
timeouts, concurrency cancellation, and artifact hashes.

## Runtime Architecture

### Container isolation and secrets

The gateway image creates UID/GID `10001`, copies only runtime artifacts, and
runs under that identity. The migrator uses the same identity. Compose applies
`no-new-privileges`, drops all capabilities, uses read-only roots where
supported, and supplies bounded tmpfs and explicit writable volumes.

Cursor, HLS, session, and related runtime secrets are file mounts under
`/run/telos`. The canonical environment points only to those mounted paths.
Startup validates file existence, ownership/permissions, minimum entropy, and
separation between keys without logging values.

### Storage and deletion

Startup opens confined descriptors for staging, shared, avatar, and private
storage roots. It constructs the quota guard, ClamAV scanner, upload pipeline,
file catalog, reconciliation service, and physical asset remover before routes
or workers start. Failure to initialize any required dependency aborts startup.

All public upload routes use `UploadPipeline`. Legacy handlers are removed or
reduced to adapters that cannot bypass quota, confinement, validation, scanning,
atomic promotion, catalog audit, or durable lease behavior.

The production asset remover maps catalog area and key to confined storage. A
missing file is an idempotent success. Unsafe keys or storage errors keep the
deletion job pending with bounded retry metadata; they are never marked done.

### Monitoring

The gateway serves metrics on a dedicated internal listener matching the
Prometheus target. The public listener never registers `/metrics`. Compose
deploys digest-pinned Prometheus and Alertmanager services on the internal
monitoring network with bounded retention, resources, and storage.

Readiness and scrape configuration are tested together so a port or path change
cannot silently disconnect monitoring. Alert rules are validated with
Prometheus tooling, and every alert has a runbook reference.

## Backend Correctness

### Security-event idempotency

Each security mutation receives an operation identifier at its transaction
boundary. The outbox idempotency key includes event kind and that identifier,
not merely actor/subject/resource. Retrying the same mutation reuses its
identifier and produces one event; a later password, role, invite, or account
operation receives a new identifier and produces a distinct event.

The identifier is persisted with the mutation or derived from a unique durable
row/event identifier created in the same transaction. Random identifiers
generated afresh during retry are not acceptable because they defeat replay
idempotency.

### Watch Party authorization

Party and playback-state reads require an active `joined` membership row.
Handlers additionally reauthorize the referenced media item and linked text and
voice channels for the current user. A host or invited-but-not-joined user does
not bypass current resource authorization.

### Watch Party atomicity

Playback control uses a Redis Lua script that:

1. loads and decodes the current state;
2. compares its version with `expectedVersion`;
3. validates that the lease key still identifies the authorized host
   generation and is unexpired;
4. writes version `N+1` with the existing TTL in the same atomic operation.

The Go layer validates bounded numeric and action inputs before invoking the
script and maps explicit Lua results to stable API errors. Concurrent requests
with one expected version yield exactly one success and one stale-version
response.

Lease-expiry auto-pause uses the same compare-and-set mechanism so it cannot
overwrite a simultaneous valid host control.

## Frontend Recovery

### Composer acknowledgement

The root composer owns a draft mutation record containing the text/embed and a
stable `clientMutationId`. Submit awaits the API acknowledgement. On success it
clears only the acknowledged draft; on failure it preserves text, embed, and
mutation ID and exposes a retryable error. Repeated submission of the same
pending draft reuses the ID, while editing the draft creates a new operation
only after the prior result is known.

### Channel change reconciliation

The chat store records the highest contiguous channel-change sequence applied
per channel. Initial history establishes a reconciliation watermark. After a
socket interruption, the client:

1. opens a replacement connection without discarding durable local state;
2. fetches `/api/v1/channels/{id}/changes` from the stored sequence, following
   pagination until caught up;
3. applies creates, edits, deletions, reactions, and pin changes in sequence;
4. refreshes pins when a pin delta cannot be represented locally;
5. merges socket events received during reconciliation;
6. advances the stored sequence only after each change is applied.

Message merging is by durable message ID and sequence, not append order.
Switching channels cancels the old reconciliation and prevents late responses
from mutating the new channel.

## Error Handling

Operational scripts use strict shell mode, validate all paths and modes before
mutation, acquire the shared maintenance lock, write state atomically, and
return nonzero on missing tools, inputs, signatures, images, external
environments, or failed commands. Evidence distinguishes `passed`, `failed`,
and `not_run`; only `passed` satisfies certification.

Backend initialization errors prevent readiness. Background deletion and
reconciliation errors remain durable and retryable. API responses preserve
stable public error codes while logging bounded correlation metadata without
secret or private-content fields.

Frontend failures retain user-authored state and expose retry. Reconnect loops
remain bounded with jitter and stop permanently on authorization revocation.

## Testing Strategy

Every behavior change follows a red-green-refactor cycle.

- Shell contract tests use temporary directories and fake adapters to prove
  missing inputs, command failures, interrupted operations, cleanup, candidate
  mismatch, and signature failures are propagated.
- Go unit and integration tests cover startup dependency construction,
  physical deletion retry, repeated distinct security mutations, party read
  authorization, concurrent Redis controls, and lease-expiry races.
- Vitest covers acknowledged and failed root sends, stable mutation IDs,
  paginated change reconciliation, buffered socket events, duplicate delivery,
  and channel switches.
- Compose validation renders `.env.example`, while hardening tests inspect the
  rendered model and disposable runtime where available.
- Final verification runs the repository-prescribed containerized backend and
  frontend Playwright commands plus lint, unit, build, operations, security,
  release, and accumulated-suite gates.

External clean-node, off-node restore, DNS cutover, alert delivery, capacity,
and physical browser/device gates remain explicit certification prerequisites.
Local fixture success must not be represented as evidence that those external
gates passed.

## Completion Criteria

The remediation is complete when:

- every live review finding has a regression test and implementation;
- findings already partially implemented are completed without regressing
  existing behavior;
- no certification or release command can report success without executing and
  validating its declared work;
- the production Compose configuration starts with mounted secrets, non-root
  identities, wired storage/deletion, and reachable internal monitoring;
- release artifacts, manifests, hashes, SBOMs, candidate locks, and signatures
  are produced and independently verifiable;
- all locally executable prescribed gates pass;
- unavailable external gates are reported as incomplete rather than passed.
