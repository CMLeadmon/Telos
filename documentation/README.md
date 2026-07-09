# Telos Documentation

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

## 1. Vision

Telos merges Discord-style chat/voice, Jellyfin-style streaming, Grimmory-style publication management, and file management into one self-hosted, single-origin application. The system serves as an infrastructure of digital sovereignty, guided by the slogans "your server, your community" and "be on the net, but not of the net."

---

## 2. Quick Facts

| Parameter | Specification |
|---|---|
| **Default Host** | `telos.local` |
| **Tech Stack** | Go/Rust core gateway + React/TypeScript/Zustand client |
| **Headless Services** | Traefik v3.3, PostgreSQL 16, Redis 7, Jellyfin, Grimmory, MariaDB 10.11, LiveKit |
| **Theming System** | Flat light / Flat dark (default) / Vaporwave (classic neon) |
| **Licensing Target** | MIT or Apache-2.0 for custom gateway/client core; child services run under their respective copyleft licenses (GPL/AGPL) |

---

## 3. Document Map

### Design System Specification

| Document | Purpose |
|---|---|
| [`design/01-brand-identity.md`](./design/01-brand-identity.md) | Vision, verbatim slogans, ouroboros logo lockup and SVG composition rules. |
| [`design/02-design-tokens.md`](./design/02-design-tokens.md) | Standard design colors, typography stacks, icon outlines, radii, structural state patterns, and persisted root element theming tokens. |
| [`design/03-application-shell.md`](./design/03-application-shell.md) | Core 4-pane layout, header actions, sidebar specs, responsive tiers, and accessibility keyboard mappings. |
| [`design/04-modules.md`](./design/04-modules.md) | Layouts, items, inputs, and OIDC sign-on behaviors for Chat, Stream, Books, Files, and the Sovereignty Manifesto. |
| [`design/05-reference-implementation.md`](./design/05-reference-implementation.md) | A complete React and Tailwind CSS reference implementation of the global application shell. |
| [`design/06-design-prompts.md`](./design/06-design-prompts.md) | Self-contained, copy-paste prompts optimized for AI coding agents to generate the light, dark, and vaporwave interfaces. |

### Technical Architecture Specification

| Document | Purpose |
|---|---|
| [`architecture/01-system-overview.md`](./architecture/01-system-overview.md) | Suite-orchestration paradigm, network ports, volume paths, and system topology. |
| [`architecture/02-deployment.md`](./documentation/../architecture/02-deployment.md) | Secret interpolation schema, `.env.example` configurations, and multi-network Compose files. |
| [`architecture/03-gateway-and-api.md`](./architecture/03-gateway-and-api.md) | Ingress routing labels, static Traefik parameters, SSO curl examples, and core REST API matrices. |
| [`architecture/04-frontend-architecture.md`](./architecture/04-frontend-architecture.md) | Zustand real-time stores, LiveKit v2 listener connections, and persistent app shell integrations. |
| [`architecture/05-roadmap-and-licensing.md`](./architecture/05-roadmap-and-licensing.md) | Integrated roadmap phases, mitigation strategies, copyleft boundary analysis, and service attributions. |

---

## 4. Reading Order

- **For UI/UX Development:** Begin with the design tokens in [`design/02-design-tokens.md`](./design/02-design-tokens.md), transition to [`design/03-application-shell.md`](./design/03-application-shell.md), and refer to the prompts in [`design/06-design-prompts.md`](./design/06-design-prompts.md) alongside the repository-root [`AGENTS.md`](../AGENTS.md).
- **For Infrastructure & Operations:** Study the system layout in [`architecture/01-system-overview.md`](./architecture/01-system-overview.md), then inspect the Compose configurations in [`architecture/02-deployment.md`](./architecture/02-deployment.md).
- **Logo Renders:** The canonical monochromatic logo renders reside at `resources/Telos_1.png`, `resources/Telos_2.png`, and `resources/Telos_3.png`. The expected path for the custom vaporwave logo variant is `resources/Telos_vaporwave.png`.

---

## 5. History Note

*(informative)* This structured documentation set supersedes the original monolithic `design.md` and `implementation.md` files, which are preserved for historical reference in the repository's git history.
