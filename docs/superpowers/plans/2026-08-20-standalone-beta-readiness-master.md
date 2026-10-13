# Standalone Beta Readiness Master Implementation Plan

**Remediated:** 2026-10-13. Start with the [cold implementation handoff](2026-10-13-standalone-beta-cold-handoff.md); it identifies the actual baseline, first task, execution order and resume ledger.

**Goal:** Ship an invite-only Telos beta that lets a community own its media, files and conversation, with a repeatable installation and proven recovery.

**Architecture:** Preserve the headless Go gateway and isolated upstream services. Traefik serves one HTTPS origin, routing API traffic to core and browser routes to a separate static Next.js client container. Linux Tauri uses the same frontend with device authentication and Secret Service credentials. Rootful Podman hosts non-root application containers; persistent configuration/state is independent of immutable release generations.

**Tech Stack:** Existing Go, PostgreSQL, Redis, Next.js/React/TypeScript, Tauri/Rust, Bash, Python and Podman locks. Use exact repository pins and verify host tools; documentation does not prove a tool is installed.

**Spec:** [Readiness design](../specs/2026-08-20-standalone-beta-readiness-design.md). **Shared interfaces:** [Handoff contracts](2026-10-13-standalone-beta-handoff-contracts.md).

## Global constraints

- Server compatibility is Ubuntu 24.04+ only, initially x86-64 with Podman 5+. Certify 24.04 and verify newer combinations explicitly. Other Linux distributions, Windows/macOS/WSL server hosting are excluded.
- Initial clients are hosted browsers and Linux x86-64 desktop. Windows and Mac native clients are future work; native mobile is deferred. Browser checks still include actual desktop Safari, iOS Safari and Android Chrome, plus desktop Chrome/Firefox/Edge and automated WebKit.
- Follow the [platform policy](../../../documentation/product/platform-scope.md). Preserve four modules (Chat, Stream, Library, Files), Settings and cross-media commentary. Do not revive removed features.
- The Bash `telos` CLI is authoritative. The Go CLI remains experimental. Keep HTTP process boundaries around Jellyfin and Grimmory.
- Applied migrations are immutable; add new numbered migrations after inspecting current history (baseline ends at 0023). Never regenerate an existing node's secrets. Never hand-edit frontend token/foundation files.
- Credentials belong in protected node environment/credential files or mounted secrets, never argv, logs, evidence or artifacts. Production paths and identities are fixed by shared contracts.
- Preserve working fail-closed gate machinery. Missing prerequisites produce `not_run`, which blocks certification. Do not alter dependencies, host packages, policy or assertions just to produce green output.
- Implement bounded behavior changes with focused regression tests and relevant phase checks. Source work can proceed without external release resources; unavailable drills remain explicit.
- Freeze only after all implementation and release tooling are integrated into a clean reviewed source commit. Candidate fixes require a new candidate, never replacement of tested bytes.

## Plan family

| Plan | Work and local exit | Candidate proof |
|---|---|---|
| [Phase 1 — truth and gates](2026-08-20-standalone-beta-phase-1-truth-and-gates.md) | Reverify existing envelope/runner, fix residual catalog/product/CI gaps | All source gates pass on candidate source |
| [Phase 2 — clean Linux deployment](2026-08-20-standalone-beta-phase-2-clean-linux-deployment.md) | Static client, routing, persistent config/secrets, validated upstream setup | Clean Ubuntu node reaches all four modules over trusted HTTPS |
| [Phase 3 — Linux desktop](2026-08-20-standalone-beta-phase-3-desktop-client.md) | Device auth/CORS/refresh, Secret Service, platform TLS, media/images, packaging | Exact Linux artifact passes real install/restart/auth/media journeys |
| [Phase 4 — operational safety](2026-08-20-standalone-beta-phase-4-operational-safety.md) | Image locks, hardening, monitoring, encrypted recovery, generation lifecycle, drills/scans | Observed hardening, recovery, upgrade/rollback, capacity, exposure, egress and supply-chain evidence |
| [Phase 5 — candidate certification](2026-08-20-standalone-beta-phase-5-candidate-certification.md) | Schemas/runner/builders early; collectors, signing and promotion tooling | One frozen candidate, complete fresh evidence, verified promotion |

**Execution is not the numeric phase order.** Reconcile Phase 1; implement Phase 5 Task 1a and Task 4a; complete Phase 2 while implementing Phase 5 Task 2a builder support; complete Phase 3/4 and remaining Phase 5 tooling; then freeze and certify under Phase 5 Task 6. This removes the former dependency cycle between operations and release tooling. The [handoff checkpoint table](2026-10-13-standalone-beta-cold-handoff.md#execution-order-and-exit-meanings) is authoritative.

## Completion criteria

Local implementation completion requires focused and phase regression checks with actual results, coherent producer/consumer interfaces and documented external availability. It does not require pretending an unavailable live drill passed.

Beta completion requires one frozen lock and exact artifacts, with all required catalog gates except the aggregate's own output present exactly once as fresh `passed` records. The aggregate subsequently verifies and signs certification. Guided browser/device assertions must be complete and signed. Separate-host restore meets RPO ≤24 hours and RTO ≤4 hours; backup retains 14 daily and 8 weekly snapshots. Capacity supports 100 registered members, 25 concurrent authenticated users and 10 chat/commentary events per second under Phase 4's measured thresholds.

Publishing promotes already-certified bytes. No later rebuild, invented predecessor, copied fixture result, stale runtime observation or missing browser device can satisfy this boundary.

## Review focus and handoff

Review the shared JSON/path/authentication contracts before task implementation. Particularly inspect generation/hash cycles, observed-versus-declared identity, sensitive attachments, token refresh failure behavior and source-versus-candidate exits. Detailed phase tasks own exact files, test assertions and commands. Record each completed packet in the handoff's progress ledger so another agent can resume without conversation history.
