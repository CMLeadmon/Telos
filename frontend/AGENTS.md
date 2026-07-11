# Frontend Agent Guide

## Design system is the source of truth
The UI must match the **"Telos Design System"** project on claude.ai/design (sync via the DesignSync tool). `src/styles/tokens/` and `src/styles/foundations/` are imported **verbatim** from that project — do not hand-edit them; change the design project and re-sync instead. App-specific CSS lives in `src/styles/app.css` (viewport shell overrides, auth card, tabbar) and per-module files (`chat.css`, `landing.css`) lifted from the approved mockups.

Two themes, toggled by `data-theme` on `<html>`: `synthwave` (dark, default) and `ink` (light). Theme switching is a token swap only — never a layout change. Flat design law: no shadows, 10px radius, 1px `--line` hairlines.

## Next.js
This Next.js version is newer than model training data — consult `node_modules/next/dist/docs/` before nontrivial Next.js work. Static export (`output: "export"`, `trailingSlash: true`); no server components at runtime, no API routes, no dynamic routes without `generateStaticParams`.

## Layout
- Routes: `/` (landing), `/login`, and `(shell)/` group: `/chat`, `/stream`, `/library`, `/files` sharing `AppShell` (topbar · rail · arena · tabbar).
- Stores (`src/stores/`): `useThemeStore` (persisted), `useAuthStore` (cookie session via `/api/v1/auth/*`), `useChatSessionStore` (channels + chat WS), `useVoiceSessionStore` (LiveKit).
- `src/lib/api.ts` holds the single-origin logic: in `next dev` (port 3000) it targets the gateway on `:8080`; in production everything is relative.

## Commands
```bash
npm run dev      # dev server on :3000 (gateway must run on :8080 for live data)
npm run build    # static export to out/ (embedded by the Go gateway)
npm run lint
npx playwright test   # needs `npm run dev` already running — no webServer in config
```

## Assets
`public/logos/*.png` are processed copies (transparency reconstructed) of `../resources/` originals — the originals have a baked-in checkerboard/white background; don't copy them over these.
