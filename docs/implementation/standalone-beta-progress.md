# Standalone beta: implementation progress ledger

Resume file for the [cold handoff](../superpowers/plans/2026-10-13-standalone-beta-cold-handoff.md).
One entry per packet, newest last. States are never merged: **implemented** (source
exists), **locally verified** (focused + phase regression commands exited 0),
**rehearsed** (disposable infrastructure, `recordKind=rehearsal`), **candidate
certified** (frozen lock, fresh external evidence). Nothing below is candidate
certified.

## Ground rules in force

- Baseline commit `1455686bd6a633d45d40957a6096eadd7f1b9019` (branch
  `standalone-beta-impl`) preserves the reviewed platform-policy and plan
  remediation that sat uncommitted on `237692fc5da5654f95d1582cd1789b6491d22283`.
  It was reconstructed from `telos-review/implementation-handoff.patch` in a scratch
  worktree and byte-compared against the working tree for every file not touched by
  later packets. `main` is untouched.
- Server scope is Ubuntu 24.04+ only; initial clients are browsers and Linux desktop.
- Missing live inputs are `unavailable`, never `passed`. Rehearsal and candidate
  evidence stay separate.

## Host inventory (2026-10-13)

| Tool | Result |
|---|---|
| OS | Ubuntu 26.04.1 LTS x86_64 (not the 24.04 reference host) |
| Python / Bash / PyYAML | 3.14.4 / 5.3.9 / 6.0.3 |
| Node / npm | 24.18.0 / 11.16.0, user-local tarball, SHA-256 checked against `SHASUMS256.txt`, at `~/.local/telos-toolchain/node` |
| Go | 1.26.5 linux/amd64 user-local tarball, SHA-256 checked against go.dev, at `~/.local/telos-toolchain/go-root` (stands in for the pinned `golang:1.26.5` image because Podman is absent) |
| ripgrep | 14.1.1 musl, checksum verified; required by `tests/operations/product_truth_test.sh` |
| cargo / rustc | 1.93.1 (presence alone is not a native-client pass) |
| podman / podman-compose / docker | **absent**; no passwordless sudo, so Podman 5+ cannot be provisioned |
| Playwright browsers | not installed |
| Secret Service, second host, off-node restic, trusted domain, signing identities | not available |

---

## P1 — truth and gates reconciliation (Tasks 1–6)

**Task/subpart:** P1-T1…T6. Tasks 1–2 verified and preserved; T3 TypeScript argv + `platform-scope` gate; T4 checker
enforcement; T5 verified; T6 CI contract and inventory.
**Baseline commit and working-tree exceptions:** `1455686`; after it the tree held only this packet's edits.
**Implemented files and interface:**
- `ci/phase-gates.json` — `frontend-types` now `node frontend/node_modules/typescript/bin/tsc --project frontend/tsconfig.json --noEmit`
  (the old `npx --prefix frontend tsc` printed compiler help and exited 0 from the repo root); new required local
  phase-1 `platform-scope` source gate (`python3 tests/operations/platform_scope_test.py`, 120 s) that
  `beta-certification` lists.
- `tests/operations/frontend_types_gate_test.sh` — loads the catalog argv, swaps only `--project`, requires exit 0 on valid
  and a real `TS2322` on invalid TypeScript; never installs packages.
- `scripts/check-product-truth.sh` + `platform_scope_test.py` — six new mutation tests (gate removed, optional, moved out of
  phase 1/local, certification dependency dropped, hollow command, wrong subject/prerequisite) all fail with a diagnostic
  naming `platform-scope`.
- `.github/workflows/ci.yml` + `tests/operations/ci_workflow_contract_test.py` (PyYAML) — added `go vet`, the disposable
  PG/Redis integration job, Compose rendering with provider + PyYAML setup, explicit TypeScript check, Node 24.18.0 setup
  and `npm ci` before every Node job, a managed Playwright job (Chromium + WebKit, 60 min budget) and Node/ripgrep setup in
  `truth-and-gates`. The contract states every job/step independently and has mutation tests for removal, `if`,
  `continue-on-error`, shell overrides, reordering setup, wrong Node version, bare Playwright and short timeouts. The legacy
  line-level `truth-and-gates` validator and all of its mutations are preserved.
- `scripts/verify-clean-checkout.sh` inventory + `ci_gates_test.sh` — seven new required paths asserted exactly once.
- `tests/operations/product_truth_test.sh` — fails early with a clear message if `node` or `rg` is missing (it silently
  depended on ripgrep).
**Checks (host Node 24.18.0 / Go 1.26.5 user-local; exit 0 unless noted):** envelope, wrappers, `ci_gates_test.sh`,
`accumulated_suite_test.sh`, `platform_scope_test.py` (12 tests), `product_truth_test.sh`, `frontend_types_gate_test.sh`,
product/release truth, inventory, both hygiene checks, `git diff --check`; backend `go test ./...`, `go vet ./...`,
headless build, `genopenapi --check` (115 routes); frontend lint, `tsc`, vitest (43 files / 207 tests), build, bundle budget.
Logs and digests: `telos-review/beta-evidence/index.jsonl`.
**Discovered and fixed while making Playwright real (all pre-existing, red at baseline: 56 passed / 38 failed):**
1. Stream home never loaded items for any library after the member-experience refactor (`a966528` dropped the per-library
   prefetch) — rows stayed on `…`. `stream/page.tsx` now fetches each library's first level once the list is ready and after
   a rescan clears it; `stream-refresh.spec.ts` mock updated to the real `/media/items` vs `?parentId=` shape.
2. Settings sections were unreachable below 900 px (the rail is hidden and the old `.setnav` was never replaced) — members on
   phones could not reach Security, Credits or any admin section. Added a visible-below-900px section selector.
3. Login submit now reads the input values, not only React state (autofill / pre-hydration input), and the login specs wait
   for hydration before filling (cold `next dev` compiles wiped just-filled controlled inputs in WebKit).
**Result:** `python3 scripts/run-playwright-gate.py --playwright-timeout-seconds 1650` exits 0 — 93 passed, 0 failed,
180 skipped. Chromium, Pixel 7 and iPhone 13 (WebKit 26.5) projects. **Caveats:** the 180 skips are specs that need live
credentials/stack (`E2E_*`), so this is not the live journey proof; WebKit needed user-local copies of six Ubuntu libraries
under the Playwright bundle's `sys/lib` slot and `PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS=1` (no GStreamer libav, so no
codec-dependent media). Automated WebKit never substitutes for desktop/iOS Safari.
**Deviation recorded (needs owner decision, not silently resolved):** the plan asks CI to run
`npm run check:a11y-evidence` after build. That script is an intentional fail-closed stub that always exits 3 (`not_run`) —
candidate-run WCAG evidence cannot exist in CI — so a CI step would be permanently red. It is omitted from CI, and the
contract test pins the stub as fail-closed (exit 3, `not_run`). **No plan task owns turning `frontend-accessibility` into
a real validator, yet it gates `frontend-quality` and `beta-certification`.** Candidate certification cannot pass until a
signed accessibility record from the browser-matrix guided assertions replaces it (carried to Phase 5 Task 3).
**External status:** not needed. **Candidate status:** not frozen.
**Known failures / downstream:** none in P1 sources. Catalog gates that shell out to `podman` (backend-*, compose-validation,
api drift, operator CLI builds) cannot run through the runner on this host (no Podman); the same commands ran directly with
host Go. Playwright login/WebKit race fixed in tests, not by retries.
**Commit(s):** see `git log --oneline standalone-beta-impl`.
**Next task and first command:** P5-T1a — `python3 -m unittest tests/operations/release_graph_test.py` (red first).

## P5-T1a / P5-T4a — release graph, trust anchors, catalog v2 runner

**Task/subpart:** P5-T1a (schemas, canonical graph, trust tooling) and P5-T4a.1–4a.6 (catalog v2, producer plumbing, subjects,
tool allowlist, prior-evidence reuse); 4a.7 verified. Task 1b (real key enrollment) is deliberately not done.
**Baseline commit and working-tree exceptions:** `a7f27b1`; tree held only this packet.
**Implemented files and interface:**
- `scripts/lib/schema_check.py` — fail-closed JSON-Schema subset (unsupported keywords are rejected, not ignored).
- `release/*.schema.json` — 12 closed schemas: artifact-index, release-manifest, candidate-lock, generation-manifest,
  installed-generation, artifact-identity, gate-result, observed-subject (runtime/external-host/artifact), guided-smoke,
  evidence-index, certification, release-review. `release/hash-graph.md` rewritten to the acyclic graph.
- `scripts/lib/release_graph.py` — canonical bytes (sorted keys, no floats/NaN/duplicates/BOM, one LF), `sha256:` vs raw digests,
  symlink-free path resolution, exclusive atomic writes, builders (`build-index|manifest|candidate-lock`), `verify`, `validate`,
  gate-result directory verification, subject↔result identity checks.
- `scripts/enroll-release-signer.py` + `scripts/lib/signing.py` — fingerprint-confirmed enrollment of ssh-ed25519/sk-ed25519
  public keys bound to purpose namespaces (`git`, `telos-beta-{evidence,certification,review}-v1`, `telos-guided-smoke-v1`).
  `release/telos-release.pub` removed; the fabricated-ellipsis `telos-tag-signers` entry removed; `--verify-files` exits 3
  until a reviewed key exists.
- `ci/phase-gates.json` is **schemaVersion 2** (61 gates + new `release-tooling`): `subjectSelector`, `maxAgeHours`
  (168 default; 72 runtime-hardening/host-exposure/separate-host-restore/upgrade-rollback; 24 candidate-supply-chain),
  `requiredPlatforms`. `image-lock` and `supply-chain-policy` are source gates; producer placeholders drop the legacy `-- name` tail.
- `scripts/run-gates.py` + `scripts/lib/gate_producers.py` — v1 stays parseable; v2 adds producer execution, result/subject
  verification (artifact bytes vs the frozen index, runtime manifest digests vs the frozen generation descriptor with config
  ID ≠ manifest digest accepted, host record hashes), closed `--inputs` manifest, `--prior-evidence-dir`, cross-scope
  prerequisites never executed, stop-after-first-required-failure, `beta-certification` unschedulable, fixture subject = output
  digest. `TELOS_GATE_ID` added to the shared contract.
- `scripts/lib/evidence.py` + envelope schema — tool allowlist extended by exactly the 13 agreed tools; envelope stays v2.
- `scripts/not-run.py` and 21 placeholder wrappers accept `--result-out` and take the gate id from the runner.
- `scripts/verify-frozen-artifact.py` — `static-client-image` / `desktop-builds` adapter (OCI digest, non-root, port, labels,
  forbidden content, ELF AppImage + identity sidecar; loads/runs but never builds; no podman ⇒ `not_run`).
**Checks (exit 0):** `release_graph_test.py` 41 tests, `signer_trust_test.py` 17, `gate_runner_v2_test.py` 54,
`frozen_artifact_test.py` 16, existing `run_gates_test.py` 10, `ci_gates_test.sh`, accumulated/wrapper/envelope/scope/contract
suites, product-truth mutations, inventory. Logs: `telos-review/beta-evidence/index.jsonl`.
**External status:** not needed (fixture ephemeral keys only). **Candidate status:** not frozen.
**Known gaps carried forward:** Task 1b real trust enrollment (needs operator public keys); OCI HTTP smoke is only exercised
with a fake `podman`; installed-receipt cross-check of runtime descriptors belongs to the Phase 4 collector.
**Next task and first command:** Phase 2 Task 1 — `bash tests/operations/static_client_test.sh` (red: no `frontend/Dockerfile`).

---

## P2 — clean Linux deployment (Tasks 1–7)

**Task/subpart:** P2-T1 static-client image, T2 Compose/Traefik route split, T3 node configuration CLI, T4 integration discovery,
T5 guided Jellyfin/Grimmory setup, T6 hosted-browser probe and outage rehearsal, T7 host policy, clean-install drill, runbooks and
catalog wiring. All locally verified except where "not run" is stated. Nothing is rehearsed on a real host.
**Baseline commits:** `264f934` → `6574070` (T1/T2) → `80c75bc` (T3) → `924c0b7` (T4) → `6c63c59` (T5) → `d720652` (T6) → `f3d6f5d`
(preflight policy) → the T7 commit that carries this entry.
**Implemented files and interface:**
- T1 `frontend/{Dockerfile,.dockerignore,nginx.conf}`: unprivileged nginx static export, no Node/source/secrets in the runtime stage.
- T2 `docker-compose.yml` `telos-client` (ingress network only, no ports/volumes/secrets); Traefik `telos-api` (priority 100, `/api/v1`)
  vs `telos-web` (priority 1) with CSP/HSTS middleware; `scripts/validate-compose.sh` checks `standalone-client`, `route-split`,
  `setup-overlay`; hermetic renderer `tests/helpers/compose_double/`. Product-truth checker moved to the post-transition wording.
- T3 `cli/lib/{config.sh,node_config.py}`, `init` (one-way; no `--force`; `--import-env`; `.config.lock`; orphan refusal; signal
  cleanup), `start` refuses before Compose, `doctor` per-defect repairs, `common.sh` Podman-only `compose` with an absolute pinned
  provider. Core runs `10001:10001`; `${TELOS_CONFIG_PATH}` mounts read-only at `/run/telos`. Values containing `${` are refused.
- T4 `backend/integration_config.go` — `validate-integrations`, fixed error vocabulary, no redirects/proxies.
- T5 `docker-compose.setup.yml` (loopback-only), `telos configure integrations [--setup]` (hidden prompts, stdin-only validation,
  atomic `set-many`, guaranteed teardown), `documentation/operations/guided-integrations.md`.
- T6 `scripts/smoke-browser-node.sh` + `scripts/lib/browser_node_smoke.py`, `scripts/rehearse-browser-node.sh`, fixture node
  `tests/helpers/fake_node_server.py`, `frontend/e2e/{hosted-node,module-outage}.spec.ts`.
- T7 `cli/lib/preflight.sh` — single host policy for `install.sh` and `telos doctor`: Ubuntu ≥ 24.04 (newer = pass + "not yet a
  verified combination"), x86-64, kernel ≥ 5.6, Podman ≥ 5.0.0 (rootful in production), podman-compose pin 1.6.0 (dev may warn),
  public 80/443 free or held by this node's own Traefik; derivatives/other distros/WSL/ARM/Docker-only refused with no best-effort
  path. `scripts/migrate-audiobooks.sh` uses the shared compose helper. New `scripts/lib/{drill_common,node_observe,clean_install_drill}.py`
  and a real `scripts/drills/clean-node-install.sh` (no target ⇒ `not_run`; legacy `--candidate-lock/--evidence-out` placeholder
  behavior preserved for the fail-closed-scripts gate). The collector is a fixed, read-only program piped over strict-host-key SSH;
  it reduces `node.env` to key names, placeholder flags and an explicit non-secret allowlist. 19 assertions listed in
  `documentation/operations/clean-install-evidence.md`. `documentation/operations/install.md` rewritten.
  Bytecode that earlier commits tracked by mistake is untracked and ignored.
**Checks (exit 0, logs in `telos-review/beta-evidence/index.jsonl`, ids `p2t*`):** `install_test.sh` (mutation-checked: 9/9 policy
mutations killed), `clean_install_drill_test.py` 25 tests (per-assertion mutation matrix, identity-before-action, verifier≠node,
ssh argv + stdin delivery through a shim, candidate/rehearsal modes), `node_config_test.sh`, `configure_integrations_test.sh`,
`runtime_hardening_test.sh` (also with real podman-compose 1.6.0 through a labeled local shim), `browser_node_fixtures_test.sh`
(with nginx 1.28.3), `static_client_config_test.sh`, `check-product-truth.sh`, inventory.
**Not run here (no Podman on this host):** `static_client_test.sh` (image build/run), `validate-compose.sh` against real Podman,
backend checks inside the pinned Go image (host Go 1.26.5 used instead), live Playwright specs (180 skipped), the hosted-node
spec (`E2E_REQUIRE_LIVE=1`), and every real-host drill.
**External status:** `clean-install` **not_run** — missing: Ubuntu 24.04 host with verified Podman 5+, public DNS + inbound 80/443,
a second (verifier) machine, legal media fixtures. **Candidate status:** none.
**Known gaps and risks:**
- No verified Podman 5 source for Ubuntu 24.04 is recorded (`install.md` says so); the podman-compose 1.6.0 wheel hash
  (`98e48af2…`, PyPI) is recorded, the venv recipe is unverified on 24.04.
- Grimmory `/api/v1/libraries` response shape is the plan's contract, not a live capture (the fixture is labeled SYNTHETIC).
- `node_observe.py` parses `podman inspect`/`volume ls`/`ss` as documented but was never run against Podman 5; a parse failure
  fails closed.
- `npm run check:a11y-evidence` is an intentional fail-closed stub with no owning task, so `frontend-accessibility` and
  `frontend-quality` cannot pass until an owner decides (Phase 5 guided-record validator is the candidate home).
**Next task and first command:** Phase 3 — read `docs/superpowers/plans/2026-08-20-standalone-beta-phase-3-desktop-client.md`; first
red test: device registration reachable without a prior session (`bash scripts/test-backend.sh auth`).

---

## P3 — native desktop client (Tasks 1–7)

**Task/subpart:** P3-T1 route table + public device registration + exact-origin CORS, T2 device-token auth lifecycle, T3 OS credential
adapter, T4 platform TLS trust (pinning removed), T5 revocable media capabilities, T6 unsigned Linux AppImage build + verifier,
T7 catalog wiring of the five Phase 3 gates. Implemented and verified; the AppImage and the five gates were built/run **inside the
Ubuntu 26.04 VM** (they need Podman and the WebKitGTK/Secret Service runtime).
**Baseline commits:** `0a7775e` → `79806c8` (+ `c180503`, `cf1405c`) → `987bf61` → `8a19f94` → `5158d0d`.
**Implemented files and interface:** `backend/routes.go` (`registerRoutes`/`gatewayHandler`), `POST /api/v1/auth/devices/register`,
`corsMiddleware` + `TELOS_ALLOWED_ORIGINS` (`tauri://localhost`, `http://tauri.localhost`), `frontend/src/lib/{deviceAuth,secureStorage,
connectionProfile,mediaLocator}.ts`, `frontend/src-tauri/src/credentials.rs` (keyring 4.1.6), `backend/media_authority.go`
(`playback-locator` / `stream-ticket`, 12 h capabilities, per-family keys, `no-store`), `scripts/{verify-desktop-artifact,
write-artifact-identity}.py`, `.github/workflows/desktop.yml`, `scripts/ci/desktop-gate.sh`; version `0.1.0-beta.1`.
**Checks:** backend unit + integration suites, 257+ frontend unit tests, `tsc`, lint, build, bundle budget, product-truth,
desktop artifact/workflow contract tests; the five Phase 3 gates (`device-registration-boundary`, `native-auth-state`,
`os-credential-adapter`, `platform-trusted-tls`, `token-safe-media`) **passed in the VM**; on this host they report `not_run`.
**External status:** `desktop-linux` certification **not_run** — needs a real Secret Service session outside the VM, a signed-off
artifact and a frozen candidate. **Candidate status:** none.
**Known gaps:** no native window rehearsal (`scripts/rehearse-desktop.sh` is referenced by the desktop-beta runbook and not written);
no signing/updater (explicitly out of scope for the unsigned beta).

## P4-T1 — image lock

`release/images.lock` schema v2 with real manifest and linux/amd64 digests resolved through skopeo on the reference VM; every Dockerfile
`FROM` and Compose image pinned `source@sha256:…`; `scripts/{refresh,verify}-image-lock.py`; `tests/operations/image_lock_test.sh`
(mutation-checked). Commit `ac6aff5`. The catalog gate `image-lock` runs `verify-image-lock.py --source`.

## VM-HOSTED REHEARSAL — real Podman 5.7 on Ubuntu 26.04 (not candidate evidence)

Two KVM VMs (node + verifier on a private link), rehearsal private CA trusted in the verifier's OS store, synthetic media. Everything
below is **rehearsal**; none of it is certification evidence.
**Passed live:** `install.sh`, `telos init`, `telos doctor`, guided `telos configure integrations --setup` against the pinned Jellyfin
10.11.11 / Grimmory v3.5.0 (driven through a PTY; the product command is unchanged), `telos start` of all 12 services, platform-trusted
HTTPS from the second machine, and the **hosted-browser member journey with both outage stages** (19 assertions: edge routes, owner
bootstrap/login/invite, member join, chat, HLS video + 197 KB segment through Jellyfin, ranged audio, EPUB, upload/download/comment/
delete, Jellyfin-only and Grimmory-only outages degrade only their module, both recover). Report archived under
`telos-review/beta-evidence/vm/`.
**Defects found only by running it, fixed with regression tests (commits `cac2044` … `27d5e46`):**
- guided-setup relay for `internal` networks, `--project-directory`, tmpfs ownership options, short image names (A3–A5, A13–A15);
- Postgres cannot start under `no-new-privileges` on 26.04 (AppArmor label split) — A16;
- Jellyfin Base URL never set, so the gateway reported Jellyfin degraded — A17 (read-only validation + final-step save,
  `jellyfin_restart_pending`);
- Jellyfin 10.11 ancestors omit the library, so the catalog authorized nothing — A18;
- `telos status`/`doctor` probed a loopback port production never publishes; doctor now also detects an ingress bridge with
  forwarding off (seen once after a restart: healthy containers, loopback fine, other machines time out);
- JSON bodies without a content type (login, bootstrap, invites, uploads, deletes, folders, comments, admin) — structural guard test;
- a 36-character base64 file id was taken for a channel UUID → 500 for members; Jellyfin/Grimmory outages surfaced as 404 instead of 503.
**Not covered by the rehearsal:** public DNS / Let's Encrypt, real Safari/iOS/Android, signing keys, a bare-metal host, 24.04.

## P4-T2 — runtime hardening (live-verified on the VM)

`config/runtime-hardening.json` (14 services, two profile-gated), `scripts/lib/runtime_hardening.py`, `scripts/verify-runtime-hardening.{py,sh}`
(static against the rendered Compose, `--runtime` against live containers through host `/proc`, `--target-file` over SSH), 28 mutation
tests plus a recorded real `inspect` fixture. Compose is hardened throughout: non-root users, `cap_drop: ALL`, read-only roots, tmpfs,
limits, bounded logs, Traefik on 8080/8443, Grimmory's JVM run directly (A19), ClamAV unprivileged, volumes created with their owner.
**Live:** 12/12 containers pass on real Podman 5.7. Documented limit: podman-compose 1.6.0 applies `stop_grace_period` only at `down`.
Exceptions: A16 (postgres/mariadb `no-new-privileges`, expires 2027-04-30). Gates `runtime-hardening-static` and `runtime-hardening` wired.

## P4-T3 — monitoring (live-verified on the VM)

Separate `:9090` listener (`GET /metrics` only, CIDR allowlist), request/dependency/outbox/socket/auth-throttle/backup metrics, the
`telos-observability` network, a hardened Prometheus v3.15.0 (no published port, 15 d / 1 GB), alert rules with runbook anchors,
`scripts/verify-monitoring.{py,sh}` (`--source` uses `promtool` extracted from the locked image; `--runtime` queries Prometheus).
**Live:** the runtime check passed on the node. Gates `monitoring-source` and `monitoring` wired.

## P4-T4 — encrypted off-node backups (live-verified on the VM)

`scripts/backup.sh` (restic to an off-node repository only; journaled stage; `SIGKILL` recovery; retention 14 d / 8 w; atomic status
textfile read by the gateway), `scripts/prune-backups.sh`, `scripts/lib/{maintenance.sh,backup_manifest.py}`, `release/backup-manifest.schema.json`,
systemd units. A fault-injection test (`tests/operations/backup_restore_test.sh`, mutation-checked) covers every step failing, signals,
contention and secret hygiene. **Live:** timer-driven backups verified a snapshot on the second VM through a restricted SFTP key.
Found live: the release capsule was 11 GB (A24); the writers died with the one-shot unit (A22).

## P4-T5 — separate-host restore and recovery drill (live-verified on the VM)

`backend/restore_sanitize.go` + `telos-core sanitize-restore` (one transaction, idempotent, refuses unmigrated/newer/drifted databases),
`scripts/restore.sh`, `scripts/lib/restore_check.py`, `scripts/lib/recovery_drill.py` (+ `node_observe.py restore`), `verify-restore.sh`,
`verify-restored-node.sh`, `restore-drill.sh --live`, `drills/recovery.sh`. **Live rehearsal (snapshot `80a6798a`, restored onto the
second VM, observed from the first):** 29 assertions passed — install contract, journal in order with sanitize before start, zero
pre-restore credentials live, the sanitizer's audit row, four stale credentials (cookie, device access/refresh, invite) found in the
restored database and refused over HTTPS (401/401/401/400), the owner's password still works, DNS and platform-trusted TLS, the
member journey on restored content, **RPO 33 s, RTO 223 s**. Artifacts: `telos-review/beta-evidence/vm/*separate-host-recovery*`.
Found live: restore as `telos_owner` failed on the extension (A23); a failed restore leaves a non-empty host that must be reset by hand.
**External status:** `separate-host-restore` is **not_run** for certification — needs a candidate, a real third observer machine (A25),
public DNS/certificate cutover and the stage controller for outages (A26). **Remaining in Task 5:** operator docs, gate wiring.

## P4-T6 — generation-safe upgrade and rollback (implemented, locally verified)

`scripts/lib/generations.py` (verify a generation against its closed `generation.json`, atomic `current` swap, installed receipt,
durable operation journal, `plan-recovery` as a pure function of journal + observed schema + readiness), `release/operation-journal.schema.json`
(+ the `restored` state), `scripts/lib/lifecycle.sh`, `scripts/upgrade.sh`, `scripts/rollback.sh`, `scripts/restore.sh --mode rollback`,
`scripts/restore-state.sh`, `telos migrate`, `scripts/lib/predecessor.py` + `release/predecessor-policy.json` (currently `null`, so
`scripts/build-predecessor.sh` reports `not_run`). Tests: `generations_test.py`, `predecessor_test.py`, `upgrade_rollback_test.sh` (a SIGKILL
at every journal step, mutation-checked) and the hermetic `upgrade_rollback_fixtures_test.sh` gate.
**Status:** implemented and locally verified. **No live upgrade has run:** there is no built predecessor and no builder yet (Phase 5 Task 2a),
so `upgrade-rollback` stays `not_run`; synthetic version history would not be proof.

## P4-T7 — capacity, host exposure, controlled egress (implemented; live rehearsals run)

`config/exposure-policy.json`, `scripts/lib/exposure_audit.py` + `audit-host-exposure.sh` (external nmap scan of the node: only 22/80/443),
`scripts/lib/egress_drill.py` + `drills/egress.sh` (locked curl probe image; 7 services direct-isolated, allowed destinations reach the proxy,
18 denied destinations refused, redaction verified), `scripts/lib/capacity_drill.py` + `run-capacity-drill.sh` + `prepare-capacity-fixture.py` +
`collect-runtime-stats.py` + `tests/load/telos.js` (25 virtual users from 26 source addresses, A27/A29), gateway gauges (A28).
**Defects found only by running it live, fixed with regression tests:** the edge login limiter was one global bucket
(Traefik `ipStrategy.depth: 1` without `X-Forwarded-For`), so 25 members shared one budget; Jellyfin 10.11.11 SIGSEGVs under roughly 18 or
more concurrent library, PlaybackInfo and audio requests (reproduced against Jellyfin directly) — reduced by the gateway start gate (A30),
not eliminated; several stale tests (doctor's container count, Compose network membership, hardening mutation, certification placeholders).

## P4-T8 — supply chain (implemented, locally verified; live scan pending)

`ci/tools.lock` v2 (gitleaks, syft, trivy with archive and binary digests, verified live), `scripts/bootstrap-ci-tools.py`, `ci/license-policy.json`
v2 (linked ecosystems versus isolated services), `ci/risk-acceptance.schema.json` + empty `ci/risk-acceptances.json`, `.gitleaks.toml`,
`scripts/lib/supply_chain.py`, `generate-sbom.sh`, `scan-candidate.sh`, `scripts/ci/validate-sbom.sh`, 47 unit tests, the CI jobs
`operations-fixtures` and `supply-chain-source`. `candidate-supply-chain` is `not_run` without a frozen candidate.

## P4-T7 (continued) — the capacity drill, three live runs, two product defects (rehearsal, not candidate evidence)

The 30-minute, 25-session capacity drill ran three times on the reference VMs. **Run 1** failed on stability, latency and semantics: Jellyfin
restarted (exit 139, SIGSEGV) and requests failed during the restart. I first blamed Jellyfin 10.11.11 and added a request start gate (A30),
which cut but did not remove the crashes. **A one-option-at-a-time differential test then found the cause:** the same Jellyfin image on a copy
of the real config under identical direct load was stable with a read-only root (0 restarts), dropped capabilities (0), memory/CPU/PID limits (0)
and jemalloc disabled, stable unhardened (0 restarts in 144,000 requests), and crash-looped only with `no-new-privileges` (49 restarts in 100 s).
It is the A16 mechanism (Ubuntu 26.04 AppArmor label split denies the .NET runtime's own signals), so Jellyfin now carries the same documented
exception (A33). **Run 2** then failed only on `capacity.semantics`: 46 of 7,200 playback-info requests answered 500. Logging the cause showed
`context canceled`: `jellyfinGET` cancelled the request's deadline context when it returned, before the caller read the body (A34; a regression
test reproduces it). **Run 3** (commit `d45b8d3`): every capacity assertion passed — 75,825 requests, 0 failed, p95 74 ms (mutation p95 53 ms),
12 containers with no restart, CPU/memory/storage inside bounds, 25 live sessions throughout, journey passes afterwards, fixture removed — and the
drill reports `not_run` only for `subject.runtime`: the rehearsal node has no installed-generation receipt, so the observation cannot be bound to
a generation. Artifacts: `telos-review/beta-evidence/vm/*capacity-rehearsal-{1,2,3}-*`. **Candidate status:** none.

## P4-T9 — Phase 4 exit gate (local)

`scripts/check-product-truth.sh` now enforces the operational inventory structurally (Phase 4 gate IDs/scopes/commands and their scripts, the
locks, schemas, policies, systemd units and operation scripts, absence of success-only script bodies), with five mutation tests; the status
document keeps implementation, rehearsal and candidate evidence apart and backup/recovery stay "not shipping until Phase 5 candidate evidence".
`verify-clean-checkout.sh` carries every Phase 4/5 path. **Known leftover:** the legacy full-stack smoke mode of `verify-clean-checkout.sh
--source-ref` still fails at compose validation (its generated `.env` predates the hardened model); the gate only uses `--inventory-only`, and the
real clean-host proof is the `clean-install` drill. Not repaired.

## P5 (continued) — builder, installer, evidence import/sign/certify, staging, guided records (implemented; no keys, no devices)

- **Builder** `scripts/build-release.sh` → `scripts/lib/build_release.py` (+ `release_compose.py`, `docker-compose.release.yml`,
  `release/generation-compat.json`): signed-tag/clean-checkout candidate mode (exit 3 while `release/telos-tag-signers` enrolls no signer) or an
  explicit-commit **rehearsal** mode that can never become a candidate; `git archive` source, locked-base `podman build --timestamp`, third-party
  images archived byte-for-byte with `skopeo --preserve-digests` (verified live to keep the upstream platform digest through `podman load`), a derived
  build-free Compose model (podman-compose 1.6.0 cannot `!reset` a `build:` key), `images.env`, candidate-independent `generation.json`, artifact-bound
  SBOMs with fixed timestamps, the hash graph, a normalized `tar.zst`, optional build-twice-and-compare. 41 tests with stand-in podman/skopeo.
- **Installer** `scripts/install-candidate.sh` (`candidate_install.py`): verifies the graph, loads and proves every image by digest, copies an
  immutable root-owned generation, keeps the lock in node state; `--activate` writes the receipt and `state/current-candidate-lock.json`. CLI release
  mode (`images.env` strictly parsed, overlay layered, `--build` refused). 14 + 6 tests. Backup unit/scripts now follow the generation layout.
- **Evidence** `evidence_bundle.py` (`import-evidence.py`, `sign-evidence.sh`, `verify-evidence.sh --bundle-dir`, `certify-beta.sh`): exact required
  set, freshness, dependency order, secret scan, signed index, subject re-derivation from the frozen bytes, signed tag, distinct certification signature,
  diagnostic-only on failure. 43 tests built on the real runner.
- **Staging** `stage-release.sh` / `promote-release.sh` (`stage_release.py`), verification-only `release.yml`, `candidate.yml` (pinned actions, no
  secrets): fake-`gh` tests for every refusal. `release/publisher-policy.json` is **unreviewed**, so staging is `not_run` until a reviewer names the
  repository and logins.
- **Guided/automated smoke** `release/guided-assertions.json`, `record-guided-smoke.py`, `browser_report.py`, `guided_smoke.py aggregate`
  (`certify-browser-matrix.sh`, `certify-desktop.sh`), candidate-mode Playwright projects: 40 tests. No tester is enrolled; 9 of 13 automated journeys
  have a tagged test.
- **Encrypted-backup gate** `scripts/drills/backup.sh` (`backup_gate.py`): the node takes a real backup, the verifier judges it from the repository
  (wrong password refused, `restic check`, dumped manifest, component sizes, no plaintext left, services up). 23 hermetic tests; not yet run live.
- **Frontend gates** (A35): `frontend-accessibility`/`frontend-quality` are real source checks; the axe-core audit is a candidate journey; the unsupported
  "Certified, 0 known failures" accessibility claim was withdrawn.

## P4-T6 (live) — upgrade, fast rollback, schema upgrade and restore on the reference node (rehearsal; not certification evidence)

Run by hand on the reference node (Ubuntu 26.04, real Podman 5.7, 12 services) between rehearsal generations built by the real release builder:
**P** (`cea1d765…`, schema 24), **T1''** (`5e05f583…`, docs-only change, schema 24, `rollbackCompatibleThrough` 24) and **T2''** (`b320584b…`, one additive
migration, schema 25). None of these is a candidate; a rehearsal can never become one.

| Step | Result |
|---|---|
| P → T1'' `upgrade.sh` (target generation's own scripts, no manual environment) | complete in 1m50s: verified encrypted snapshot, migration a no-op, atomic activation, receipt, markers and member journey intact |
| T1'' → P `rollback.sh` (fast, compatible) | complete in 1m27s; all three markers (before, after, left on T1'') survive; journey passes |
| P → T1'' again | complete in 1m48s |
| T1'' → T2'' `upgrade.sh` (schema 24 → 25) | complete in 1m49s; snapshot `4cbe88cf` verified |
| `rollback.sh` on the schema-25 node | **refused**, changed nothing, printed the restore command — as designed |
| `restore.sh --mode rollback` on that node | **refused** ("the operation is complete") — a defect, below |

**Defects found by these runs (all fixed, each with a regression test; see A36, A37, A39):** the upgrade lost the maintenance-lock descriptor its backup
needs; upgrade/restore did not load the node's repository environment; a second candidate stole the first generation's image names so a rollback hung
pulling a `localhost` image; `rollback.sh` named a restore that then refused the same operation (**A37**: a completed upgrade is now claimable by the
restore only when the previous generation provably cannot run on the schema); and a rollback-mode restore would have started the *newer*
generation's images and run its migrations on the restored data (**A39**: it now runs everything from the previous generation's verified tree, in
release mode builds nothing, and refuses before stopping anything if that generation's images are not loaded). The last two were visible only because
the live run exercised the incompatible-schema branch.

The sequence is now codified as the real `upgrade-rollback` gate (`scripts/drills/upgrade-rollback.sh` → `scripts/lib/upgrade_drill.py`, fixed
node-side program `upgrade_node.py`, A38); 28 hermetic tests cover both branches and every injected fault. Its live run is recorded next.

## P4-T6 (live drill) — `upgrade-rollback` gate run against rehearsal generations (rehearsal; not certification evidence)

The codified drill (`scripts/drills/upgrade-rollback.sh`, from the verifier machine, as `--rehearsal` with each target generation's lock) ran twice on the
final tooling. Generations were rebuilt by the real builder from the same commit as the tooling: **T1''** `5e05f583…` (predecessor of run A), **T1c**
`2eb453db…` (docs-only change, schema 24, `rollbackCompatibleThrough` 24) and **T2c** `2f69eec6…` (one additive migration, schema 25). Artifacts:
`telos-review/beta-evidence/vm/*upgrade-rollback-drill-rehearsal-{a,b}`.

| Run | Pair | Branch (computed from the manifests) | Result |
|---|---|---|---|
| A | T1'' → T1c | schema 24 within the window → binary rollback | **passed**, 36 assertions, 5m29s: upgrade 110 s, `rollback.sh` 88 s with both markers kept, re-upgrade 109 s, three member journeys, runtime subject over 11 services |
| B | T1c → T2c | schema 25 outside the window → refuse, then restore | **passed**, 37 assertions, 5m37s: `rollback.sh` refused in 0.5 s with the journal, schema and active generation unchanged; `restore.sh --mode rollback` 94 s brought back schema 24, kept the marker left before the snapshot, dropped the one left after, kept the failed state under `telos-rollback/`; re-upgrade; three journeys |

**What running it live found** (all fixed with tests; the first drill attempt failed on the first and the node had to be recovered by the documented restore):
- The node-side program ran the lifecycle scripts with a clean environment and dropped `PODMAN_COMPOSE_PROVIDER`, which an operator's `sudo` shell gets from
  `/etc/environment`; the upgrade stopped the writers, could not start Compose, aborted, could not restart the old generation either, and left a `failed`
  journal and a stopped node. The drill correctly recorded `upgrade.exit` failed and stopped. The program now passes that one variable through and reports
  whether the provider is usable, so a missing provider fails at `start.provider` before anything changes.
- **A37/A39**: see the previous section — a completed schema upgrade could not be restored, and the restore would have started the newer generation.
- **A40**: after a restore (databases first) the open ingress bridge came up with forwarding off — loopback and `telos status` fine, no other host could connect.
  Root cause traced to netavark's leftover `/run/sysctl.d/10-netavark-<bridge>.conf` re-applied by `systemd-sysctl`; `telos start` now repairs it. In run B the
  repair fired twice (during the restore and during the re-upgrade) and the journeys, which cross the network, passed.
- A `failed` journal (the abort could not restart the old generation) can only be cleared by a restore of the recorded snapshot, even when no data changed. That is
  the conservative design and it is documented; there is no acknowledge-and-continue command, and I did not add one.

**Limits of this evidence:** rehearsal generations on two virtual machines, not a frozen candidate against an exact signed predecessor; each run exercised only
the branch its generation pair selects (the candidate-bound claim is limited to that branch, see A38); the observer was a rehearsal machine; `candidate` mode of the
drill (restore bound to the predecessor's lock) is covered by hermetic tests only. `upgrade-rollback` stays `not_run` for certification.

## P5 (live) — encrypted-backup gate run against the generation-layout node (rehearsal; not certification evidence)

`scripts/drills/backup.sh` ran live for the first time (`telos-review/beta-evidence/vm/*encrypted-backup-drill-rehearsal`): the node took a real backup of the
running 12-service stack, and the verifier — with its own copy of the repository password — found the snapshot, proved the repository encrypted (a wrong password is
refused), ran `restic check --read-data-subset=5%`, dumped and validated the manifest from the repository, compared every component's size, confirmed retention
14 daily / 8 weekly, and confirmed no plaintext was left and every service was running again: **passed, 11 assertions, 19 s**, with an observed-runtime subject.
The real systemd service (`telos-backup.service`, generation layout) also ran once to success in 13 s.

Found by running it: (1) the gate sent its node program as `str` where the transport takes bytes — hermetic tests used an injected transport and never saw it;
(2) the node's timer had been installed during earlier work but never enabled, and its unit and a rehearsal drop-in still named the pre-generation `/opt/telos`
layout, so the first complete run failed `backup.schedule` — correctly; the runbook step now says to install the units from the active generation and
enable the timer, and that the gate checks it. **Limits:** the repository is an SFTP path on the verifier VM, not an independent off-site location; the
password was copied to the verifier by hand (the gate's contract is that the verifier holds its own copy); rehearsal record, not a candidate's.

## P5 (live) — candidate-supply-chain scan run against a rehearsal candidate (rehearsal; the gate is RED)

`scripts/scan-candidate.sh --rehearsal-lock …` ran for the first time on the real tools (Syft 1.54.1, Trivy 0.75.0, gitleaks 8.30.1, downloaded by digest from
`ci/tools.lock`) against the rehearsal candidate T2c: 11 images, SBOMs, the source tree. Artifacts: `telos-review/beta-evidence/vm/*candidate-supply-chain-scan-rehearsal`.

**Tooling defects the live run exposed (fixed, with tests):** `supply_chain.py` had been committed with a second copy of `write_scan_result`, `check_policy` and a
stale `main` plus a fragment of `scan()` spliced into `main` — the later copy won at import and the scan command crashed on its first real use (a structural test
now forbids duplicate top-level definitions; the whole repository was checked for the same accident, none other); Trivy reads an OCI image only as a layout
*directory* while the builder archives third-party images as OCI-archive tarballs, so those are now unpacked defensively (regular files and directories only, nothing
outside the target) and the scratch space is always removed; gitleaks' own config discovery looks only at the scan root, so the tree's `.gitleaks.toml` is now named
explicitly and its digest recorded; Go binaries carry no license data and the "isolated service" rule matched names (A41); one more planted test string needed
its exact value in the allowlist.

**Result of the corrected scan: failed, 465 findings, all vulnerabilities** — 0 secrets, 0 license or linking findings. By artifact: clamav 0, grimmory 78, jellyfin
149, mariadb 26, postgres 34, prometheus 8, redis 2, telos-client 41, telos-core 45, telos-egress-proxy 4, traefik 78. **23 distinct Critical advisories**: Grimmory
(Go stdlib 1.17.8 ×4, Tomcat 11.0.24 ×3), Jellyfin (perl-base ×3, libxml2, ASP.NET runtime 9.0.16 ×2), MariaDB and Postgres (the `gosu` binary's Go stdlib 1.24.6),
Traefik (stdlib 1.23.8, grpc 1.71.0, OpenSSL), and OpenSSL `CVE-2026-31789` in the Alpine layers of `telos-core` and `telos-client` (our own images).

**What a refresh of the pins would buy** (measured with the gate's own Trivy and database, not applied — moving a pin changes what runs and needs the stack re-exercised;
the table is in the archive): Alpine 3.22.2 → 3.22.6 takes 2 Critical / 19 High to 0 / 0; Traefik v3.3 → v3.7.14 takes 4 / 74 to 0 / 4; nginx-unprivileged 1.27 → 1.29
takes 2 / 39 to 0 / 43; Postgres 16.14 → 16.15 takes 33 High to 24 but the `gosu` Critical remains, as it does in MariaDB 10.11.19; Redis and Prometheus have no Critical.
Grimmory v3.5.0 and Jellyfin 10.11.11 were not re-measured. So most Criticals are fixable by moving pins; the `gosu` one and whatever remains in Grimmory and Jellyfin
need an upstream rebuild, a different image, or a decision by a person (the gate cannot accept a Critical; a High needs an exact, expiring, operator-signed record).
That triage is a maintainer decision and is **not** made here: `ci/risk-acceptances.json` stays empty.

## P4-T7 (live, final) — capacity gate on the generation-layout node: PASSED as a rehearsal

Run 4 of the 30-minute capacity drill (`telos-review/beta-evidence/vm/*capacity-rehearsal-4-generation-node-passed`), against the node serving rehearsal generation T2c from
`/srv/telos/current`: **18 assertions passed, 1,800 s, 18,000 iterations, 0 failed requests, p95 83 ms (mutation p95 53 ms), every semantic check, 12 containers with no
restart / OOM / health loss, CPU and memory inside bounds, storage kept, 25 live member sessions throughout, journey passes afterwards, fixture removed — and, unlike
runs 1–3, an observed-runtime subject**: the node now has a generation receipt and every service runs a registry-pinned image. (The first attempt refused to start: the
capacity target must name the exact candidate under test; it had `null`.) Limits: a rehearsal generation, one load host that is also a VM on the same machine as the node.

## P5 (final) — the whole local gate catalog, run on the host and on the Podman reference VM at commit `c14e2e0`

`scripts/run-gates.py --scope local` over `ci/phase-gates.json` with a source-only checkpoint (artifact `telos-review/beta-evidence/runner/local-2-{host,node-vm}`):

| Run | Result |
|---|---|
| Host (no Podman) | 23 passed; the 22 Podman-dependent gates and everything behind them `not_run` (`backend-unit`: "tool version identity is unavailable for: podman") |
| Node VM (Ubuntu 26.04, rootless Podman 5.7, Node 24, Rust) | **45 of 49 passed**, including `backend-unit/race/vet/integration/headless-build`, `api-contract-drift`, both operator CLIs, `compose-validation`, `standalone-compose`, the desktop gates (device registration, native auth, credential adapter, trusted TLS, token-safe media), `image-lock`, runtime-hardening/monitoring source checks, all four Phase 4 fixture suites, `supply-chain-policy` and `release-tooling` |
| Not passed anywhere | `static-client-image` and `desktop-builds` (`not_run`: "a verified frozen candidate is required" — they verify a frozen artifact, which a source checkpoint cannot be), and, behind the first, `browser-node-fixtures` and `frontend-quality`. `check-frontend-quality.py quality` itself passes when run directly |

Running the catalog on the VM exposed real defects, all fixed with tests: the Playwright gate's process-ownership check treated a transient `EACCES` during `exec`, and a zombie, as an uninspectable process (it passed on the fast host only by timing); the backup lock test left a `sleep 30` behind, which the supervisor rightly rejects; ESLint scanned the desktop build output in `src-tauri/target`. Environment, not product: the VM needed ripgrep, Playwright browsers, a PostgreSQL 16 tree and a higher descriptor limit (the supervising runner tracks every descendant of a process-heavy test run). Once, a Podman gate was failed for "leaving a descendant running" (rootless Podman's first-use helper); it passed on re-run, and I did **not** widen the grace period, because the catalog test deliberately allows only the Playwright gate one.

## P5 (live) — reproducible build verified on the reference VM (rehearsal)

`scripts/build-release.sh --rehearsal … --verify-reproducible` on the node VM (11 min): `reproducibilityVerified: true` — the two builds in the run (separate scratch
directories, the second without cache) were byte-identical — and the result equals a build of the same commit made hours earlier, before several later commits to the
builder's own tree: archive `265ea5ce3cd88984…` and candidate lock `2eb453dbd766…` are identical in both. Archived with the other VM evidence
(`telos-review/beta-evidence/vm/*reproducible-build-verification`). Rehearsal only; the freeze repeats it.
