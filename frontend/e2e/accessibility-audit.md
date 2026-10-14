# Telos Accessibility Audit Checklist (WCAG 2.2 AA)

**This is a checklist, not a certification.** An earlier version of this file claimed full conformance with no run
behind it; that claim has been withdrawn. No row below is marked as passing because nothing recorded
here proves it. Evidence about conformance comes from candidate runs, never from this document:

| Evidence | Where it comes from |
|---|---|
| axe-core, WCAG 2.2 A/AA rules, serious and critical violations, every shell route at 390 px and 1440 px | the automated `accessibility-axe` journey in `e2e/hosted-node.spec.ts`, run in Chrome, Firefox, Edge and WebKit against the candidate and recorded in each platform's signed record |
| Keyboard-only reach, visible focus, phone and desktop layout, 200 % zoom | the guided `responsive-accessible` question, answered by a person on Safari, iOS Safari and Android Chrome (and the desktop matrix on Linux) |
| Static lint rules (`jsx-a11y` subset in `next/core-web-vitals`) | `npm run lint`, part of CI |

`scripts/check-frontend-quality.py accessibility` (the `frontend-accessibility` gate) only checks that this machinery exists
and covers every route; it does not say the application conforms.

## Routes to audit

`/` (landing), `/login`, `/chat`, `/stream`, `/library`, `/files`, `/settings`, each at 390×844 and 1440×900.

## Per-route checklist (fill in during a real candidate run; keep the filled copy with that run's evidence, not here)

- [ ] Keyboard navigation reaches every control in a sensible order; no keyboard trap
- [ ] Focus is visible; focus returns to the opener when a dialog closes
- [ ] Every control has an accessible name and a correct role; images have text alternatives
- [ ] Text contrast and non-text contrast meet AA in both themes
- [ ] The layout works at 200 % zoom and without horizontal scrolling at 390 px
- [ ] Status and error changes are announced
