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
