# Telos Documentation Rewrite — Design

**Date:** 2026-07-08
**Status:** Approved (structure, UX decisions, and architecture decisions each approved in session)
**Scope:** Documentation only. No application code is produced by this work.

## Context

The project currently holds two documents produced by an AI research/export
pipeline: `documentation/design.md` (frontend UI/UX spec) and
`documentation/implementation.md` (systems architecture blueprint), plus three
canonical logo renders in `resources/`. Both documents suffer from:

- Collapsed formatting: headings, lists, and tables flattened into single
  run-on lines.
- `[cite: N]` research artifacts embedded in prose **and inside YAML/TypeScript
  code blocks**, making those blocks syntactically invalid.
- Internal contradictions (single-accent color rule vs. reference code using
  emerald/amber/rose; "sky-500 or cyan-500" ambiguity; SVG `<path>`-only rule
  vs. `<text>` element in the logo snippet).
- Technical errors (Docker networks that Traefik cannot route across, LiveKit
  media ports published on the wrong container, hardcoded secrets, outdated
  livekit-client API usage, invalid Traefik header templating).
- Missing UX coverage (accessibility, responsive behavior, semantic states
  under a strict monochrome palette).

External facts were verified on 2026-07-08: BookLore was withdrawn by its
maintainer in March 2026; **Grimmory** (`github.com/grimmory-tools/grimmory`,
AGPL-3.0) is its community successor. Official image `grimmory/grimmory`
(alt: `ghcr.io/grimmory-tools/grimmory`), port 6060, env vars `USER_ID`,
`GROUP_ID`, `DATABASE_URL` (JDBC/MariaDB), `DATABASE_USERNAME`,
`DATABASE_PASSWORD`, `API_DOCS_ENABLED`, `DISK_TYPE`; volumes `/app/data`,
`/books`, `/bookdrop`; healthcheck `GET /api/v1/healthcheck`.

## Decision

Full rewrite of both documents into a split, numbered documentation set. The
originals are preserved in git history (root commit `8bf9c24`) and deleted
from the working tree once the new set lands.

## Target structure

```
documentation/
├── README.md                          — project vision, quick facts, doc map
├── design/
│   ├── 01-brand-identity.md           — philosophy, slogans, ouroboros logo spec
│   ├── 02-design-tokens.md            — palette, typography, icons, radii,
│   │                                    semantic states without semantic color
│   ├── 03-application-shell.md        — 4-pane layout, header, nav, responsive
│   │                                    behavior, accessibility
│   ├── 04-modules.md                  — chat/voice, stream, books, files, manifesto
│   └── 05-reference-implementation.md — corrected reference React shell
└── architecture/
    ├── 01-system-overview.md          — paradigm, diagram, service inventory
    ├── 02-deployment.md               — docker-compose (fixed networks/ports),
    │                                    .env-driven secrets, .env.example
    ├── 03-gateway-and-api.md          — Traefik config, API matrix, OIDC flow
    ├── 04-frontend-architecture.md    — Zustand voice store, persistent shell
    └── 05-roadmap-and-licensing.md    — phases, risks, GPL/AGPL compliance, credits
```

Audience note carried through every file: these documents are specs intended
to be consumed by AI coding agents as well as humans — unambiguous, imperative,
internally consistent, one concern per file.

## UX/UI decisions (design/ set)

1. **Canonical accent.** "Sovereign Blue" is exactly `#0EA5E9` (Tailwind
   `sky-500`); badge/wash form is `sky-400` text on `sky-500/10`. All `cyan-*`
   usages and the "or cyan-500" ambiguity resolve to `sky-*`.
2. **Semantic states without semantic color.** The grayscale + single-blue
   rule stands. New pattern section defines: destructive = stark-white
   emphasis + confirmation step; error = icon + monospace detail text;
   live/voice = pulsing Telos Blue indicator; success = transient check icon.
   Reference code must comply (no emerald/amber/rose anywhere).
3. **Accessibility section.** WCAG AA contrast pairs for the palette,
   `focus-visible` rings in Telos Blue, keyboard navigation map for the
   4-pane shell, `prefers-reduced-motion` handling.
4. **Responsive behavior.** ≥`xl`: full 4-pane. <`xl`: SLA sidebar hidden.
   <`lg`: contextual sidebar becomes overlay drawer. <`md`: left nav strip
   becomes bottom tab bar.
5. **Logo spec.** Ouroboros as in `resources/Telos_[1-3].png` (canonical
   renders). Inline SVG must use outlined `<path>` data for the wordmark —
   no SVG `<text>` element. Monochromatic, scales with `currentColor`.
6. **Reference shell.** Rewritten with valid Tailwind classes only (e.g., no
   `z-25`), palette-compliant, explicitly framework-agnostic structural
   reference.

## Architecture decisions (architecture/ set)

1. **Network topology fix.** Jellyfin, Grimmory, and LiveKit join
   `telos-ingress` in addition to `telos-backend` so Traefik can route to
   them. `telos-db` remains strictly internal. Three-network model retained.
2. **LiveKit media transport fix.** UDP 50000–50100, UDP 3478 (STUN/TURN),
   and TCP 7881 (fallback) are published on the `livekit` service, not
   Traefik. Traefik proxies only HTTP/WS signaling under `/livekit`. Remove
   the dead `livekit-webrtc` entrypoint, the invalid
   `X-Forwarded-For: ${remote-ip}` header, and wildcard CORS (scope to app
   origin).
3. **Secrets.** All credentials become `${VAR}` compose references backed by
   a documented `.env.example`. Obsolete `version: '3.8'` key removed. All
   `[cite: N]` artifacts stripped everywhere.
4. **Grimmory alignment.** Use verified upstream details (see Context) and
   real repository links.
5. **Frontend code modernization.** Current livekit-client API
   (`remoteParticipants`, `audioCaptureDefaults`), correct imports
   (`ConnectionState`), call-bar styling per design system.
6. **Retained content.** API integration matrix, OIDC/SSO provisioning flow,
   phased roadmap, and GPL/AGPL process-separation analysis are kept, cleaned,
   and reformatted into proper markdown tables/sections.

## Content mapping (old → new)

| Original section | Destination |
|---|---|
| design.md §1 Brand Identity & Philosophy | design/01-brand-identity.md |
| design.md §2 Global Design Tokens | design/02-design-tokens.md |
| design.md §3 Global Layout Architecture | design/03-application-shell.md |
| design.md §4 Module Specifications (A–E) | design/04-modules.md |
| design.md §5 Reference Code | design/05-reference-implementation.md |
| impl.md Executive Summary + diagram | architecture/01-system-overview.md |
| impl.md Phase 1 (compose, networks, volumes) | architecture/02-deployment.md |
| impl.md Phase 2 (gateway, API matrix, OIDC) | architecture/03-gateway-and-api.md |
| impl.md Phase 3 (Zustand, shell) | architecture/04-frontend-architecture.md |
| impl.md Phase 4 (roadmap, legal) | architecture/05-roadmap-and-licensing.md |

## Out of scope

- No application code, scaffolding, or docker files outside `documentation/`.
- No changes to `resources/` images.
- No new product features beyond documenting the gaps listed above.

## Verification

- Every fenced YAML block parses (`python -c "import yaml; ..."` or
  equivalent); every fenced TS/TSX block is free of `[cite:` artifacts and
  balanced braces (spot-check by reading).
- `grep -rn "cite:" documentation/` returns nothing.
- `grep -rniE "emerald|amber|rose-|cyan-" documentation/` returns nothing
  (except prose explicitly listing forbidden colors).
- All internal cross-links between the new files resolve.
- Old `design.md` / `implementation.md` removed; originals reachable via
  `git show 8bf9c24`.
