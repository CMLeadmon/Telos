# Telos Documentation

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

> **⚠ These specs predate most of the code, and every `architecture/*.md` file carries its own "Superseded in part" banner.** They remain the statement of intent; they are not a description of the running system. **Where code and docs disagree, the code wins** — flag the discrepancy rather than following the doc. For what is actually built, start at [`../CLAUDE.md`](../CLAUDE.md).
>
> The **less-is-more redesign (2026-07)** removed voice rooms, LiveKit, Watch Parties, the notification inbox, and My List. Any reference to those below is historical.

## 1. Vision

Telos merges Discord-style chat, Jellyfin-style streaming, Grimmory-style publication management, and file management into one self-hosted, single-origin application. The system serves as an infrastructure of digital sovereignty, guided by the slogans "your server, your community" and "be on the net, but not of the net."

---

## 2. Quick Facts

| Parameter | Specification |
|---|---|
| **Default Host** | `telos.local` |
| **Modules** | Chat · Stream · Library · Files, plus Settings |
| **Tech Stack** | Go 1.26.5 core gateway + React 19 / Next.js 16 / TypeScript / Zustand client |
| **Headless Services** | Traefik v3, PostgreSQL 16.14, Redis 7, Jellyfin, Grimmory, MariaDB 10.11, ClamAV, Squid (controlled egress) |
| **Operator Entry Point** | `./telos` CLI (`install.sh` → `telos doctor` / `start` / `status` / `stop`) |
| **Theming System** | Synthwave (default) / Ink |
| **License** | Apache-2.0 for the Telos gateway/client (see `LICENSE`, `NOTICE`, `CREDITS.md`); isolated services run under their respective licenses (GPL/AGPL/MIT/etc.) |

---

## 3. Document Map

### Technical Architecture Specification

| Document | Purpose |
|---|---|
| [`architecture/01-system-overview.md`](./architecture/01-system-overview.md) | Suite-orchestration paradigm, network ports, volume paths, and system topology. |
| [`architecture/02-deployment.md`](./architecture/02-deployment.md) | Secret interpolation schema, `.env.example` configurations, and multi-network Compose files. |
| [`architecture/03-gateway-and-api.md`](./architecture/03-gateway-and-api.md) | File-provider ingress, upstream credential boundaries, and core REST API matrices. |
| [`architecture/04-frontend-architecture.md`](./architecture/04-frontend-architecture.md) | Zustand real-time stores and persistent app shell integrations. *(Its LiveKit/voice sections describe removed features.)* |
| [`architecture/05-roadmap-and-licensing.md`](./architecture/05-roadmap-and-licensing.md) | Integrated roadmap phases, mitigation strategies, copyleft boundary analysis, and service attributions. |

### Product

| Document | Purpose |
|---|---|
| [`product/beta-feature-status.md`](./product/beta-feature-status.md) | What is actually shipped vs. planned. Enforced by `scripts/check-product-truth.sh`. |

### Operations Runbooks

| Document | Purpose |
|---|---|
| [`operations/install.md`](./operations/install.md) | Node installation. |
| [`operations/backup-and-restore.md`](./operations/backup-and-restore.md) | Backup contents, retention, destructive restore, verification checklist. |
| [`operations/restore-drill.md`](./operations/restore-drill.md) | Rehearsed restore procedure. |
| [`operations/upgrade-and-rollback.md`](./operations/upgrade-and-rollback.md) | Version upgrade and rollback paths. |
| [`operations/release.md`](./operations/release.md) | Release staging and cutting. |
| [`operations/incidents.md`](./operations/incidents.md) | Incident response. |
| [`operations/monitoring.md`](./operations/monitoring.md) | Metrics and alerting. |
| [`operations/capacity.md`](./operations/capacity.md) | Capacity limits and long-stream leases. |
| [`operations/data-retention.md`](./operations/data-retention.md) | Retention matrix and account-deletion semantics. |
| [`operations/network-exposure.md`](./operations/network-exposure.md) | What is reachable from where. |
| [`operations/controlled-egress.md`](./operations/controlled-egress.md) | Squid deny-by-default outbound proxy. |
| [`operations/container-hardening.md`](./operations/container-hardening.md) | Runtime hardening baseline. |
| [`operations/dependency-security-baseline.md`](./operations/dependency-security-baseline.md) | Pinned dependency + vulnerability posture. |
| [`operations/repository-baseline.md`](./operations/repository-baseline.md) | Workspace classification and tracked-file policy. |
| [`operations/clean-checkout-verification.md`](./operations/clean-checkout-verification.md) | The build+boot gate from a clean clone. |
| [`operations/api-list-inventory.md`](./operations/api-list-inventory.md) | Every paginated list endpoint and its cursor policy. |
| [`operations/audiobook-migration.md`](./operations/audiobook-migration.md) | Audiobook catalog migration operations. |
| [`operations/beta-certification.md`](./operations/beta-certification.md) | Beta exit criteria. |
| [`operations/browser-certification.md`](./operations/browser-certification.md) | Supported browser matrix. |

*(`operations/release-inputs.pathspec` is machine-read by the release tooling, not prose.)*

### Design History *(informative)*

`../docs/superpowers/specs/` and `../docs/superpowers/plans/` hold the design spec and execution plan for each feature program — the beta-readiness, less-is-more, and member-experience work. Read these to learn **why** something is the way it is.

---

## 4. Reading Order

- **To change code:** start at [`../CLAUDE.md`](../CLAUDE.md), then [`../AGENTS.md`](../AGENTS.md) for constraints and gates.
- **For Infrastructure & Operations:** study the system layout in [`architecture/01-system-overview.md`](./architecture/01-system-overview.md), then the Compose configurations in [`architecture/02-deployment.md`](./architecture/02-deployment.md), then the relevant runbook above.
- **Logo Renders:** the canonical monochromatic logo renders reside at `resources/logos/Telos_sun_ink.svg`; the vaporwave/synthwave variant is at `resources/logos/Telos_sun_synthwave.svg`.

---

## 5. History Note

*(informative)* This structured documentation set supersedes the original monolithic `design.md` and `implementation.md` files, which are preserved for historical reference in the repository's git history.
