# Frontend Agent Guide

## Design system is the source of truth
The UI must match the **"Telos Design System"** project on claude.ai/design (sync via the DesignSync tool). `src/styles/tokens/` and `src/styles/foundations/` are imported **verbatim** from that project — do not hand-edit them; change the design project and re-sync instead. App-specific CSS lives in `src/styles/app.css` (viewport shell overrides, auth card, tabbar) and per-module files (`chat.css`, `landing.css`) lifted from the approved mockups.

Two themes, toggled by `data-theme` on `<html>`: `synthwave` (dark, default) and `ink` (light). Theme switching is a token swap only — never a layout change. Flat design law: no shadows, 10px radius, 1px `--line` hairlines.

## Next.js
This Next.js version is newer than model training data — consult `node_modules/next/dist/docs/` before nontrivial Next.js work. Static export (`output: "export"`, `trailingSlash: true`); no server components at runtime, no API routes, no dynamic routes without `generateStaticParams`.

## Layout
- Routes: `/` (landing), `/login`, and `(shell)/` group: `/chat`, `/stream`, `/library`, `/files` sharing `AppShell` (topbar · rail · arena · tabbar).
- Stores (`src/stores/`): `useThemeStore` (persisted), `useAuthStore` (cookie session via `/api/v1/auth/*`), `useChatSessionStore` (channels + chat WS), `useVoiceSessionStore` (LiveKit).
- `src/lib/api.ts` is always single-origin. The development server proxies `/api/*` and its WebSocket upgrades to the loopback gateway; production serves the static export from that gateway directly.

## Commands
```bash
npm run dev      # public dev server on :3000; proxies to loopback gateway/LiveKit
npm run build    # static export to out/ (embedded by the Go gateway)
npm run lint
npx playwright test   # needs `npm run dev` already running — no webServer in config
```

For access through a hostname other than `localhost`, list each trusted
hostname or IP in comma-separated `TELOS_DEV_ORIGINS` values in the ignored
`.env.development.local`. The development-only gateway and LiveKit loopback
bindings are defined in `../docker-compose.dev.yml`; clients only need :3000.

Port 3000 is plain HTTP. Browsers allow microphone capture on HTTP localhost as
a development exception, but not on LAN IPs, Tailscale IPs, public IPs, or
ordinary hostnames. Remote clients can render the development app over port
3000, but voice requires a trusted HTTPS proxy or tunnel. Production users must
use the Traefik-served `https://${TELOS_DOMAIN}` origin.

## Assets
`public/logos/*.svg` are copied from `../resources/logos/` — the original SVGs have clean transparent backgrounds.
