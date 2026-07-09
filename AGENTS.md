# Telos — Agent Guide

This is a documentation-first repository for Telos. It contains no application code at present; these documentation files serve as the buildable specification for the platform. The project is guided by two core slogans: "your server, your community" (community-facing) and "be on the net, but not of the net" (philosophical/technical).

---

## 1. Read This First

Refer to this matrix to find the appropriate entry point for your development intent:

| Intent | Entry Point |
|---|---|
| **Build the Frontend** | [`documentation/design/06-design-prompts.md`](./documentation/design/06-design-prompts.md) (ready-to-paste prompts) followed by the specs in [`documentation/design/01-brand-identity.md`](./documentation/design/01-brand-identity.md) through [`documentation/design/05-reference-implementation.md`](./documentation/design/05-reference-implementation.md) |
| **Deploy or Manage Infrastructure** | [`documentation/architecture/01-system-overview.md`](./documentation/architecture/01-system-overview.md) through [`documentation/architecture/03-gateway-and-api.md`](./documentation/architecture/03-gateway-and-api.md) |
| **Manage Frontend State & WebRTC** | [`documentation/architecture/04-frontend-architecture.md`](./documentation/architecture/04-frontend-architecture.md) |
| **Understand the Roadmap & Licensing** | [`documentation/architecture/05-roadmap-and-licensing.md`](./documentation/architecture/05-roadmap-and-licensing.md) |
| **Browse the Full Index** | [`documentation/README.md`](./documentation/README.md) |

---

## 2. Build Order

When implementing the system, execute the development phases in this sequence:
1. **Phase 1: Chat Core** — Establish PostgreSQL schemas, Redis cache prefixes, and the WebSocket core gateway.
2. **Phase 2: Storage & Media** — Mount shared volumes, set up headless Jellyfin, and integrate the custom HLS.js streaming player.
3. **Phase 3: Catalog** — Build Grimmory integration, watch directory routines, and e-book reader controls.
4. **Phase 4: Real-Time** — Provision the LiveKit server and hook up the Zustand voice session store.

---

## 3. Hard Constraints Digest

Every implementing agent must strictly comply with these core rules:
- **Grayscale + Single Accent:** Grayscale neutrals and white body surfaces with exactly one accent color emphasis per visual region (Sovereign Blue in light/dark themes; hot pink in the vaporwave theme).
- **Three Themes:** The interface supports light, dark (default), and vaporwave themes toggled via a three-state control (Sun, Moon, Waves icons) on the root element.
- **Flat Rule:** Light and dark themes are strictly flat with no box-shadows, no blurred backdrops, and no gradients. The only exceptions are the vaporwave theme's neon glow (CSS `box-shadow` property form only) and the pink-to-cyan gradient on the vaporwave logo.
- **Iconography:** Lucide icons exclusively. No emojis are permitted anywhere.
- **Semantic States:** Convey states structurally using icons, text weights, and user copy (with double confirmation on irreversible actions), never through extra status colors.
- **Secrets:** All credentials must be sourced from `.env` interpolation. Never commit hardcoded secrets.
- **Copyleft Boundary:** Never link or compile Jellyfin or Grimmory code directly into the Telos core gateway. Maintain strict containerized process boundaries.

---

## 4. Verification Suite

Run this plain shell verification command block to validate your changes:

```bash
# 1. Search for citations, TO-DOs, or temporary placeholders
grep -rn "ci""te:" documentation/ AGENTS.md ; echo "exit=$?"
grep -rnE "TO""DO|TB""D" documentation/ AGENTS.md ; echo "exit=$?"

# 2. Verify flat rule compliance (no banned shadow or blurred backdrop classes)
grep -rnE "shadow-[a-z0-9]|backdrop-""blur" documentation/ AGENTS.md ; echo "exit=$?"

# 3. Verify color compliance (no forbidden color classes in unauthorized specs)
grep -rnE "cy""an-|eme""rald|am""ber|ro""se-" documentation/ AGENTS.md | grep -v "02-design-tokens.md" ; echo "exit=$?"
```

---

## 5. Assets Reference

- Canonical logo renders are located at [`resources/Telos_1.png`](./resources/Telos_1.png), [`resources/Telos_2.png`](./resources/Telos_2.png), and [`resources/Telos_3.png`](./resources/Telos_3.png).
- The expected path for the owner-supplied vaporwave logo variant is `resources/Telos_vaporwave.png` (implementations must fall back to the monochrome mark when this asset is not present).
