# Standalone Beta Phase 1: Truth and Gates Reconciliation Plan

> **Reconciled 2026-10-13.** [Platform policy](../../../documentation/product/platform-scope.md) is binding: Ubuntu 24.04+ servers, browser and Linux desktop first, Windows/Mac native clients deferred. This document replaces the original create-from-scratch instructions. Task IDs 1–6 remain stable; existing implementations must be inspected and preserved.

> **For agentic workers:** Use `superpowers:executing-plans` for sequential implementation, or the already-authorized execution method. Checkboxes below are remaining verification or remediation work, not evidence that the existing component is missing.

**Goal:** Close the remaining Phase 1 catalog/CI gaps, verify the existing fail-closed foundation, and produce an honest source-verification checkpoint before Phase 2.

**Architecture:** Preserve evidence envelope v2, catalog schema v1, the argv-safe runner, and Linux process supervision. Reconcile their actual behavior with the handoff and CI. Final immutable-artifact verification belongs to Phase 5 Task 4a; do not retrofit its future schema or semantics into Phase 1.

**Tech Stack:** Bash, Python 3, Git, Node 24.18.0, pinned Go containers, GitHub Actions. Python-only checks use the standard library; Compose validation additionally imports PyYAML.

**Spec:** [Standalone beta design](../specs/2026-08-20-standalone-beta-readiness-design.md). Begin with the [cold handoff](2026-10-13-standalone-beta-cold-handoff.md), [shared handoff contracts](2026-10-13-standalone-beta-handoff-contracts.md), [master](2026-08-20-standalone-beta-readiness-master.md), and platform policy. Discover the checkout through Git rather than hardcoding its previous location.

## Global constraints

- Preserve all pre-existing uncommitted changes. Inspect `git status --short` and scoped diffs before editing; do not reset, clean, stash, stage broadly, or certify this dirty tree as a candidate.
- Keep existing evidence v2 and catalog v1 interfaces until the explicit Phase 5 Task 4a migration. Do not replace the hardened runner with the original plan's simple `subprocess.run` sketch.
- Keep `passed`/0, `not_run`/3, and `failed`/nonzero-other-than-3 semantics. No missing executable, environment, reference host, or implementation is success.
- Commands are argv arrays. Do not add shell command strings, implicit retries, secret-bearing arguments, or environment dumps.
- Gate IDs are stable; update catalog dependencies and tests together. Windows/Mac server gates and initial Windows/Mac native gates stay excluded.
- Do not implement Phase 2–5 product features or turn their wrappers green here. In particular, `telos-client`, guided integration setup, desktop delivery, encrypted recovery, and candidate certification remain later-phase work.
- Run checks from the repository root unless explicitly stated otherwise. Report actual exit codes and limitations, not expected results as observations.
- Resolve implementation failures with focused tests; do not replace dependencies, lockfiles, installed packages, or acceptance criteria merely to make a gate pass.

## Read this state before executing

| Task | Current source disposition | Remaining work |
|---|---|---|
| 1 — Evidence envelope | Implemented: `scripts/lib/evidence.py`, strict v2 schema, verifier wrapper, adversarial fixtures | Fresh verification; preserve stronger current validation |
| 2 — Fail-closed wrappers | Implemented: `scripts/not-run.py` and strict wrappers; beta certification intentionally exits 3 | Fresh behavioral verification; retain later-phase ownership |
| 3 — Catalog and runner | Implemented and subsequently hardened: source binding, tool probes, process supervision, Playwright lifecycle wrapper | Correct TypeScript command, add platform gate, verify source-checkpoint procedure |
| 4 — Product contract | Implemented; current contract already expresses Ubuntu 24.04+ and Linux-first clients | Preserve adopted contract; add catalog/platform mutation coverage |
| 5 — Truth cleanup | LiveKit proxy removal and blocked delivery/operator-path documentation already exist | Fresh Node proxy and product mutation checks; reconcile only newly found discrepancies |
| 6 — CI/phase exit | Truth-and-gates job and inventory exist | Add missing real CI checks and prerequisite setup; fresh full source checkpoint |

Evidence for these source claims is in `scripts/lib/evidence.py:79` and `:257`, `scripts/not-run.py:70`, `scripts/certify-beta.sh:27`, `scripts/run-gates.py:277` and `:810`, `scripts/run-playwright-gate.py:539`, `scripts/check-product-truth.sh:63` and `:346`, `.github/workflows/ci.yml:96`, and `scripts/verify-clean-checkout.sh:65`. Line numbers are navigation aids; inspect named functions after edits.

Earlier raw logs under `telos-review/evidence/` recorded ten checks, including envelope, fail-closed scripts, runner fixtures, scope, product/release truth, inventory, and hygiene. They do not identify a frozen delivered candidate or certify the current working tree. `document-qa.json`, `desktop.png`, and `mobile.png` concern the review HTML, not the Telos product. The fixture output `version-node: passed` or `version-podman: passed` tests fake tool probes; it does not establish that those runtimes are installed.

During the 2026-10-13 reconciliation, direct envelope, wrapper, runner/CI fixtures, accumulated-suite, platform-scope, product/release truth, and documentation-hygiene checks were rerun successfully. The runner suite also passed its inventory subcheck. The product mutation suite stopped at `tests/operations/product_truth_test.sh:12` with `node: command not found` (exit 127). These observations describe that audit run only; the implementer must rerun the exit suite after changes.

## Environment and verification levels

The inspected host is Ubuntu 26.04.1 x86_64. Bash, Git, Python 3, and PyYAML are available. `node`, `npm`, `npx`, `podman`, `podman-compose`, `docker`, and host Go are absent from PATH; `frontend/node_modules/` and `release/candidate-lock.json` are absent. Cargo/Rust presence alone does not establish a configured Linux Tauri build environment. Recheck prerequisites when resuming; do not blindly trust older AGENTS/CLAUDE statements that this host already has Podman.

| Level | What may run | What its result means |
|---|---|---|
| Direct source/fixture checks | Existing Bash/Python test scripts, including temporary fixture repositories, on the working tree | Current script behavior was tested; no candidate or installed-product claim |
| Committed source checkpoint | Runner with `--scope local --phase 1`, clean HEAD, disposable source identity file, fresh external evidence directory | Selected Phase 1 source checks passed against that commit |
| Final candidate verification | Phase 5 verifier with the frozen lock and built artifact/runtime evidence after Task 4a | Delivered artifacts and required external journeys are certified only if every required outcome passes |

A Phase 1 checkpoint must not depend on creating the Phase 5 release candidate. The current runner accepts a JSON document containing `sourceCommit`; this is enough for its source checks but is not a complete candidate lock. Name the temporary file `source-checkpoint.json`, keep it outside the checkout, and label the output **source verification only**. Do not save it as `release/candidate-lock.json`, promote it, or upload its envelopes as release evidence.

The current runner requires a clean source tree and HEAD equality before running source gates (`scripts/run-gates.py:277`), creates a new evidence directory whose parent already exists (`:351`), and requires `--evidence-dir` (`:815`). `--phase 1` selects phase numbers up to 1. Omitting both `--phase` and `--gate` selects all local gates, including later placeholders; nonzero is then expected. The verifier checks envelope shape and identity; successful envelope verification does not mean every envelope is `passed`, timely, artifact-bound, or sufficient for release.

## Review focus

- Working directory: a TypeScript command must actually read `frontend/tsconfig.json` and report a real type error (Task 3).
- Scope drift: platform tests must run through the catalog and CI, with deferred clients excluded even as indirect prerequisites (Tasks 3–4).
- False environment claims: passing fake-tool/process fixtures must not substitute for running Node, Go, Compose, or browser checks (Task 6).
- Source/candidate confusion: a dirty tree, missing tools, later placeholders, or a disposable identity file cannot produce candidate success (Tasks 1, 3, and 6).
- CI enforcement: skipped/optional job steps, missing setup, and bare Playwright without a managed server must fail contract tests (Task 6).

## Task 1: Reconcile and verify evidence envelope v2

**Status:** Implemented; fresh verification required after any dependent change.

**Files:** Inspect `scripts/lib/evidence.py`, `scripts/verify-evidence.sh`, `release/evidence-envelope.schema.json`, `tests/operations/evidence_envelope_test.sh`, and `tests/helpers/mutate_evidence.py`.

**Interfaces:** Preserve `evidence.py digest PATH`, `write`, and `verify --schema PATH --candidate-lock PATH ENVELOPE...`. Preserve strict schema, source-commit binding, source-subject digest binding, nonempty recognized tool versions, secret-key rejection, timestamp order, private atomic output, and existing overwrite protection. The lock digest currently hashes raw lock bytes; do not silently change it to a different canonicalization rule.

- [ ] Run `bash tests/operations/evidence_envelope_test.sh`. Success means exit 0 after expected rejection diagnostics for invalid fixtures.
- [ ] Inspect `verify_envelopes` and record its boundary: it validates envelopes, including correctly formed `not_run`/`failed` envelopes; it is not the Phase 5 completeness/freshness/artifact certification verifier.
- [ ] If a real regression appears, add one focused failing fixture and repair that regression. Do not recreate the module or weaken current checks to match the old plan.

## Task 2: Reconcile and verify fail-closed wrappers

**Status:** Implemented; normal later-phase execution remains intentionally `not_run`.

**Files:** Inspect `scripts/not-run.py`, `tests/operations/beta_certification_test.sh`, and the wrappers listed by that test. They cover certification, runtime hardening, capacity, exposure, browser matrix, release build/stage/signing/pins, restored-node/monitoring, upgrade/rollback, provisioning, local CI, SBOM validation, and the three operations drills.

**Interfaces:** Preserve unknown/incomplete argument exit 2, unimplemented check exit 3, the optional valid `not_run` envelope, and corrective reasons naming the owning phase. The Bash `./telos` dispatcher remains authoritative; its future guided command is `telos configure integrations`.

- [ ] Run `bash tests/operations/beta_certification_test.sh`. The test exits 0 only when it successfully proves the wrappers fail closed.
- [ ] Inspect both provisioning wrappers and confirm their reasons point to Phase 2 guided setup, without claiming that setup exists now.
- [ ] Repair only an observed parsing/evidence regression. Retain all later-phase `not_run` results; fixture success is not successful provisioning, release building, or certification.

## Task 3: Reconcile the catalog and correct residual gate commands

**Status:** Runner implemented; two catalog gaps remain.

**Files:** Modify `ci/phase-gates.json` and `tests/operations/ci_gates_test.sh`; create `tests/operations/frontend_types_gate_test.sh`. Inspect `scripts/run-gates.py`, `scripts/lib/process_supervisor.py`, `scripts/run-playwright-gate.py`, and `scripts/verify-accumulated-suite.sh` without redesigning them.

**Interfaces:** Keep catalog schemaVersion 1 and the existing runner arguments. Preserve the strengthened Linux `/proc`, pidfd, subreaper, descendant cleanup, timeout, source-binding, and tool-probe behavior. Phase 5 Task 4a owns later candidate-execution changes.

- [ ] Add assertions in `ci_gates_test.sh` requiring a local, required, phase-1 `platform-scope` gate with command `["python3", "tests/operations/platform_scope_test.py"]`, `subjectKind: "source"`, no prerequisites, and a 120-second timeout. Require `beta-certification` to include it with all other required predecessor IDs.
- [ ] Add a real compiler regression in `frontend_types_gate_test.sh`. Load the `frontend-types` argv from the catalog; assert it selects `frontend/tsconfig.json`. In a temporary directory create a tsconfig naming a single TypeScript file with `types: []`, `skipLibCheck: true`, and `noEmit: true`. Run the catalog compiler from the repository root with only the `--project` value changed to that temporary tsconfig: a valid assignment must exit 0; `const value: number = "wrong"` must exit nonzero and report a type error. Require the locked installed TypeScript compiler; missing Node/dependencies is a failed prerequisite, never a skipped pass. This test must never invoke package installation.
- [ ] Run the new assertions and compiler regression on a prepared Node host. Confirm the present command is rejected before editing the catalog.
- [ ] Replace the existing `frontend-types` argv `["npx", "--prefix", "frontend", "tsc", "--noEmit"]` with `["node", "frontend/node_modules/typescript/bin/tsc", "--project", "frontend/tsconfig.json", "--noEmit"]`. `npx --prefix` does not make root-launched `tsc` use the frontend project. Explicit `--project` and the locked local compiler remove that ambiguity and avoid implicit downloads. Add the `platform-scope` gate and its certification dependency.
- [ ] Run `bash tests/operations/frontend_types_gate_test.sh`, the corrected `frontend-types` argv, `bash tests/operations/ci_gates_test.sh`, and `bash tests/operations/accumulated_suite_test.sh`. The first two need Node/dependencies. The latter suites use controlled fixtures and can run without installed product runtimes.
- [ ] Record the disposable source-checkpoint procedure below in the implementation evidence. Do not weaken dirty-tree protection to make a runner invocation work during editing.

## Task 4: Preserve and strengthen the adopted product contract

**Status:** Structural checker and October platform narrowing implemented; catalog integration needs regression coverage.

**Files:** Inspect `documentation/product/beta-contract.json` and `documentation/product/beta-feature-status.md`. Modify `tests/operations/platform_scope_test.py` and `scripts/check-product-truth.sh` for scope/catalog enforcement.

**Interfaces:** Preserve exact current fields: `server.os: "ubuntu-24.04"`, `server.versionPolicy: "24.04-or-newer"`, `server.arch: "x86_64"`, `server.podmanMinimum: "5.0.0"`, `desktop: ["linux-x86_64"]`, and `plannedDesktop: ["windows-x86_64", "macos-universal"]`. Preserve four content modules plus Settings and the blocked browser/backup markers. Do not copy the original August JSON that required Windows/Mac native clients.

- [ ] Add mutations in `platform_scope_test.py` for removing `platform-scope` from the catalog, making it optional, moving it out of phase 1/local scope, or deleting its certification dependency. Require each mutated tree to fail `check-product-truth.sh --root FIXTURE` with a diagnostic naming `platform-scope`. Run these tests before changing the checker and observe the missing enforcement.
- [ ] Extend the checker's catalog validation to require that exact gate once, with `required: true`, `phase: 1`, `scope: "local"`, the Task 3 argv, `subjectKind: "source"`, and membership in `beta-certification.prerequisites`. Rerun the mutation tests and retain all existing scope checks.
- [ ] Keep the existing non-Ubuntu-server, Windows-server-gate, direct/indirect deferred-desktop, and optional-browser-gate rejection cases.
- [ ] Run `python3 tests/operations/platform_scope_test.py`, `bash tests/operations/ci_gates_test.sh`, `bash scripts/check-product-truth.sh`, and, with Node available, `bash tests/operations/product_truth_test.sh`.
- [ ] Retain the truthful transitional state: `browserService` names the planned `telos-client`, but product truth currently permits its absence only with the explicit Phase 2 blocked-delivery markers (`scripts/check-product-truth.sh:346`). Product truth passing is not a deployed browser node. Phase 2 owns the runtime route/service change.

## Task 5: Verify existing removed-feature and operator-path cleanup

**Status:** Implemented; full product mutation/proxy verification unavailable on the inspected host until Node is available.

**Files:** Inspect `frontend/dev-server.mjs`, `tests/operations/dev_server_proxy_test.mjs`, `tests/operations/product_truth_test.sh`, current README/agent guides/product docs, and the provisioning wrappers. Edit only a discrepancy exposed by a current check; historical specs are not shipped-runtime scan targets.

**Interfaces:** Preserve the API/WebSocket dev proxy and the Bash CLI entry point. Preserve accurate blocked-delivery statements while later implementations are absent.

- [ ] On a Node 24.18.0 host run `node --check frontend/dev-server.mjs` and `bash tests/operations/product_truth_test.sh`. The latter includes the dev-proxy behavioral test and structural mutation fixtures.
- [ ] Run `bash scripts/check-product-truth.sh`, `bash scripts/check-release-truth.sh`, `python3 scripts/check-documentation-hygiene.py citations`, and `python3 scripts/check-documentation-hygiene.py placeholders`.
- [ ] Use the Python hygiene commands as gates. Raw `grep` exits 1 when it finds no matches, so the historical instruction “all commands exit 0 and grep prints nothing” was contradictory.

## Task 6: Complete CI enforcement and record the Phase 1 exit

**Status:** Truth job and existing runtime jobs are present; catalog coverage is incomplete.

**Files:** Modify `.github/workflows/ci.yml`, `tests/operations/ci_gates_test.sh`, and `scripts/verify-clean-checkout.sh`. Include the new regression test from Task 3 and scope changes from Task 4. Keep explicit tracking of the current policy and test files, which were untracked when this reconciliation began.

**Interfaces:** CI runs source/fixture checks. It must not manufacture a frozen candidate lock or upload fixture/source envelopes as certification. Direct CI commands may use setup-go; the local catalog remains Podman-based.

Current CI already runs backend race tests, frontend lint/unit/build/bundle, headless build, API drift, Linux experimental Go CLI builds, and the truth fixtures. It omits explicit full `go vet`, the disposable PostgreSQL/Redis integration harness, Compose rendering, explicit TypeScript checking, accessibility-evidence validation, and actual Playwright execution. Both hygiene checks already run transitively through `ci_gates_test.sh`; do not report them missing. The `platform_scope_test.py` CI step already exists, while its catalog gate is missing. Linux Go CLI builds do not certify the authoritative Bash installer.

- [ ] Extend the existing workflow contract tests to require the missing checks as real required steps, reject job/step skipping and `continue-on-error`, and require tool/dependency setup before dependent steps. Preserve current adverse workflow mutations rather than replacing them with substring checks.
- [ ] Add explicit backend `go vet ./...` and an integration job invoking `bash scripts/test-backend.sh` with working Podman and registry access. The harness itself supplies disposable PostgreSQL 16.14 and Redis 7; plain `go test` self-skips integration without their environment.
- [ ] Add required Compose validation using `bash scripts/validate-compose.sh --env-file .env.example`. Prepare Podman 5+, its Compose provider, and Python PyYAML in a reproducible CI environment; check their versions/import before the render. Do not assume Docker's presence fulfills this script's `podman compose` call. Host distribution support remains Ubuntu 24.04+.
- [ ] Pin setup-node to the repository's `24.18.0` and run `npm ci` in `frontend/` before Node-dependent jobs. In the frontend job run the corrected explicit TypeScript command and `bash tests/operations/frontend_types_gate_test.sh` from the root. Add `npm --prefix frontend run check:a11y-evidence` after build.
- [ ] Add `python3 scripts/run-playwright-gate.py --playwright-timeout-seconds 1650` with available Playwright browser binaries/system libraries, free port 3000, and a job timeout allowing at least the catalog's 1800-second gate budget plus setup. Preserve its process cleanup. Provision Chromium and WebKit for the existing projects through explicit CI setup using the locked Playwright installation. `npm run check:browsers` only checks a JSON declaration; it does not prove browser binaries are installed. This suite is not the complete external browser matrix.
- [ ] Ensure the `truth-and-gates` job supplies Node 24.18.0 before `product_truth_test.sh`; it starts a Node proxy fixture. Keep the existing envelope, wrapper, runner, accumulated-suite, scope, product/release, and inventory steps required. Add `documentation/product/platform-scope.md`, `scripts/run-playwright-gate.py`, `scripts/check-documentation-hygiene.py`, `tests/operations/platform_scope_test.py`, and `tests/operations/frontend_types_gate_test.sh` to the explicit clean-checkout inventory. Preserve the existing evidence/supervisor inventory entries; do not stage unrelated edits.
- [ ] Run the direct suite below. Run the full source checkpoint only after residual code and approved existing changes are committed and the intended source tree is clean. Request review of residual changes; do not require recreating the original six commits.

## Exact verification sequence

**A. Working-tree source/fixture checks.** Each command must be recorded separately with exit code and output. They do not require a release lock. Missing Node blocks the two explicitly marked commands; continue independent Python/Bash checks and report the remaining gap.

```bash
bash tests/operations/evidence_envelope_test.sh
bash tests/operations/beta_certification_test.sh
bash tests/operations/ci_gates_test.sh
bash tests/operations/accumulated_suite_test.sh
python3 tests/operations/platform_scope_test.py
bash tests/operations/product_truth_test.sh       # requires Node
bash tests/operations/frontend_types_gate_test.sh # added by Task 3; requires Node + npm ci
bash scripts/check-product-truth.sh
bash scripts/check-release-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only
python3 scripts/check-documentation-hygiene.py citations
python3 scripts/check-documentation-hygiene.py placeholders
git diff --check
```

Expected only after implementation and prerequisites: exit 0 for every command. Expected invalid-fixture diagnostics inside passing tests are not product failures. Inventory checks tracked inputs, not clean installation or runtime readiness. No file whose only copy is untracked may be treated as available in the next clean checkout.

**B. Prepare a verification host.** Use an explicit environment-setup task or existing prepared Ubuntu host. Node must match `.nvmrc` (24.18.0); install frontend dependencies with `npm ci` without rewriting lockfiles. Supply Podman 5+, a working Compose provider, PyYAML, pinned Go image `docker.io/library/golang:1.26.5`, PostgreSQL 16.14/Redis 7 test images, and Playwright Chromium/WebKit binaries and system libraries. Verify registry access, container networking, and Linux process-supervision support. Preparation is not proof that any gate passed. This plan does not authorize opportunistic runtime installation during a read-only audit.

**C. After integration, verify a clean committed source checkpoint.** Run from the repository root; first inspect and resolve any nonempty status through normal integration, preserving user changes. Do not run the following block on a dirty tree.

```bash
git status --short
git rev-parse HEAD
```

Then create the minimal identity and evidence parent outside the repository. The runner must create `phase1-evidence` itself:

```bash
phase1_checkpoint_dir="$(mktemp -d /tmp/telos-phase1-checkpoint.XXXXXX)"
python3 - "$phase1_checkpoint_dir/source-checkpoint.json" <<'PY'
import json
from pathlib import Path
import subprocess
import sys
commit = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
Path(sys.argv[1]).write_text(json.dumps({"schemaVersion": 1, "sourceCommit": commit}) + "\n")
PY
python3 scripts/run-gates.py \
  --catalog ci/phase-gates.json --scope local --phase 1 \
  --candidate-lock "$phase1_checkpoint_dir/source-checkpoint.json" \
  --evidence-dir "$phase1_checkpoint_dir/phase1-evidence"
```

Record the actual runner exit, HEAD, catalog digest, source identity file, logs, envelopes, and tool prerequisites. Preserve that directory; every rerun uses a new one. `--phase 1` avoids later placeholders and does not select native builds or external certification. Only all required selected gates `passed` makes this a completed source checkpoint. An unavailable host/tool is a gap; do not mark the phase complete. The current runner does not magically discover tools hidden inside every shell wrapper, so a missing nested command may emerge as `failed`/127 rather than `not_run`; preflight and report the concrete cause without relabeling it success.

## Exit and next handoff

- [ ] Tasks 1–6 have current source/fixture results, and the residual catalog/CI changes have reviewable diffs.
- [ ] Corrected TypeScript behavior and actual backend/frontend/Compose/Playwright gates ran on the prepared host; CI enforced them.
- [ ] The integrated source checkpoint is clean and all required Phase 1 source outcomes pass. Earlier logs remain labeled earlier; no synthetic release candidate was created.
- [ ] Handoff names the reviewed commit and next task: Phase 2 Ubuntu browser-node work. Hosted browser delivery and all later external evidence remain blocked until their owning phases actually run.

**Immediate next step for a cold implementer:** inspect current diffs and prerequisites, run the available direct checks, then implement Task 3's catalog/TypeScript regressions and Task 4's platform coverage before Task 6's CI changes. Do not restart evidence-envelope or gate-runner implementation.
