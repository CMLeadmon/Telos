# Telos standalone beta: cold implementation handoff

**Remediated:** 2026-10-13. **Baseline:** `237692fc5da5654f95d1582cd1789b6491d22283` plus the reviewed, currently uncommitted platform-policy and plan remediation changes. This is an implementation handoff, not a release certificate.

**Goal:** Deliver an invite-only, self-hosted community library where members can chat, watch/listen, read, store files and attach commentary on a server their community controls.

**Architecture:** Preserve the existing Go gateway, PostgreSQL/Redis, isolated Jellyfin/Grimmory services and shared Next.js client. Finish their deployment, authentication, media and operations seams. Serve browser assets from a separate static container; package the same client in Linux Tauri. Use immutable releases and observed evidence to prove installation and recovery.

**Tech stack:** Existing committed Go/npm/Cargo/tool locks; rootful Podman 5+ on Ubuntu 24.04+ x86-64. Python/Bash release tooling. No rewrite or new infrastructure platform is authorized by this plan.

**Spec:** [Readiness design](../specs/2026-08-20-standalone-beta-readiness-design.md). **Global constraints:** [Platform policy](../../../documentation/product/platform-scope.md), [shared contracts](2026-10-13-standalone-beta-handoff-contracts.md), repository `AGENTS.md` and subtree instructions. **Review focus:** completing real missing integration behavior without recreating working gate machinery, accepting mock evidence, or broadening platform scope.

## Read this first

1. Read `AGENTS.md`, `CLAUDE.md`, and `frontend/AGENTS.md` before frontend edits. Treat code as evidence of *current* behavior; the platform policy and this plan define the requested *target*. Do not use historical “code wins” language to preserve a defect this plan explicitly fixes.
2. Read the platform policy, this document and shared contracts in full.
3. Read the [master](2026-08-20-standalone-beta-readiness-master.md) and only the detailed phase for the next task. Other historical split plans are background, not competing checklists.
4. Inspect actual referenced files before changing them. Line numbers are navigational hints from the baseline, not immutable locations.

The checked-out review workspace is `/home/carter/Projects/Telos`. A clone of GitHub alone does **not** include these uncommitted handoff changes. Before transferring work, preserve the reviewed diff and new files in a reviewed commit or an explicit patch bundle including untracked policy documents/tests. Record the resulting baseline commit in the execution ledger. Never reset, clean, blanket-stash or overwrite unrelated work to obtain a clean tree.

## Current state and confidence

| Area | What exists | What remains |
|---|---|---|
| Product | Four modules, commentary, auth, migrations, media integrations, standalone split | Prove the assembled user journeys on a distributable candidate |
| Gate foundation | Envelope v2, strict catalog v1, process containment, fail-closed wrappers, structural truth checks | Reconcile residual Phase 1 gaps; explicitly migrate runner for artifact/runtime evidence |
| Browser deployment | Static-export frontend and headless gateway | Separate client image/service, routing, persistent secrets/configuration and guided integrations |
| Linux desktop | Tauri shell and device credential primitives | Registration route, bearer/CORS/refresh integration, Secret Service, trusted TLS, images/media, real packaging/journeys |
| Operations | Scripts and policy foundations | Real encrypted recovery, safe generations/upgrade, observed hardening, load/exposure/egress and supply-chain verification |
| Release | Partial schemas and fail-closed commands | Acyclic graph, builder, collectors, signatures, exact-candidate certification and promotion |

This is a substantial existing application with unfinished delivery infrastructure. The plan is feasible because it closes identified interfaces rather than replacing the product. The highest uncertainty is operational integration with real upstreams, host runtimes, browsers and recovery environments; those are explicit checkpoints, not hidden assumptions. No completion date or release readiness is asserted here.

Fresh review-host checks on 2026-10-13 passed envelope fixtures, fail-closed certification fixtures, runner/CI fixtures, accumulated-suite fixtures, six platform-policy mutations, product/release truth and documentation hygiene. Product mutation tests could not complete because Node is absent. The host is Ubuntu 26.04.1 and lacks Node/npm/npx, Go, Podman and Docker; it cannot certify the Ubuntu 24.04 runtime, desktop or browser matrix. Cargo/rustc availability alone is not a native-client pass.

## First implementation session

Run read-only preflight from the repository root:

```bash
pwd
git status --short
git rev-parse HEAD
git log -1 --format='%H %s'
cat /etc/os-release
python3 --version
bash --version
command -v node npm npx go podman cargo rustc
```

The last command may return nonzero when tools are missing. Record availability, do not substitute fixture tool-version values for installed binaries. Read Phase 1's verification section and run its available direct fixture tests. Do not invoke a final-candidate gate with a fabricated lock. Phase 1 documents a temporary source-only identity for committed local checkpoints; it cannot certify a release.

**First production change:** [Phase 1 Task 3](2026-08-20-standalone-beta-phase-1-truth-and-gates.md), the TypeScript compiler working-directory regression and catalog correction. Then Task 4 platform enforcement and Task 6 missing CI coverage. Tasks 1–2 already have implementations: verify and preserve them. If baseline inspection shows a residual already fixed, record the exact test/commit and move on rather than rewrite it.

## Execution order and exit meanings

Task IDs below are stable (`P2-T4` means Phase 2 Task 4). A subpart is a scheduling distinction inside its existing task, not permission to omit its assertions.

| Checkpoint | Work in order | Exit permits |
|---|---|---|
| A — reconcile baseline | P1-T1 through T6, focusing residuals | Existing truth/gate foundation verified; start shared release contracts |
| B — establish interfaces | P5-T1a strict graph/result schemas, then P5-T4a catalog/runner migration | Downstream producers can consume stable records; no signing key needed for fixtures |
| C — complete browser node | P2-T1 through T7 local implementation; P5-T2a builder tooling as its inputs land | Server/API/configuration contracts stable; local tests pass; record live rehearsal availability |
| D — complete product and operations | P3-T1 through T7; P4-T1 through T9; P5-T2a/3/4b/5 tooling | Integrated source, local regressions and non-certifying rehearsal reports ready |
| E — freeze | P5-T1b real trust enrollment; P5-T6 prerequisites; clean reviewed source/tag and one final build | One immutable candidate lock and exact artifact set |
| F — prove and publish | P5-T6 local and external candidate gates, import/sign/certify, reviewed promotion | Beta release only when every required input is fresh and passed |

Within C/D, follow task order and explicit producer/consumer prerequisites. Phase 3 consumes Phase 2 routing/configuration, but owns registration/CORS itself; do not wait for Phase 2 to implement those. Phase 4 consumes Phase 2 layout and early Phase 5 schemas/builder. Phase 5 tooling can use tiny fixtures before product artifacts exist; its final build must wait for **all** source/tooling changes. P3 and P4 may overlap after C if workspace ownership is isolated, but parallel execution is optional.

A local implementation exit means required source/fixture tests pass and external availability is honestly recorded. A rehearsal exercises disposable infrastructure using `recordKind=rehearsal`; it never enters the final evidence set. A release exit requires real candidate-bound external evidence. Missing live inputs do not block independent coding; they do block the affected drill and release certification.

## External inputs to arrange early

Use the exact [shared prerequisite list](2026-10-13-standalone-beta-handoff-contracts.md#8-external-prerequisite-records). Particularly: verified Podman 5+ on Ubuntu 24.04, two distinct recovery nodes, trusted HTTPS/DNS, off-node restic, legal media, real Safari/mobile browsers, Linux Secret Service, signing/publishing identities and a buildable predecessor.

Phase 2 bounds the Podman provisioning and pinned Grimmory API verification work with explicit artifacts. If either cannot be verified, leave that live dependency unavailable with a corrective reason; do not invent package instructions or response fields. A first release still needs an honest predecessor/upgrade rehearsal input under Phase 4; arbitrary synthetic version history is not proof.

## Work packet and resume ledger

Create `docs/implementation/standalone-beta-progress.md` in the first implementation commit. Each packet should change one coherent interface or behavior and end with its focused assertions, relevant phase regressions and a reviewable commit. Split large Tasks 3–5 into their numbered test/implementation steps if needed; do not attempt a whole phase in one context window.

Use this record after every packet:

```text
Task/subpart:
Baseline commit and working-tree exceptions:
Implemented files and interface:
Checks: exact command, exit code, sanitized output/artifact path
External status: not needed / rehearsal passed / unavailable with reason
Candidate status: not frozen / exact lock digest (never infer)
Known failures and affected downstream tasks:
Commit(s):
Next task and first command:
```

Do not mark an unchecked task complete because the files exist. Distinguish implemented, locally verified, rehearsed, and candidate certified. A failure outside the packet stays visible; never relax an assertion, fabricate a key/digest, edit applied migration history, regenerate existing node secrets, or install unrelated dependencies to make a check green.

Stop the **dependent action** if an interface conflicts with the shared contract, an external destructive target is undeclared, a real credential/trust input is missing, or a frozen candidate needs a source fix. Resolve source conflicts with a small contract/test change; record external blockers and continue independent work. Publication and destructive live operations require the authorized target/identity described by their tasks; plan review itself authorizes neither production destruction nor publishing.

## Copy/paste implementation assignment

> Implement the Telos standalone beta plan starting at `docs/superpowers/plans/2026-10-13-standalone-beta-cold-handoff.md`. Read repository instructions, platform policy and shared contracts first. Inspect current HEAD and preserve all existing work. Reconcile Phase 1 instead of recreating it; begin with the documented residual task. Follow checkpoints A–F, including early Phase 5 schema/runner/builder tooling before dependent rehearsals. Keep the progress ledger current, with exact checks and next action. Server scope is Ubuntu 24.04+ only; initial clients are browsers and Linux desktop. Do not implement Windows/macOS server support or require deferred native clients. Keep rehearsal and candidate evidence separate; missing external inputs are unavailable, never passed. Complete independent source work, and report concrete blockers for remaining live certification. Do not claim a beta release until one immutable candidate satisfies all gates.

## Remediation audit

| Original planning defect | Remedy / owner |
|---|---|
| Phase 1 described absent code that already exists | P1 reconciles implementation and fresh verification separately |
| Phase 4 needed a builder that Phase 5 could not start yet | Early P5-T1a/T4a/T2a; final freeze remains last |
| Generation/SBOM embedded downstream candidate digest | Acyclic graph and external installed receipt; P5-T1/P4-T6 |
| New catalog fields rejected by existing runner | Explicit catalog2 migration preserving envelope2; P5-T4a |
| Producers/runner both wrote evidence, runtime identity was declarative | One runner envelope; validated result/observed subject sidecars |
| Secrets/config tied to replaceable source checkout | Persistent node path, common helper, fixed ownership; P2-T3/P4-T6 |
| Ubuntu scope banner contradicted native build/CI bodies | Initial Linux desktop only throughout active phases; policy checks |
| Native login/media plan missed refresh/images and capability limits | P3 auth lifecycle, authenticated artwork, revocation-bound bearer URLs |
| Recovery lacked crash/locking/identity details | P4 journals, lock inheritance, cleanup, same-host versus separate-host proof |
| Signing/browsers/hosts appeared as implicit prerequisites | Explicit external inventory, fixture/rehearsal/candidate states |

The five detailed plans remain the executable task bodies. This handoff and shared contracts replace conflicting sequencing/interface statements in earlier master/spec versions; they do not mark product features or release gates complete.
