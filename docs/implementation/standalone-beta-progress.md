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
