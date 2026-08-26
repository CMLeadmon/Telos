# Frontend Agent Guide

## Design system is the source of truth
The UI must match the **"Telos Design System"** project on claude.ai/design (sync via the DesignSync tool). `src/styles/tokens/` and `src/styles/foundations/` are imported **verbatim** from that project — do not hand-edit them; change the design project and re-sync instead. App-specific CSS lives in `src/styles/app.css` (viewport shell overrides, auth card, tabbar) and per-module files (`chat.css`, `landing.css`) lifted from the approved mockups.

Two themes, toggled by `data-theme` on `<html>`: `synthwave` (dark, default) and `ink` (light). Theme switching is a token swap only — never a layout change. Flat design law: no shadows, 10px radius, 1px `--line` hairlines.

## Next.js
This Next.js version is newer than model training data — consult `node_modules/next/dist/docs/` before nontrivial Next.js work. Static export (`output: "export"`, `trailingSlash: true`); no server components at runtime, no API routes, no dynamic routes without `generateStaticParams`.

Production hosted-browser delivery is blocked until Phase 2 adds `telos-client`. The default backend build is headless and does not require `backend/out/` or a build tag. The static export remains a client build artifact, not an embedded Phase 1 production surface.

## Layout
- Routes: `/` (landing), `/login`, and the `(shell)/` group — `/chat`, `/stream`, `/library`, `/files`, `/settings` — sharing `AppShell` (topbar · rail · arena · tabbar).
- The four capability-gated top-level modules are the `MODULES` array in `src/components/AppShell.tsx`: Chat, Stream, Library, Files. Settings hangs off the topbar, not the module rail. `/library?view=files` is a legacy deep link that redirects to `/files`.
- Stores (`src/stores/`), all Zustand:
  - **Session/shell:** `useAuthStore` (cookie session via `/api/v1/auth/*`), `useThemeStore` (persisted), `usePreferencesStore`, `useSettingsStore`, `useMobileNavStore`
  - **Per-module:** `useChatSessionStore` (channels, chat WS, threads), `useMediaStore`, `useLibraryStore`, `useFilesStore`, `useAnnotationStore` (commentary on any target)
- `src/lib/api.ts` is always single-origin. The development server proxies `/api/*` and its WebSocket upgrades to the loopback gateway. Phase 2 will place the separate `telos-client` browser service and API gateway behind the production origin.
- Mount-time data loaders must be **sync** `useCallback`s using promise chains — `api<T>(...).then(setX).catch(() => setX(fallback))`. The React Compiler lint rule `react-hooks/set-state-in-effect` traces from an effect into an async callback and rejects `setState(await ...)`. See `components/settings/SecuritySection.tsx` for the sanctioned pattern.

## Commands
```bash
npm run dev        # public dev server on :3000; proxies /api/* to the loopback gateway
npm run build      # static export to out/ (future telos-client build input)
npm run lint
npx tsc --noEmit
npm run test:unit  # vitest + node:test
npx playwright test   # needs `npm run dev` already running — no webServer in config
```

The full gate before handing frontend work off:
`npm run lint && npx tsc --noEmit && npm run test:unit && npm run build`.

For access through a hostname other than `localhost`, list each trusted
hostname or IP in comma-separated `TELOS_DEV_ORIGINS` values in the ignored
`.env.development.local`. The development-only gateway loopback bindings are
defined in `../docker-compose.dev.yml`; clients only need :3000.

Serving the app from a non-`localhost` origin also requires
`TELOS_PUBLIC_ORIGIN` to name that origin, or every POST returns 403
`origin_forbidden` while GETs keep working — which presents as "the app is
broken." Keep `http://localhost:8080` in `TELOS_DEV_ORIGINS` so local browsing
retains its writes. Note the static export uses absolute `/_next/...` asset
paths, so it cannot be served under a path prefix.

## Assets
`public/logos/*.svg` are copied from `../resources/logos/` — the original SVGs have clean transparent backgrounds.
