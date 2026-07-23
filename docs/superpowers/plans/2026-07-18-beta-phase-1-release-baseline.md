# Telos Beta Phase 1: Release Baseline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert the current development workspace into a complete, truthful, reproducible release baseline whose clean checkout contains every input needed to build and boot Telos.

**Architecture:** Classify the existing dirty tree without discarding user work, separate source/configuration from generated or local state, establish the legal and product-truth surfaces, pin the build toolchain, and prove an immutable candidate made from the staged implementation tree before committing that exact tree.

**Tech Stack:** Git, Go 1.26.5, Node.js 24.18.0 LTS, Next.js 16.2.10, PostgreSQL 16.14, Podman/Docker Compose, shell verification

## Global Constraints

- Execution mode is Fable 5 Medium inline: execute one task per run and stop after its commit and evidence review.
- Read the [master plan](2026-07-18-beta-readiness-master.md) and approved [design](../specs/2026-07-18-beta-readiness-remediation-design.md) before P1-T1.
- Treat every pre-existing modification and untracked file as user-owned until classified. Never use `git reset --hard`, `git clean`, or bulk checkout commands.
- This phase may commit reviewed existing work because clean-checkout completeness is its purpose. It may not rewrite application behavior except legal/product-truth UI and build reproducibility.
- Runtime-critical files cannot remain untracked at the phase exit.
- Generated binaries, exports, local certificates, environment files, test output, and prototype data cannot enter the release commit.
- Do not alter applied migration contents during classification; later schema repairs use new migrations.

## Entry Gate

- [ ] Confirm the branch and record `git rev-parse HEAD`, `git status --short`, `git diff --stat`, and `git ls-files --others --exclude-standard`.
- [ ] Confirm commit `1c556e0` or its descendant contains the approved remediation design.
- [ ] Confirm no other agent/process is actively editing the same files.
- [ ] Confirm the existing frontend development server may remain running but is not used as clean-checkout evidence.

## File Responsibility Map

| Area | Files |
|---|---|
| Repository classification | `.gitignore`, `frontend/.gitignore`, `documentation/operations/repository-baseline.md` |
| Required release inputs | `backend/db/migrations/*.sql`, `config/dynamic/routes.yaml`, `docker-compose.dev.yml`, `documentation/operations/*.md`, `scripts/*.sh` |
| Legal/product truth | `LICENSE`, `NOTICE`, `CREDITS.md`, `README.md`, `documentation/README.md`, `documentation/architecture/*.md`, `documentation/product/beta-feature-status.md` |
| Credits UI | `frontend/src/components/settings/CreditsSection.tsx`, `frontend/src/app/(shell)/settings/page.tsx`, `frontend/src/styles/settings.css`, `frontend/e2e/credits.spec.ts` |
| Reproducible build/test | `.nvmrc`, `backend/go.mod`, `backend/go.sum`, `backend/Dockerfile`, `frontend/package.json`, `frontend/package-lock.json`, `frontend/vitest.config.ts`, `frontend/src/test/setup.ts`, `docker-compose.yml` |
| Clean-checkout proof | `scripts/verify-clean-checkout.sh`, `scripts/tests/verify-clean-checkout-test.sh`, `documentation/operations/release-inputs.pathspec`, `documentation/operations/clean-checkout-verification.md` |

---

## P1-T1: Classify and Normalize the Existing Working Tree

**Closes:** R01

**Files:**

- Create: `documentation/operations/repository-baseline.md`
- Modify: `.gitignore`
- Modify: `frontend/.gitignore`
- Delete from source control: `backend/db/legacy-prototype-backup-20260711.sql`
- Exclude/remove generated local state: `backend/telos-core`, `backend/out/`, `frontend/.next/`, `frontend/out/`, `frontend/test-results/`, `frontend/playwright-report/`

**Contract:** `repository-baseline.md` records each pre-existing changed/untracked path under exactly one disposition: release input, reviewed application change, generated output, local secret/state, or intentionally deferred non-runtime artifact.

- [ ] Capture the entry-gate inventory, inspect every changed/untracked path, and write its disposition and owner into `documentation/operations/repository-baseline.md`; include the current commit and date, never secret content.
- [ ] Add precise ignore rules for build output, compiled binaries, local certificates, local environment files, backup output, test reports, and temporary upload/restore staging; do not ignore migrations, configuration, operations docs, or scripts.
- [ ] Remove the tracked legacy prototype SQL dump and verify no committed dump contains the three live users, five seeded channels plus activity, or fifteen historical messages.
- [ ] Remove only generated files confirmed by the inventory while preserving every source/configuration change; use path-specific removal commands and inspect `git diff --stat` afterward.
- [ ] Run `git check-ignore -v` against one representative generated path and `git ls-files --others --exclude-standard`; expected result: generated/local state is ignored and all remaining untracked paths are explicitly classified.
- [ ] Run `git diff --check`; expected result: no whitespace errors in files touched by this task.
- [ ] Commit only the classification, ignore rules, and prototype/generated cleanup with message `chore: classify beta release workspace`.

## P1-T2: Track Every Runtime-Critical Release Input

**Closes:** R01

**Files:**

- Add/review: `backend/db/migrations/0004_settings.sql`
- Add/review: `backend/db/migrations/0007_manage_library.sql`
- Add/review: `config/dynamic/routes.yaml`
- Add/review: `docker-compose.dev.yml`
- Add/review: `documentation/operations/backup-and-restore.md`
- Add/review: `scripts/backup.sh`
- Add/review: `scripts/restore.sh`
- Create: `scripts/verify-clean-checkout.sh`
- Create: `scripts/tests/verify-clean-checkout-test.sh`
- Modify: `.env.example`
- Modify: `AGENTS.md`

**Contract:** A clean checkout includes the complete ordered migrations `0001` through `0007`, the production route provider, a loopback-only development overlay, documented environment inputs, and executable operations scripts. Inventory mode checks the current index and working tree by default; it must not silently substitute the previous `HEAD`.

- [ ] Create `scripts/verify-clean-checkout.sh` and its named shell harness `scripts/tests/verify-clean-checkout-test.sh`; have the harness construct a disposable Git fixture, omit `config/dynamic/routes.yaml`, and assert `bash scripts/verify-clean-checkout.sh --inventory-only` exits `10` before restoring the required path.
- [ ] Review migrations `0004` and `0007` against the live schema and existing handlers, preserve their applied bytes, and track them in numeric order without rewriting migrations `0001` through `0006`.
- [ ] Review `config/dynamic/routes.yaml`, `docker-compose.dev.yml`, and the backup/restore docs/scripts; ensure production exposes only Traefik HTTP entry points, development bindings use `127.0.0.1`, and current recovery mechanics are marked for Phase 3 hardening rather than claimed complete.
- [ ] Update `.env.example` and `AGENTS.md` together: include every referenced non-secret example variable, distinguish production/development inputs, preserve normative/process-boundary rules, and make `docker compose --env-file .env.example config --quiet` succeed.
- [ ] Run `bash scripts/tests/verify-clean-checkout-test.sh` and then `bash scripts/verify-clean-checkout.sh --inventory-only`; expected result: the fixture failure is proven, the current implementation tree passes, all required paths are tracked, migrations are contiguous, and no production service other than Traefik publishes an HTTP administration port.
- [ ] Commit with message `chore: track complete beta release inputs`.

## P1-T3: Establish Apache Licensing, Credits, and Product Truth

**Closes:** R02, P01

**Files:**

- Create: `LICENSE`
- Create: `NOTICE`
- Create: `CREDITS.md`
- Create: `documentation/product/beta-feature-status.md`
- Create: `scripts/check-release-truth.sh`
- Create: `frontend/src/components/settings/CreditsSection.tsx`
- Create: `frontend/e2e/credits.spec.ts`
- Modify: `README.md`
- Modify: `documentation/README.md`
- Modify: `documentation/architecture/01-system-overview.md`
- Modify: `documentation/architecture/02-deployment.md`
- Modify: `documentation/architecture/03-gateway-and-api.md`
- Modify: `documentation/architecture/04-frontend-architecture.md`
- Modify: `documentation/architecture/05-roadmap-and-licensing.md`
- Modify: `frontend/src/app/page.tsx`
- Modify: `frontend/src/app/(shell)/settings/page.tsx`
- Modify: `frontend/src/styles/settings.css`

**Interface:**

```ts
export type CreditEntry = {
  name: string;
  role: string;
  sourceUrl: string;
  licenseName: string;
  licenseUrl: string;
};
```

`CREDITS.md` is canonical; the in-app Credits section renders a reviewed static projection of the same component names, source links, licenses, and process-boundary roles.
`scripts/check-release-truth.sh` exits nonzero for a non-Apache Telos license claim, local/generic repository link, nonexistent tunnel/service, absolute local-only privacy claim, missing isolated-service credit, or mismatch between the canonical Credits matrix and its UI projection.

- [ ] Write the Playwright test first: an authenticated user can open Settings → Credits, find Jellyfin, Grimmory, LiveKit, Traefik, PostgreSQL, Redis, MariaDB, and ClamAV, and follow valid source/license links; show the test fails before the surface exists.
- [ ] Add the unmodified Apache License 2.0 text to `LICENSE`, project-specific copyright/attribution content to `NOTICE`, and a complete isolated-service/infrastructure matrix to `CREDITS.md`.
- [ ] Reconcile README/normative documents and publish `documentation/product/beta-feature-status.md`: replace local/generic links, state Apache-2.0 plus accurate third-party licenses, require the approved non-AI capabilities, mark Oracle/AI removed and OIDC/external notifications outside beta, and disclose direct HTTPS, optional private networks, metadata providers, and encrypted operator-selected off-node backups.
- [ ] Add the Credits settings surface and remove any legal/product link that targets a nonexistent location; ensure the surface is available to every authenticated role.
- [ ] Run `bash scripts/check-release-truth.sh && (cd frontend && npx playwright test e2e/credits.spec.ts --project=chromium)`; expected result: no false license/local link/absolute privacy/nonexistent tunnel match and the Credits test passes.
- [ ] Commit with message `docs: establish beta licensing and product truth`.

## P1-T4: Pin Toolchains and Make Dependency Installation Reproducible

**Closes:** R03

**Files:**

- Create: `.nvmrc`
- Modify: `backend/go.mod`
- Modify: `backend/Dockerfile`
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`
- Create: `frontend/vitest.config.ts`
- Create: `frontend/src/test/setup.ts`
- Create: `frontend/src/test/test-harness.test.tsx`
- Create: `scripts/tests/reproducible-build-test.sh`
- Modify: `frontend/playwright.config.ts`
- Modify: `docker-compose.yml`
- Modify: `README.md`
- Modify: `documentation/architecture/02-deployment.md`
- Modify: `scripts/verify-clean-checkout.sh`

**Contract:** The build uses Go 1.26.5, Node.js 24.18.0 LTS, PostgreSQL 16.14, Next.js 16.2.10, `npm ci`, and lockfile integrity. Security dependency remediation remains P2-T1, but no build input may float.

- [ ] Add version assertions to `scripts/verify-clean-checkout.sh`; demonstrate the assertions reject the current Go 1.22/Node 22/non-exact Next baseline.
- [ ] Set `.nvmrc` to `24.18.0`, `go 1.26.5` in `backend/go.mod`, the builder images to exact patch tags, and PostgreSQL to the `16.14` image tag; do not introduce a host-Go requirement.
- [ ] Pin Next.js `16.2.10`, PostCSS override `8.5.10`, EPUB.js `0.4.2`, and all direct dependencies; add the Vitest `4.1.10` component harness and an explicit Playwright `chromium` project, regenerate the Node 24.18.0 lockfile, and prove unit/component plus focused Chromium commands work.
- [ ] Replace `npm install --legacy-peer-deps` with `npm ci`; copy only dependency manifests before install and exclude local `node_modules`, `.next`, `out`, and test artifacts from the build context.
- [ ] Run `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... && (cd frontend && npm ci && npm run lint && npm run test:unit && npm run build) && bash scripts/tests/reproducible-build-test.sh`; expected result: every gate passes and two unchanged-source builds have identical dependency manifests, embedded frontend inventory, and image configuration apart from explicitly normalized timestamps.
- [ ] Commit with message `build: pin beta toolchains and dependencies`.

## P1-T5: Prove the Clean-Checkout Build and Boot Gate

**Closes:** R01, R03, O05

**Files:**

- Complete: `scripts/verify-clean-checkout.sh`
- Modify: `scripts/tests/verify-clean-checkout-test.sh`
- Create: `documentation/operations/release-inputs.pathspec`
- Create: `documentation/operations/clean-checkout-verification.md`
- Modify: `.dockerignore`
- Modify: `docker-compose.yml`
- Modify: `.env.example`

**Script contract:**

```text
scripts/verify-clean-checkout.sh --inventory-only [--source-ref <tree-ish>]
scripts/verify-clean-checkout.sh --source-ref <tree-ish> --evidence-out <absolute-path>
  0  all requested checks passed
  10 tracked/input inventory failed
  20 build or test failed
  30 compose boot/readiness failed
  40 teardown or artifact-integrity failed
```

Inventory mode checks the current index and working tree unless `--source-ref` is supplied. Full mode refuses to run without `--source-ref`, exports that exact tree-ish into a temporary directory, generates a noncommittable test environment from cryptographically random values, validates Compose, builds from the export, boots the stack, runs smoke probes, writes sanitized evidence outside the repository, tears down, and removes the temporary directory. It never defaults to `HEAD`.

- [ ] Complete `scripts/tests/verify-clean-checkout-test.sh` with disposable fixtures for a missing route (`10`), a forced build failure (`20`), a failed readiness probe (`30`), and a forced teardown/integrity failure (`40`); run `bash scripts/tests/verify-clean-checkout-test.sh` and require every exit-code assertion to pass.
- [ ] Implement `--source-ref` export, `--evidence-out`, isolated Compose project/volume names, cleanup traps, and mode-`0600` random smoke credentials; reject an evidence path inside the repository and never copy `.git`, `.env`, certificates, caches, generated output, compiled output, or backups.
- [ ] Finalize `documentation/operations/release-inputs.pathspec` as a reviewable, one-literal-path-per-line manifest of every classified release input and reviewed application change, including the manifest and verification evidence document themselves. Require a clean index, run `git add -A --pathspec-from-file=documentation/operations/release-inputs.pathspec`, inspect `git diff --cached --name-status`, and run `git diff --cached --check`; stop if any staged path is not classified or any release input remains unstaged.
- [ ] Write a provisional tree without moving the branch using `PREVIEW_TREE=$(git write-tree)` and `PREVIEW_COMMIT=$(printf '%s\n' 'Telos Phase 1 verification preview' | git commit-tree "$PREVIEW_TREE" -p HEAD)`. Run `bash scripts/verify-clean-checkout.sh --source-ref "$PREVIEW_COMMIT" --evidence-out /tmp/telos-phase1-preview.json`; expected result: backend/frontend gates, container build, isolated boot, gateway liveness, login/bootstrap, static assets, and teardown all pass without touching the active development stack.
- [ ] Copy only stable, sanitized tool/image/command/status/service fields from the preview evidence into `documentation/operations/clean-checkout-verification.md`, never credentials or a self-referential candidate ID; stage that document with the same manifest, rerun the staged-path and whitespace checks, then freeze `CANDIDATE_TREE=$(git write-tree)` and create `CANDIDATE_COMMIT=$(printf '%s\n' 'Telos Phase 1 final verification candidate' | git commit-tree "$CANDIDATE_TREE" -p HEAD)` without moving the branch.
- [ ] Run `bash scripts/verify-clean-checkout.sh --source-ref "$CANDIDATE_COMMIT" --evidence-out /tmp/telos-phase1-final.json`; expected result: exit `0`, evidence agrees with the committed stable fields, `git status --short` contains only classified deferred non-runtime paths, and `git ls-files --others --exclude-standard` contains no runtime-critical input. If any release input changes after `git write-tree`, restage it, create a new candidate, and rerun the full gate.
- [ ] Only after the final candidate passes, run `git commit -m 'test: prove clean beta checkout builds and boots' -m "Verified-Tree: $CANDIDATE_TREE"` and `test "$(git rev-parse 'HEAD^{tree}')" = "$CANDIDATE_TREE"`; if a hook changes the index or the tree comparison fails, do not claim success and repeat candidate verification for the new staged tree.

## Phase 1 Exit Gate

- [ ] `git ls-files backend/db/migrations` shows contiguous `0001` through `0007`.
- [ ] `git ls-files --others --exclude-standard` contains no runtime-critical input.
- [ ] No committed file contains live user/message prototype data, a credential, a private certificate, a compiled binary, or generated frontend output.
- [ ] `LICENSE`, `NOTICE`, `CREDITS.md`, the in-app Credits surface, and beta feature-status matrix agree on Apache-2.0 and third-party boundaries.
- [ ] Product and privacy scans return no local links, false license statement, nonexistent tunnel, or absolute local-only network claim.
- [ ] Go 1.26.5 and Node.js 24.18.0 clean builds pass from the lockfiles.
- [ ] The Phase 1 commit’s `Verified-Tree` trailer equals `git rev-parse 'HEAD^{tree}'`; `bash scripts/verify-clean-checkout.sh --source-ref HEAD --evidence-out /tmp/telos-phase1-post-commit.json` succeeds, and its sanitized stable fields match the committed verification document.
- [ ] The common verification commands in the master plan pass.
- [ ] Stop for human review; Phase 2 starts only from this accepted commit.
