# Standalone Beta Phase 4: Operational Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the standalone server safe to operate through an invite-only beta by pinning its supply chain, enforcing effective runtime hardening, producing encrypted off-node backups, proving separate-host recovery and generation-safe upgrades, and validating monitoring, capacity, exposure, and controlled egress.

**Architecture:** Treat operational policy as machine-readable input and inspect the effective runtime rather than trusting Compose text. Lock every production/build image to a reviewed registry digest. Run scheduled maintenance under one lock and journal every destructive transition. Stop writers briefly while collecting a consistent backup, upload and verify it through restic, then delete plaintext on every exit path. Restore only onto a distinct reference host from a compatible release generation. Keep fixture tests for deterministic failure branches, while live candidate evidence remains mandatory and separate.

**Tech Stack:** Podman 5+, Compose, Bash, Python 3 standard library, restic, systemd user units, Prometheus, k6, Syft, Trivy, gitleaks, Go/Rust/npm dependency metadata, Ubuntu 24.04 LTS x86-64.

**Spec:** `docs/superpowers/specs/2026-08-20-standalone-beta-readiness-design.md`

## Global Constraints

- Execute from the merged Phase 2 baseline. Phase 3 may proceed concurrently, but Phase 5 consumes only the merged outputs of both phases.
- The external operational gates run against one reference candidate generation on Ubuntu 24.04 LTS x86-64. Fixture success never satisfies backup, restore, upgrade, rollback, capacity, exposure, or controlled-egress candidate gates.
- Production image references and build-stage `FROM` lines use registry digests. Tags may remain only as human-readable context before `@sha256:`; mutable tag-only production references are forbidden.
- Refreshing an image/tool lock is an explicit reviewed commit. Normal build, install, upgrade, and certification commands never resolve `latest` or silently update a digest.
- Gateway and static client run as explicit non-root identities. Every long-running service has a verified effective identity, `no-new-privileges`, bounded capabilities, writable paths, resources, PIDs, file descriptors, and logs. A publisher-required upstream root identity must be a narrowly scoped policy exception with rationale, compensating controls, owner, issue, and expiry; beta certification cannot accept a root exception for core/client.
- Only Traefik publishes Telos ports. Setup overlays are absent during normal operation. Host-management ports are declared separately and never mistaken for Telos listeners.
- Backup repositories must be off-node (`s3:`, `sftp:`, or `rest:https://` for beta). Reject local paths, `local:`, `file:`, ambiguous `rclone:`, and repositories under the Telos host storage/release/config trees.
- Restic passwords come from a mode-`0600` file. Repository credentials come from operator-owned environment/credential files and are never printed, captured in evidence, or placed in argv.
- Plaintext backup material exists only in a mode-`0700` `mktemp` directory and is removed on success, command failure, signal, restic failure, and restart recovery. There is no keep-plaintext switch in beta commands.
- Retention is exactly 14 daily and 8 weekly snapshots tagged `scheduled`; pruning occurs only after a new verified snapshot.
- Recovery must revoke all browser sessions, device/access/refresh credentials, and outstanding invites before admitting traffic. Users reauthenticate with passwords after a restore.
- Upgrade uses one maintenance lock, verified encrypted pre-upgrade backup, one-shot migrations, atomic active-generation pointer, bounded readiness wait, and durable journal. Binary rollback occurs only when the predecessor declares the resulting schema compatible; otherwise use verified restore.
- External evidence uses Phase 1 envelopes and binds candidate/runtime/host identities. A missing second host, scanner, backup repository, DNS cutover, or load generator is `not_run`.
- Never run destructive live tests against an undeclared host. Each live command requires explicit candidate lock, target identity, evidence path, and documented confirmation token.

---

## Current-State Baseline

Production still references mutable `latest` tags for ClamAV, Jellyfin, and Grimmory (`docker-compose.yml:248-270`, `docker-compose.yml:308-310`). The runtime-hardening script can emit a passed record without inspecting a runtime (`scripts/verify-runtime-hardening.sh:22-33`), and the capacity drill reports completion without a load test (`scripts/run-capacity-drill.sh:18-24`). Backup documentation claims an encrypted off-node path (`documentation/operations/backup-and-restore.md:6-11`), but the current script creates a plaintext generation and its cleanup trap has no removal operation (`scripts/backup.sh:4-24`). This phase replaces those baselines with candidate-bound runtime and recovery proofs.

---

## File Structure

- Create: `release/images-lock.schema.json` — strict production/build image lock.
- Modify: `release/images.lock` — reviewed registry/platform digests for every image.
- Create: `scripts/refresh-image-lock.py` — explicit resolver producing reviewable lock changes.
- Create: `scripts/verify-image-lock.py` — Dockerfile/Compose/runtime lock verification.
- Modify: `scripts/verify-release-pins.sh`, `backend/Dockerfile`, `frontend/Dockerfile`, `deploy/egress-proxy/Dockerfile`, `docker-compose.yml` — consume locked digests.
- Create: `tests/operations/image_lock_test.sh` — lock/schema/mutation tests.
- Create: `config/runtime-hardening.json` — per-service effective policy.
- Modify: `docker-compose.yml`, `config/traefik.yaml`, service Dockerfiles/entrypoints — hardening controls.
- Modify: `scripts/verify-runtime-hardening.sh`, `tests/operations/runtime_hardening_test.sh`, `documentation/operations/container-hardening.md` — static/live policy verification.
- Create: `backend/metrics_server.go`, `backend/metrics_server_test.go` — internal metrics listener/lifecycle.
- Modify: `backend/metrics.go`, `backend/main.go`, `config/prometheus/*.yml`, `docker-compose.yml` — truthful bounded metrics and internal Prometheus.
- Modify: `scripts/verify-monitoring.sh`, `tests/operations/monitoring_test.sh`, `documentation/operations/monitoring.md` — real source/runtime monitoring checks.
- Modify: `scripts/backup.sh`, `scripts/prune-backups.sh`, `deploy/systemd/telos-backup.service`, `deploy/systemd/telos-backup.timer` — encrypted scheduled snapshots.
- Create: `scripts/lib/backup_manifest.py`, `release/backup-manifest.schema.json` — backup contents/compatibility contract.
- Modify: `tests/operations/backup_restore_test.sh`, `scripts/tests/backup_test.sh`, `documentation/operations/backup-and-restore.md`, `documentation/operations/data-retention.md` — behavioral backup tests/runbooks.
- Modify: `scripts/restore.sh`, `scripts/verify-restore.sh`, `scripts/verify-restored-node.sh`, `scripts/restore-drill.sh`, `scripts/drills/recovery.sh` — verified separate-host recovery.
- Create: `backend/restore_sanitize.go`, `backend/restore_sanitize_integration_test.go` — transactional post-restore invalidation.
- Modify: `documentation/operations/restore-drill.md` — exact recovery gate.
- Create: `scripts/lib/generations.py`, `release/generation-manifest.schema.json` — installed-generation/journal contract.
- Modify: `scripts/upgrade.sh`, `scripts/rollback.sh`, `scripts/build-predecessor.sh`, `scripts/restore-state.sh`, `scripts/drills/upgrade-rollback.sh`, `tests/operations/upgrade_rollback_test.sh`, `documentation/operations/upgrade-and-rollback.md` — safe lifecycle.
- Modify: `tests/load/telos.js` — real k6 scenarios.
- Create: `scripts/prepare-capacity-fixture.py`, `scripts/collect-runtime-stats.py` — bounded load fixture/resource collection.
- Modify: `scripts/run-capacity-drill.sh`, `scripts/audit-host-exposure.sh`, `scripts/tests/egress_test.sh`, `tests/operations/capacity_exposure_test.sh` — real static/live gates.
- Create: `config/exposure-policy.json`, `documentation/operations/network-exposure.md`, update `documentation/operations/capacity.md` and `controlled-egress.md` — audited policy/runbooks.
- Modify: `ci/tools.lock`, `ci/license-policy.json`, `ci/risk-acceptance.schema.json` — supply-chain policy.
- Create: `ci/risk-acceptances.json`, `scripts/bootstrap-ci-tools.py`, `scripts/generate-sbom.sh`, `scripts/scan-candidate.sh`, `tests/operations/supply_chain_test.sh` — candidate scanning.
- Modify: `scripts/ci/validate-sbom.sh`, `.github/workflows/ci.yml`, `ci/phase-gates.json` — required operational/supply-chain gates.

---

### Task 1: Replace mutable and fabricated image pins with a verified lock

**Files:**
- Create: `release/images-lock.schema.json`
- Modify: `release/images.lock`
- Create: `scripts/refresh-image-lock.py`
- Create: `scripts/verify-image-lock.py`
- Modify: `scripts/verify-release-pins.sh`
- Create: `tests/operations/image_lock_test.sh`
- Modify: `backend/Dockerfile`
- Modify: `frontend/Dockerfile`
- Modify: `deploy/egress-proxy/Dockerfile`
- Modify: `docker-compose.yml`

**Interfaces:**
- Produces: schema-versioned lock entries with source tag, registry manifest digest, `linux/amd64` image digest, retrieval time, and registry.
- Produces: verifier modes `--source` and `--runtime`.
- Consumes later: release builder, runtime hardening, SBOM, and candidate lock.

- [ ] **Step 1: Write lock mutation tests**

Create a valid fixture lock, Dockerfiles, and Compose file. Assert verification fails for a 64-character repeated digest, missing service/base image, tag-only image, digest mismatch between source and lock, duplicate digest under unrelated source names, unknown lock key, wrong platform, mutable `latest`, unreviewed extra production image, and a runtime image ID not matching the locked platform digest. Assert it passes only when every production/build reference maps exactly once.

- [ ] **Step 2: Run the test and expose the current fabricated values/missing services**

```bash
bash tests/operations/image_lock_test.sh
```

Expected: nonzero because current lock values are synthetic and omit multiple images.

- [ ] **Step 3: Define the closed lock schema**

Use this entry shape:

```json
{
  "schemaVersion": 2,
  "images": {
    "postgres": {
      "source": "docker.io/library/postgres:16.14-alpine",
      "manifestDigest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "platforms": {"linux/amd64": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
      "registry": "docker.io",
      "resolvedAt": "2026-08-20T14:00:00Z"
    }
  }
}
```

Required keys cover Go/Node/Alpine/nginx build/runtime bases, Traefik, PostgreSQL, Redis, Squid, ClamAV, Jellyfin, MariaDB, Grimmory, Prometheus, restic helper (if containerized), k6, Syft, and Trivy. Test-only images live in a distinct `testImages` section and cannot satisfy production keys.

- [ ] **Step 4: Implement explicit resolution**

`refresh-image-lock.py --key KEY --source REF --platform linux/amd64 --output PATH` resolves through `skopeo inspect --raw` or `podman manifest inspect`, requires registry TLS validation, records both manifest/platform digests, and updates only the named key atomically. It prints old/new non-secret digests for review. It refuses `latest` unless the source project publishes no immutable version tag and `--allow-latest-source-label` is paired with a resolved digest; production files still receive the digest.

- [ ] **Step 5: Resolve and review real digests**

Run the resolver once per required key from a clean networked environment. Review architecture, upstream publisher, release notes, license/process boundary, and scan result before committing. Then replace every `FROM` and Compose `image:` with `source@sha256:$resolved_manifest_digest`. The shell variable here denotes resolver output; never copy the sample digest from the schema example.

- [ ] **Step 6: Implement source/runtime verification**

`verify-image-lock.py --source` parses all Dockerfile `FROM` and Compose image fields and cross-checks lock completeness. `--runtime --candidate-lock PATH` inspects every effective container image ID/platform and asserts it is either a locked third-party platform digest or a Telos image digest from the candidate lock. Both reject dirty lock changes during certification.

Make `verify-release-pins.sh` a strict wrapper around source verification and remove existence-only success.

- [ ] **Step 7: Run image checks and representative builds**

```bash
bash tests/operations/image_lock_test.sh
bash scripts/verify-release-pins.sh
python3 scripts/verify-image-lock.py --source
podman build -f backend/Dockerfile --target builder-false -t localhost/telos-core-lock-test .
podman build -f frontend/Dockerfile -t localhost/telos-client-lock-test frontend
podman rmi localhost/telos-core-lock-test localhost/telos-client-lock-test
```

Expected: all exit `0`; no tag-only production image is reported.

- [ ] **Step 8: Commit reviewed image locks**

```bash
git add release/images-lock.schema.json release/images.lock scripts/refresh-image-lock.py \
  scripts/verify-image-lock.py scripts/verify-release-pins.sh \
  tests/operations/image_lock_test.sh backend/Dockerfile frontend/Dockerfile \
  deploy/egress-proxy/Dockerfile docker-compose.yml
git commit -m "supply-chain: lock every production image by digest"
```

---

### Task 2: Enforce effective container hardening

**Files:**
- Create: `config/runtime-hardening.json`
- Modify: `docker-compose.yml`
- Modify: `config/traefik.yaml`
- Modify: `backend/Dockerfile`
- Modify: `frontend/Dockerfile`
- Modify: `deploy/egress-proxy/Dockerfile`
- Modify: relevant entrypoint/config files for upstream services only where documented mounts are required.
- Modify: `scripts/verify-runtime-hardening.sh`
- Modify: `tests/operations/runtime_hardening_test.sh`
- Modify: `documentation/operations/container-hardening.md`
- Modify: `cli/lib/preflight.sh`

**Interfaces:**
- Produces: static policy verification and live effective-runtime inspection bound to candidate identity.
- Consumes: image lock from Task 1 and standalone topology from Phase 2.

- [ ] **Step 1: Replace existence tests with policy mutation tests**

Test removal/change of each control independently: core/client user, `no-new-privileges`, capability drop, read-only root, unexpected writable bind/tmpfs, privileged mode, host PID/IPC/network, device mount, socket mount, resource/memory/CPU/PID limit, `nofile`, log rotation, restart, stop grace, health check, network membership, published port, and image digest. A policy service missing from Compose or a Compose service missing from policy also fails.

Add live-inspector fixtures for effective UID 0, added effective capability, writable `/`, unexpected listener, wrong image ID, wrong network, and unbounded log config.

- [ ] **Step 2: Run tests and capture the current false green**

```bash
bash tests/operations/runtime_hardening_test.sh
```

Expected: nonzero because the current test checks only that a markdown file exists.

- [ ] **Step 3: Define explicit per-service policy**

`config/runtime-hardening.json` lists every service with exact networks, allowed writable mounts/tmpfs, expected internal listeners, minimum stop grace, health requirement, CPU/memory/PID/nofile/log bounds, and whether it is one-shot. Use these upper bounds:

| Service | CPUs | Memory | PIDs | nofile |
|---|---:|---:|---:|---:|
| Traefik | 0.5 | 256 MiB | 128 | 8192 |
| core | 2 | 1 GiB | 256 | 16384 |
| client | 0.25 | 128 MiB | 64 | 4096 |
| PostgreSQL | 2 | 2 GiB | 256 | 16384 |
| Redis | 0.5 | 512 MiB | 128 | 8192 |
| egress proxy | 0.5 | 512 MiB | 128 | 8192 |
| ClamAV | 2 | 2 GiB | 256 | 8192 |
| Jellyfin | 4 | 4 GiB | 512 | 32768 |
| MariaDB | 2 | 2 GiB | 256 | 16384 |
| Grimmory | 2 | 2 GiB | 512 | 16384 |
| Prometheus | 1 | 1 GiB | 128 | 8192 |

CLI preflight requires at least 4 CPU cores, 16 GiB RAM, and sufficient configured storage for the full profile; lower hosts are unsupported rather than silently overcommitted.

- [ ] **Step 4: Harden core, client, and Traefik first**

Create UID/GID 10001 in core and keep its binary/config read-only; run client as its unprivileged image UID. Move Traefik internal listeners to 8080/8443 so it runs non-root without `CAP_NET_BIND_SERVICE`, while host mappings remain 80/443. Allow only ACME state as writable. Apply `read_only`, bounded tmpfs, `cap_drop: [ALL]`, `security_opt`, no devices/socket, resource/ulimit/log bounds, graceful stop, and health checks.

- [ ] **Step 5: Harden remaining services to their supported effective identities**

Add controls and only the writable data/config/tmp paths named in policy. Verify each upstream entrypoint still initializes under an empty volume. Do not force an arbitrary `user:` that makes upstream initialization fail; instead pin the publisher-supported non-root identity and prove effective UID at runtime. Any required initialization transition must end with non-root PID 1 before readiness.

- [ ] **Step 6: Implement static and live verifier modes**

Expose:

```text
scripts/verify-runtime-hardening.sh --compose docker-compose.yml --env-file PATH --policy PATH
scripts/verify-runtime-hardening.sh --runtime --candidate-lock PATH --policy PATH --evidence-out PATH
```

Static mode parses rendered Compose. Runtime mode uses `podman inspect`, `/proc/1/status`, mount info, network/listener inspection, and `podman stats --no-stream`; it verifies image lock/candidate IDs and writes Phase 1 evidence. Unknown flags or a missing container are failures, not skips.

- [ ] **Step 7: Boot an isolated full stack and inspect it**

Use the clean-checkout harness with unique networks/volumes and complete test integrations. Run:

```bash
bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env
bash scripts/verify-runtime-hardening.sh \
  --compose docker-compose.yml --env-file tests/fixtures/compose.env \
  --policy config/runtime-hardening.json
bash tests/operations/runtime_hardening_test.sh
```

Then run live mode against the isolated stack/candidate test lock. Expected: every service is healthy and matches effective policy; teardown removes all test resources.

- [ ] **Step 8: Commit runtime hardening**

```bash
git add config/runtime-hardening.json docker-compose.yml config/traefik.yaml \
  backend/Dockerfile frontend/Dockerfile deploy/egress-proxy \
  scripts/verify-runtime-hardening.sh tests/operations/runtime_hardening_test.sh \
  documentation/operations/container-hardening.md cli/lib/preflight.sh
git commit -m "deploy: enforce effective container hardening"
```

---

### Task 3: Make monitoring real, internal, and privacy-bounded

**Files:**
- Create: `backend/metrics_server.go`
- Create: `backend/metrics_server_test.go`
- Modify: `backend/metrics.go`
- Modify: `backend/main.go`
- Modify: `backend/server.go`
- Modify: `docker-compose.yml`
- Modify: `config/prometheus/prometheus.yml`
- Modify: `config/prometheus/alerts.yml`
- Modify: `scripts/verify-monitoring.sh`
- Modify: `tests/operations/monitoring_test.sh`
- Modify: `documentation/operations/monitoring.md`
- Modify: `documentation/operations/incidents.md`

**Interfaces:**
- Produces: `/metrics` on internal port `9090`, never the public API/Traefik route.
- Produces: Prometheus scrape and alert-rule health plus a runtime evidence check.

- [ ] **Step 1: Write metrics lifecycle/privacy tests**

Test a separate listener starts on an injected loopback address, serves only `/metrics`, rejects other paths, stops within the server shutdown budget, and never appears in the public gateway mux. Add bounded metric tests for request count/duration by method/status class (no raw path), readiness dependencies by fixed service label, active sockets, outbox size, auth throttling, backup age, and last backup result. Backup status is read from an atomically written Prometheus textfile beneath `TELOS_OPS_STATE_PATH`, mounted read-only into core; backup code never calls a public metrics endpoint. Reject source labels containing username, user ID, channel, item, query, host, token, or arbitrary path.

- [ ] **Step 2: Replace monitoring existence checks**

`tests/operations/monitoring_test.sh` must run `promtool check config`/`check rules` using the locked Prometheus image, render Compose, prove no metrics port is published/routed, and use fixture Prometheus API responses to test `verify-monitoring.sh` failure on target down, stale scrape, firing critical alert, and missing series.

- [ ] **Step 3: Run tests and observe the missing listener/nonsensical alert**

```bash
bash tests/operations/monitoring_test.sh
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 \
  go test ./... -run 'TestMetricsServer|TestMetricsPrivacy'
```

Expected: nonzero; no port 9090 listener is started and the dependency alert cannot fire meaningfully.

- [ ] **Step 4: Implement internal metrics lifecycle**

Start a second `http.Server` on `METRICS_ADDR=:9090` after core dependencies initialize, expose only metrics, and shut it down with the main server. In production accept connections only on the internal observability network. Instrument fixed-cardinality request/readiness signals without PII and register a bounded collector that parses only the two declared backup metrics from `${TELOS_OPS_STATE_PATH}/last-backup.prom`; malformed/stale files yield a failure metric, never arbitrary labels.

- [ ] **Step 5: Add an internal Prometheus service**

Create `telos-observability` as an internal network joined only by core and Prometheus. Add a digest-pinned, non-root, hardened `telos-prometheus` service with no host port, read-only config, bounded writable data volume, health check, limits, and a 15-day retention bound. The operator accesses status through `telos status`/the verifier, not a public Prometheus UI.

- [ ] **Step 6: Replace alert rules**

Add valid rules for scrape down, readiness dependency down, elevated 5xx ratio with minimum traffic, outbox backlog, authentication throttling surge, backup failure, and backup age over 24 hours. Every alert has severity and a repository runbook anchor. Remove `telos_active_sockets < 0`.

- [ ] **Step 7: Implement runtime verification**

`verify-monitoring.sh --runtime --candidate-lock PATH --evidence-out PATH` runs a temporary locked probe on `telos-observability`, checks Prometheus target/rule APIs and required series freshness, and fails on any firing critical alert. `--source` performs config/privacy/routing checks and is a local gate.

- [ ] **Step 8: Run monitoring checks**

```bash
bash tests/operations/monitoring_test.sh
bash scripts/verify-monitoring.sh --source
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env
```

Expected: all exit `0` and no metrics/Prometheus port is public.

- [ ] **Step 9: Commit monitoring**

```bash
git add backend/metrics_server.go backend/metrics_server_test.go backend/metrics.go \
  backend/main.go backend/server.go docker-compose.yml config/prometheus \
  scripts/verify-monitoring.sh tests/operations/monitoring_test.sh \
  documentation/operations/monitoring.md documentation/operations/incidents.md
git commit -m "ops: add internal beta monitoring and alerts"
```

---

### Task 4: Create encrypted, verified, retained off-node backups

**Files:**
- Modify: `scripts/backup.sh`
- Modify: `scripts/prune-backups.sh`
- Create: `scripts/lib/backup_manifest.py`
- Create: `release/backup-manifest.schema.json`
- Modify: `deploy/systemd/telos-backup.service`
- Modify: `deploy/systemd/telos-backup.timer`
- Modify: `tests/operations/backup_restore_test.sh`
- Modify: `scripts/tests/backup_test.sh`
- Modify: `documentation/operations/backup-and-restore.md`
- Modify: `documentation/operations/data-retention.md`
- Modify: `.env.example`
- Modify: `cli/commands/doctor.sh`

**Interfaces:**
- Produces: one restic snapshot tagged `scheduled`, `telos`, release ID, and candidate digest.
- Produces: a strict backup manifest inside the encrypted snapshot.
- Consumes: `RESTIC_REPOSITORY`, `RESTIC_PASSWORD_FILE`, optional backend credential file, candidate lock, and maintenance lock.

- [ ] **Step 1: Build a fake Podman/restic failure matrix**

Replace the backup existence test with stubs that record argv/stdin and model success plus failure at container validation, quiesce, PostgreSQL dump, MariaDB dump, config copy, storage archive, manifest generation, restic init/check/backup/snapshot verification, prune, and service restart. For every case assert:

- exit is nonzero at the failing step;
- plaintext staging path no longer exists;
- services stopped by the script are restarted if they were running initially;
- no later restic/prune success command ran;
- no secret appears in argv/stdout/stderr/evidence;
- no snapshot is called verified without matching snapshot ID/content;
- maintenance contention exits `75` before changing runtime state.

Also test SIGINT/SIGTERM cleanup through a controllable blocking stub.

- [ ] **Step 2: Run backup tests and expose the no-op cleanup**

```bash
bash tests/operations/backup_restore_test.sh
bash scripts/tests/backup_test.sh
```

Expected: nonzero because current plaintext destination survives exit and no encrypted upload occurs.

- [ ] **Step 3: Define the backup manifest**

Require format version, source/candidate/release identity, source machine-ID hash, creation start/end, schema version and migration checksum set, component names/sizes/SHA-256, restic tags, and consistency method. Required components are PostgreSQL custom dump, MariaDB dump, `.env`/secret material encrypted inside restic, Traefik ACME, Jellyfin config, Grimmory config, shared storage, release capsule, and migration manifest. Redis is explicitly excluded as rebuildable.

- [ ] **Step 4: Implement consistent collection and unconditional cleanup**

Acquire the maintenance lock. Validate candidate/restic configuration and off-node repository scheme. Create staging with:

```bash
stage="$(mktemp -d "${TELOS_BACKUP_TMP_ROOT:-/var/tmp}/telos-backup.XXXXXX")"
chmod 0700 "$stage"
cleanup() { rc=$?; restart_initial_services; find "$stage" -type f -exec chmod 600 {} + 2>/dev/null || true; rm -rf -- "$stage"; exit "$rc"; }
trap cleanup EXIT INT TERM HUP
```

Resolve and validate the explicit temp root before `rm -rf`; never allow `/`, `$HOME`, repository root, storage root, or release root. Enter maintenance mode and stop Traefik/core/Jellyfin/Grimmory writers, keep the static client and both databases available, produce consistent dumps/copies, generate hashes/manifest, then restart the stopped services before the potentially long upload.

- [ ] **Step 5: Upload and verify with restic**

Require restic repository initialized, run `restic backup "$stage" --tag scheduled --tag telos --tag "candidate:$candidate_lock_digest" --json`, parse the returned snapshot ID, verify tags/paths via `restic snapshots --json` and `restic ls --json`, run `restic check --read-data-subset=5%`, then prune with exact 14/8 policy. A check/prune failure makes the run failed, while the already-created snapshot remains available and is named in the error.

After verification, atomically write fixed-label last-success epoch/result metrics beneath `TELOS_OPS_STATE_PATH` for the internal collector. A failed run atomically records failure without erasing the last-success epoch. Remove `TELOS_KEEP_PLAINTEXT`. Do not print the repository credential environment.

- [ ] **Step 6: Make the timer call the encrypted workflow**

Use a systemd user service with `UMask=0077`, `NoNewPrivileges=true`, `PrivateTmp=true`, explicit environment-file paths, `OnCalendar=*-*-* 00,06,12,18:00`, `RandomizedDelaySec=30m`, persistent catch-up, and a 12-hour stale-success retry rule. The service invokes `backup.sh --candidate-lock /srv/telos/current/candidate-lock.json --evidence-out /var/lib/telos/ops/evidence/backup-latest.json`; it never invokes the old local directory interface.

- [ ] **Step 7: Add doctor and runbook checks**

Doctor validates restic version, repository scheme/reachability, password-file ownership/mode, timer enabled/last result, last successful snapshot age, free temp space, and no orphan plaintext directory. It prints safe corrective commands without secret values.

- [ ] **Step 8: Run backup/retention tests**

```bash
bash tests/operations/backup_restore_test.sh
bash scripts/tests/backup_test.sh
python3 -m py_compile scripts/lib/backup_manifest.py
systemd-analyze verify deploy/systemd/telos-backup.service deploy/systemd/telos-backup.timer
```

Expected: all exit `0`; fake failure matrix confirms no plaintext survives.

- [ ] **Step 9: Run one live off-node snapshot checkpoint**

Against a non-candidate reference stack and test off-node repository, create a snapshot, interrupt a second run during upload, verify plaintext cleanup, inspect encrypted repository contents through restic only, apply retention, and restore the first snapshot into a temporary directory for hash comparison. This proves mechanics but does not satisfy the separate-host candidate recovery gate.

- [ ] **Step 10: Commit encrypted backups**

```bash
git add scripts/backup.sh scripts/prune-backups.sh scripts/lib/backup_manifest.py \
  release/backup-manifest.schema.json deploy/systemd/telos-backup.service \
  deploy/systemd/telos-backup.timer tests/operations/backup_restore_test.sh \
  scripts/tests/backup_test.sh documentation/operations/backup-and-restore.md \
  documentation/operations/data-retention.md .env.example cli/commands/doctor.sh
git commit -m "ops: create verified encrypted off-node backups"
```

---

### Task 5: Prove destructive restore on a separate host

**Files:**
- Modify: `scripts/restore.sh`
- Modify: `scripts/verify-restore.sh`
- Modify: `scripts/verify-restored-node.sh`
- Modify: `scripts/restore-drill.sh`
- Modify: `scripts/drills/recovery.sh`
- Create: `backend/restore_sanitize.go`
- Create: `backend/restore_sanitize_integration_test.go`
- Modify: `backend/main.go`
- Modify: `docker-compose.yml`
- Modify: `tests/operations/backup_restore_test.sh`
- Modify: `scripts/tests/restore_test.sh`
- Modify: `documentation/operations/restore-drill.md`
- Modify: `documentation/operations/backup-and-restore.md`

**Interfaces:**
- Produces: `telos-core sanitize-restore` one-shot mode using the schema-owner connection.
- Produces: live `separate-host-restore` evidence bound to snapshot and candidate.
- Consumes: encrypted restic snapshot/manifest from Task 4 and HTTPS smoke from Phase 2.

- [ ] **Step 1: Add restore safety/adversarial tests**

Extend fake-runtime tests for missing confirmation, same machine ID, local repository, candidate mismatch, incomplete/tampered manifest, newer schema, migration checksum drift, insufficient disk, nonempty target without explicit recoverable preservation, DB import failure, storage extraction traversal/symlink/device file, sanitize failure, readiness failure, and external-probe mismatch. Assert no traffic starts before sanitize/readiness, old state is preserved under a validated rollback directory, and every partial state is journaled.

- [ ] **Step 2: Add transactional sanitization integration tests**

Seed active browser sessions, device credentials/access sessions, used/unused invites, users/content, and audit records. Run sanitization and assert all sessions/devices/access tokens are revoked, unused invites are invalidated, users/content remain, a restore audit event exists, and a second run is safe/idempotent. Force a SQL failure and assert the transaction changes nothing.

- [ ] **Step 3: Run tests and observe current same-host/plaintext assumptions**

```bash
bash tests/operations/backup_restore_test.sh
bash scripts/tests/restore_test.sh
bash scripts/test-backend.sh db
```

Expected: nonzero until encrypted snapshot retrieval, host separation, and sanitization exist.

- [ ] **Step 4: Implement `sanitize-restore` before normal startup**

Dispatch the one-shot command before serve initialization. Connect only through `DATABASE_OWNER_URL`, verify migrations, and transactionally revoke session/device/access state plus outstanding invites while preserving accounts/content. Compose exposes it only under a `restore` profile and no network beyond `telos-db`.

- [ ] **Step 5: Restore from restic into validated staging**

Expose:

```text
scripts/restore.sh --snapshot ID --candidate-lock PATH \
  --release-generation PATH --evidence-out PATH \
  --confirm RESTORE-TO-EMPTY-REFERENCE-HOST
```

Acquire the maintenance lock, require target host machine-ID hash differs from the manifest source, restore into a `0700` staging directory, verify every component hash/schema/candidate compatibility before stopping/changing services, reject unsafe tar members before extraction, journal each transition, preserve prior target state, restore DB/config/storage, run migrations only under declared compatibility rules, run sanitize, start services, and wait for internal readiness. Delete staging on all exits.

- [ ] **Step 6: Implement restored-node verification**

`verify-restored-node.sh` requires candidate lock, snapshot manifest, public HTTPS URL, an external browser-smoke envelope, and output evidence. It checks host/candidate/snapshot identities; readiness; password reauthentication; old cookie/device/invite rejection; Chat, Stream, Library, Files, commentary; range video/audio; and no unexpected public ports. Do not accept fixture probe statuses.

- [ ] **Step 7: Implement RPO/RTO drill aggregation**

Retain `--fixture` solely for timestamp policy tests, with status values aligned to Phase 1. Add live mode that computes:

```text
RPO = incidentDeclaredAt - snapshotFinishedAt
RTO = externalJourneyPassedAt - incidentDeclaredAt
```

Require RPO `<= 86400` and RTO `<= 14400`, distinct source/recovery machine hashes, restic snapshot ID, DNS/certificate verification, and external probe evidence. `scripts/drills/recovery.sh` delegates to live mode and cannot print standalone success.

- [ ] **Step 8: Run local recovery tests**

```bash
bash tests/operations/backup_restore_test.sh
bash scripts/tests/restore_test.sh
bash scripts/test-backend.sh db
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: all exit `0`; fixture drill cannot emit candidate `passed` evidence.

- [ ] **Step 9: Execute a live second-host restore checkpoint**

On a distinct clean Ubuntu reference host, restore the latest test snapshot from the off-node repository, complete DNS/trusted-certificate cutover, run the external browser journey from a third network location, and aggregate evidence. Expected: status `passed`, RPO at most 24 hours, RTO at most 4 hours. Preserve the host until evidence review completes.

- [ ] **Step 10: Commit recovery implementation**

```bash
git add scripts/restore.sh scripts/verify-restore.sh scripts/verify-restored-node.sh \
  scripts/restore-drill.sh scripts/drills/recovery.sh backend/restore_sanitize.go \
  backend/restore_sanitize_integration_test.go backend/main.go docker-compose.yml \
  tests/operations/backup_restore_test.sh scripts/tests/restore_test.sh \
  documentation/operations/restore-drill.md documentation/operations/backup-and-restore.md
git commit -m "ops: verify separate-host encrypted recovery"
```

---

### Task 6: Implement generation-safe upgrade and rollback

**Files:**
- Create: `scripts/lib/generations.py`
- Create: `release/generation-manifest.schema.json`
- Modify: `scripts/upgrade.sh`
- Modify: `scripts/rollback.sh`
- Modify: `scripts/build-predecessor.sh`
- Modify: `scripts/restore-state.sh`
- Modify: `scripts/drills/upgrade-rollback.sh`
- Modify: `tests/operations/upgrade_rollback_test.sh`
- Modify: `release/predecessor-policy.json`
- Modify: `documentation/operations/upgrade-and-rollback.md`
- Modify: `documentation/operations/release.md`

**Interfaces:**
- Produces: immutable generation layout and durable operation journal.
- Consumes: a verified generation from Phase 5's builder, image/candidate locks, and Task 4 backup command.
- Produces external gate: `upgrade-rollback`.

- [ ] **Step 1: Replace upgrade existence tests with state-machine tests**

Model a temporary release root with `generations/`, `current` symlink, `state/operation.json`, candidate/predecessor manifests, and stub backup/Compose/health commands. Cover successful upgrade, lock contention, unverified generation, wrong predecessor, backup failure, migration failure before activation, interruption at every journal state, readiness failure after activation, compatible binary rollback, incompatible schema refusing binary rollback and requiring restore, second invocation recovery, symlink/path escape, dirty generation, and evidence mismatch.

- [ ] **Step 2: Run tests and expose success-only scripts**

```bash
bash tests/operations/upgrade_rollback_test.sh
```

Expected: nonzero because upgrade/rollback/predecessor scripts do not perform their claims.

- [ ] **Step 3: Define generation identity/compatibility**

The manifest requires release ID/version, source commit, candidate-lock digest, artifact/image digests, migration min/current/max, migration checksum-set digest, predecessor IDs, rollback-compatible-through schema, creation time, and file inventory. Generation directories are mode `0755`, immutable to the service user, and addressed by candidate digest. `current` is the only mutable pointer.

Update predecessor policy to identify an actually buildable tagged/committed predecessor and its exact candidate/release digest; never leave a version-only assertion.

- [ ] **Step 4: Implement generation/journal primitives**

`generations.py` validates paths beneath the configured release root, verifies manifest/file digests, fsyncs directory/file changes, atomically swaps symlinks with `rename`, and writes a closed journal containing operation ID, from/to generations, snapshot ID, pre/post schema, state, timestamps, and last error. Recovery resumes or safely rolls back based on state; it never guesses after migration.

- [ ] **Step 5: Implement upgrade**

Expose:

```text
scripts/upgrade.sh --generation PATH --current-candidate-lock PATH \
  --evidence-out PATH --confirm UPGRADE-THIS-NODE
```

Acquire the maintenance lock, recover prior journal, verify source/current/predecessor compatibility, create and verify encrypted pre-upgrade snapshot, enter maintenance, stop services, run one-shot migrations from target generation, atomically activate, start exact target images, wait for readiness and HTTPS smoke, then mark complete. A pre-migration failure leaves current active. A post-migration failure follows the declared compatibility branch.

- [ ] **Step 6: Implement rollback**

Fast rollback activates the prior generation only when its manifest accepts the current schema/checksum set. Otherwise require the recorded encrypted snapshot and call the verified restore path. Both paths retain the failed generation and journal for investigation; neither deletes a release automatically.

- [ ] **Step 7: Build and verify the predecessor**

`build-predecessor.sh` checks out/exports the exact predecessor source in a temporary directory, verifies its signed source identity, builds its generation without touching the current worktree, validates it, and outputs its immutable path/digest. It exits `not_run` when the policy does not name an obtainable exact source/artifact.

- [ ] **Step 8: Run local lifecycle tests**

```bash
bash tests/operations/upgrade_rollback_test.sh
python3 -m py_compile scripts/lib/generations.py
bash -n scripts/upgrade.sh scripts/rollback.sh scripts/build-predecessor.sh \
  scripts/restore-state.sh scripts/drills/upgrade-rollback.sh
```

Expected: all exit `0`; each injected interruption recovers deterministically.

- [ ] **Step 9: Run a live predecessor checkpoint**

On a reference node containing representative data, install the exact predecessor, take a verified snapshot, upgrade to the test generation, run browser/native probes, execute compatible rollback if policy permits, re-upgrade, then exercise the incompatible-schema restore branch in an isolated clone. Expected: all intended branches produce source-bound `passed` records; an unavailable genuine predecessor is `not_run` and blocks final certification.

- [ ] **Step 10: Commit lifecycle safety**

```bash
git add scripts/lib/generations.py release/generation-manifest.schema.json \
  scripts/upgrade.sh scripts/rollback.sh scripts/build-predecessor.sh \
  scripts/restore-state.sh scripts/drills/upgrade-rollback.sh \
  tests/operations/upgrade_rollback_test.sh release/predecessor-policy.json \
  documentation/operations/upgrade-and-rollback.md documentation/operations/release.md
git commit -m "ops: make upgrade and rollback generation-safe"
```

---

### Task 7: Implement real capacity, exposure, and controlled-egress drills

**Files:**
- Modify: `tests/load/telos.js`
- Create: `scripts/prepare-capacity-fixture.py`
- Create: `scripts/collect-runtime-stats.py`
- Modify: `scripts/run-capacity-drill.sh`
- Modify: `scripts/audit-host-exposure.sh`
- Modify: `scripts/tests/egress_test.sh`
- Modify: `tests/operations/capacity_exposure_test.sh`
- Create: `config/exposure-policy.json`
- Modify: `documentation/operations/capacity.md`
- Modify: `documentation/operations/network-exposure.md`
- Modify: `documentation/operations/controlled-egress.md`

**Interfaces:**
- Produces external gates: `capacity`, `host-exposure`, `controlled-egress`.
- Consumes: candidate server URL/lock, 100 disposable test accounts, locked k6/probe images, runtime policy.

- [ ] **Step 1: Replace existence checks with command/result tests**

Use fake k6, Podman, `ss`, scanner, proxy, and evidence tools. Assert scripts reject missing candidate/URL/accounts, HTTP URL, non-reference host, setup overlay, insufficient account count, wrong load thresholds, missing resource samples, unexpected port/network, direct egress success, denied-host proxy success, stale runtime identity, and malformed k6 summary. Unknown flags fail. A missing external scanner/load host exits `3` with `not_run` evidence.

- [ ] **Step 2: Run tests and expose empty scenarios/false successes**

```bash
bash tests/operations/capacity_exposure_test.sh
```

Expected: nonzero because the current k6 default function is empty and both scripts print completion.

- [ ] **Step 3: Build the 100-member disposable fixture safely**

`prepare-capacity-fixture.py` accepts URL/candidate lock, reads owner credential from a protected file descriptor, creates 100 invite/member accounts through public APIs, writes mode-`0600` credentials under a caller-supplied external temp directory, and records cleanup IDs. It never prints passwords/invites. Refuse use against a node lacking the explicit `capacity-test` marker in its health metadata.

- [ ] **Step 4: Implement k6 scenarios**

Use separate scenarios:

```javascript
export const options = {
  scenarios: {
    member_journeys: { executor: "constant-vus", vus: 25, duration: "30m", exec: "memberJourney" },
    chat_commentary: {
      executor: "constant-arrival-rate", rate: 10, timeUnit: "1s",
      duration: "30m", preAllocatedVUs: 25, maxVUs: 50, exec: "eventWrite"
    }
  },
  thresholds: {
    http_req_failed: ["rate<0.01"],
    http_req_duration: ["p(95)<750"],
    "http_req_duration{kind:mutation}": ["p(95)<1000"]
  }
};
```

Distribute all 100 accounts, keep exactly 25 concurrent authenticated users, cover Chat/Stream/Library/Files/commentary reads, alternate chat/commentary writes at 10 aggregate events/s, and verify response semantics—not just status. Do not stream full media bodies during every iteration; include bounded range requests.

- [ ] **Step 5: Implement capacity orchestration and stats**

`run-capacity-drill.sh` validates host/runtime/candidate, starts stats collection at 5-second intervals, runs locked k6 from a separate load host/network, parses `summary-export`, and requires: thresholds pass; no container restart/OOM/health loss; CPU under 80% sustained and memory under 85% of limit; disk free above reserve; database pool/Redis/stream limits not saturated for over 60 seconds; final browser smoke passes. Evidence hashes raw k6/stats logs but excludes credentials.

- [ ] **Step 6: Define and audit exposure**

`config/exposure-policy.json` distinguishes Telos public TCP 80/443, operator-declared host-management ports, zero public UDP Telos ports, internal listeners/networks, and controlled proxy egress. `audit-host-exposure.sh` combines local `ss`/Podman/network inspection with an external full TCP/UDP scanner record. It fails if any Telos service except Traefik publishes, setup ports listen, metrics/databases/upstreams are reachable, Traefik lacks only 80/443, or external results disagree. Never label an explicitly declared SSH port as a Telos port.

- [ ] **Step 7: Prove controlled egress from each service namespace**

Keep static Squid parse checks. Live mode uses a locked probe sharing each applicable container's network namespace to prove direct Internet access fails, approved HTTPS through the proxy succeeds, unlisted domain/IP/private/link-local/alternate-port requests fail, and proxy logs redact queries. A missing engine/network is `not_run`, not a static pass. Bind results to runtime image/network IDs.

- [ ] **Step 8: Run local fixture/static tests**

```bash
bash tests/operations/capacity_exposure_test.sh
bash scripts/tests/egress_test.sh
node --check tests/load/telos.js
python3 -m py_compile scripts/prepare-capacity-fixture.py scripts/collect-runtime-stats.py
bash scripts/audit-host-exposure.sh --source --policy config/exposure-policy.json
```

Expected: all local/static commands exit `0`; live modes without environments exit `3`.

- [ ] **Step 9: Run live operational drills**

Against the same test candidate/reference host, run egress, external exposure, then capacity. Do not run backup/upgrade concurrently. Expected: three candidate/runtime-bound `passed` envelopes and post-drill browser smoke success. Delete disposable users with the cleanup manifest afterward and record cleanup separately.

- [ ] **Step 10: Commit operational drills**

```bash
git add tests/load/telos.js scripts/prepare-capacity-fixture.py \
  scripts/collect-runtime-stats.py scripts/run-capacity-drill.sh \
  scripts/audit-host-exposure.sh scripts/tests/egress_test.sh \
  tests/operations/capacity_exposure_test.sh config/exposure-policy.json \
  documentation/operations/capacity.md documentation/operations/network-exposure.md \
  documentation/operations/controlled-egress.md
git commit -m "ops: run real capacity exposure and egress drills"
```

---

### Task 8: Gate SBOM, vulnerabilities, secrets, and licenses

**Files:**
- Modify: `ci/tools.lock`
- Modify: `ci/license-policy.json`
- Modify: `ci/risk-acceptance.schema.json`
- Create: `ci/risk-acceptances.json`
- Create: `scripts/bootstrap-ci-tools.py`
- Create: `scripts/generate-sbom.sh`
- Create: `scripts/scan-candidate.sh`
- Modify: `scripts/ci/validate-sbom.sh`
- Create: `tests/operations/supply_chain_test.sh`
- Modify: `.github/workflows/ci.yml`
- Modify: `ci/phase-gates.json`
- Modify: `documentation/operations/dependency-security-baseline.md`

**Interfaces:**
- Produces: candidate-bound SPDX JSON SBOMs, vulnerability results, secret scan, and scoped license report.
- Consumes: image/tool locks and later candidate lock.

- [ ] **Step 1: Add tool/policy/result mutation tests**

Test missing tool digest, wrong downloaded digest, unlisted tool, malformed/expired/overbroad risk acceptance, Critical vulnerability, unaccepted High vulnerability, accepted exact High CVE+package+version before expiry, forbidden linked license, missing license, isolated GPL service falsely classified as linked, secret fixture detection, SBOM missing an artifact, and result bound to another candidate. The validator must reject a report created before the artifact timestamp or with a different scanner DB digest.

- [ ] **Step 2: Run tests and expose existence-only SBOM validation**

```bash
bash tests/operations/supply_chain_test.sh
```

Expected: nonzero because current validator only prints success.

- [ ] **Step 3: Lock tools by version, platform, URL, and SHA-256**

Upgrade reviewed Syft, Trivy, and gitleaks releases and record exact archive URL/digest per supported CI platform. Add any required `cargo-deny`/Go license tool similarly. `bootstrap-ci-tools.py` downloads to a mode-restricted external cache, verifies digest before extraction/execution, and never falls back to PATH unless `--verify-system-tool` confirms exact version and binary digest.

- [ ] **Step 4: Make license policy reflect the process boundary**

Define `linkedArtifacts` for Go core, browser JS, and Rust desktop bundles with allowed SPDX expressions. Define Jellyfin/Grimmory and infrastructure images as `isolatedServices`: their licenses must be inventoried/credited and their code must not appear in linked dependency graphs, but GPL/AGPL inside those separate images is not mislabeled as a Telos linking violation. Any forbidden linked dependency fails.

- [ ] **Step 5: Generate one SBOM per immutable artifact**

`generate-sbom.sh --candidate-lock PATH --out-dir PATH` verifies artifact digests first, runs locked Syft against core/client OCI archives and each desktop package, captures Go/npm/Cargo dependency lock digests, emits SPDX JSON with deterministic filenames, and writes an index mapping artifact digest to SBOM digest. It does not scan a mutable tag.

- [ ] **Step 6: Implement candidate scanning**

`scan-candidate.sh` verifies tools/locks, runs gitleaks against the exact source tree, Trivy against each OCI archive/SBOM/package, evaluates licenses, and validates risk acceptances. Policy: any secret or Critical fails; High fails unless an acceptance names exact CVE, package, version, artifact, owner, rationale, issue URL, creation, and unexpired expiry at most 30 days. Empty `risk-acceptances.json` is valid.

- [ ] **Step 7: Make SBOM validation semantic**

`scripts/ci/validate-sbom.sh --candidate-lock PATH --sbom-dir PATH` checks schema, coverage, package uniqueness, artifact/SBOM digests, creation/tool/database identities, and candidate binding. Remove directory-existence success.

- [ ] **Step 8: Run supply-chain tests and a source scan**

```bash
bash tests/operations/supply_chain_test.sh
python3 -m py_compile scripts/bootstrap-ci-tools.py
bash scripts/check-product-truth.sh
bash scripts/verify-release-pins.sh
```

Then generate/scan test artifacts from the Phase 4 source lock. Expected: all policies pass or produce a concrete reviewed blocking report; do not weaken policy to clear a finding.

- [ ] **Step 9: Wire required CI gates**

Add tool-lock, image-lock, secret, dependency, source-license, and operations fixture jobs with `contents: read`, no broad token, and no `continue-on-error`. Candidate artifact SBOM/vulnerability gates remain Phase 5 release jobs because ordinary PR CI has no final artifacts.

- [ ] **Step 10: Commit supply-chain gates**

```bash
git add ci/tools.lock ci/license-policy.json ci/risk-acceptance.schema.json \
  ci/risk-acceptances.json scripts/bootstrap-ci-tools.py scripts/generate-sbom.sh \
  scripts/scan-candidate.sh scripts/ci/validate-sbom.sh \
  tests/operations/supply_chain_test.sh .github/workflows/ci.yml ci/phase-gates.json \
  documentation/operations/dependency-security-baseline.md
git commit -m "ci: gate immutable artifacts on supply-chain policy"
```

---

### Task 9: Run and review the Phase 4 exit gate

**Files:**
- Modify: `ci/phase-gates.json`
- Modify: `documentation/product/beta-feature-status.md`
- Modify: `scripts/check-product-truth.sh`
- Modify: `scripts/verify-clean-checkout.sh` — add all new release/runtime-critical Phase 4 inputs to its explicit inventory.

**Interfaces:**
- Produces local gates: `image-lock`, `runtime-hardening-static`, `monitoring-source`, `backup-fixtures`, `restore-fixtures`, `upgrade-rollback-fixtures`, `capacity-exposure-fixtures`, `supply-chain-policy`.
- Produces external gates: `runtime-hardening`, `monitoring`, `encrypted-backup`, `separate-host-restore`, `upgrade-rollback`, `capacity`, `host-exposure`, `controlled-egress`, `candidate-supply-chain`.

- [ ] **Step 1: Require the complete operational inventory structurally**

Extend product/clean-checkout checks for image/tool/policy locks, backup/generation manifests, systemd units, operation scripts, external gate IDs, and absence of success-only script bodies. Mark backups shipping only after a reviewed live snapshot and separate-host restore pass; before then keep the matrix blocked.

- [ ] **Step 2: Run the complete local Phase 4 suite**

```bash
bash tests/operations/image_lock_test.sh
bash tests/operations/runtime_hardening_test.sh
bash tests/operations/monitoring_test.sh
bash tests/operations/backup_restore_test.sh
bash scripts/tests/backup_test.sh
bash scripts/tests/restore_test.sh
bash tests/operations/upgrade_rollback_test.sh
bash tests/operations/capacity_exposure_test.sh
bash tests/operations/supply_chain_test.sh
bash scripts/verify-release-pins.sh
bash scripts/verify-monitoring.sh --source
bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env
bash scripts/check-product-truth.sh
bash scripts/check-release-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only
bash scripts/test-backend.sh
```

Expected: every local command exits `0`; all live commands without their environments return `3`/`not_run` rather than success.

- [ ] **Step 3: Execute live gates against one Phase 4 test generation**

In order, verify image/runtime hardening and monitoring, take encrypted backup, perform separate-host restore, run predecessor upgrade/rollback, run controlled egress/exposure, then capacity. Use the same source/test-generation digest everywhere and stop on the first failure. Expected: all nine external Phase 4 envelopes are `passed` before review.

- [ ] **Step 4: Review recovery and security evidence**

Review raw log digests, host identities, snapshot ID, RPO/RTO calculation, generation journal, runtime IDs, port scan, proxy results, k6 thresholds/stats, SBOM coverage, vulnerability database/tool identities, and license boundary. Reject stale or manually edited evidence.

- [ ] **Step 5: Update product status and commit enforcement**

Only after the live review, update backup/recovery/operations claims. Then:

```bash
git add ci/phase-gates.json documentation/product/beta-feature-status.md \
  scripts/check-product-truth.sh scripts/verify-clean-checkout.sh
git commit -m "ops: enforce the standalone beta safety envelope"
```

- [ ] **Step 6: Record the review checkpoint**

Request code review focused on cleanup traps, destructive target validation, restore sanitization, schema compatibility, effective privileges, external evidence binding, and accepted vulnerability scope before freezing a candidate in Phase 5.
