# Frontend Design Prompts

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

## How to Use

These are ready-to-paste prompts for any coding agent (such as Antigravity, Claude Code, or Cursor) implementing the Telos frontend. Each prompt is self-contained. When the repository is available, the implementing agent should also read the full specs at [`02-design-tokens.md`](./02-design-tokens.md), [`03-application-shell.md`](./03-application-shell.md), and [`04-modules.md`](./04-modules.md). 

For best results, build the dark theme first (default), then the light theme, and finally the vaporwave theme.

---

## Prompt: flat dark (default)

```text
You are an AI coding agent tasked with implementing the Telos application shell and chat module. 

### Context & Grounding
Telos is a self-hosted sovereign platform designed to merge chat/voice, media streaming, e-book library, and file management into one single-origin application. The interface uses a dense, functional 4-pane layout containing:
1. Global Header (height: h-14)
2. Global Nav Strip (width: 72px)
3. Contextual Sidebar (width: w-64)
4. Central Arena (takes remaining workspace)
5. Server SLA Sidebar (width: w-64, visible on screens >= xl)

The slogans are "your server, your community" and "be on the net, but not of the net" (both always lowercase).

### Theme & Palette (Flat Dark)
Implement the theme using these exact hex values:
- App Background (Deep Void): #0B0D13
- Panel/Card Background (Dark Slate): #121620
- Elevated/Hover: #1E293B (slate-800 or slate-800/50)
- Text Primary (Stark White): #F8FAFC
- Text Secondary: #94A3B8 (slate-400)
- Text Tertiary: #64748B (slate-500, used only for large/adjacent text context)
- Primary Accent (Sovereign Blue): #0EA5E9
- Badge/Wash: #0EA5E9 at 10% alpha background with #38BDF8 (sky-400) text

### Flat Discipline (Strict)
This theme is strictly flat.
- ABSOLUTELY NO box-shadows, gradients, or blurred backdrops (do not use shadow utility classes or backdrop blur effects).
- Visual separation must be achieved entirely via solid color surfaces and 1px borders (border-slate-800).
- Accent colors: At most one Sovereign Blue emphasis per visual region.

### Typography
- UI/Headers/Body: Inter, Geist Sans, system-ui fallback stack.
- Metrics/Logs/Paths/Code: JetBrains Mono, Fira Code fallback.
- Book reader module reading view: Serif font stack (the only exception).

### Semantic States
States must be carried structurally (via icons, text, and weight), never by extra semantic colors (do not use red, green, yellow, or other status colors):
- Irreversible destructive (delete/purge): Stark-white filled button + explicit confirmation step naming the target. Icon: Trash2 or AlertTriangle.
- Recoverable destructive (leave/close): Stark-white button, no confirmation.
- Error: AlertTriangle icon + short label + monospace detail line in #94A3B8. Border is #334155 (slate-700). No red tint.
- Live/Active: Pulsing Sovereign Blue dot (animate-pulse + motion-reduce:animate-none) + label.
- Success/Confirmation: Transient Check icon in #38BDF8 (sky-400) that fades after ~2s. No green.
- Progress: Sovereign Blue bars on #1E293B tracks.

### UI Quality & Polish
- Icons: lucide-react exclusively. No emojis anywhere.
- Radii: Inputs/buttons use 8px (rounded-lg). Structural cards use 12-16px (rounded-xl/rounded-2xl).
- Keyboard Navigation: Ctrl/Cmd+K to search; Ctrl/Cmd+1..4 to switch modules; Alt+Up/Down for sidebar channel/library navigation; Esc to close drawer/overlay.
- Accessibility: Interactive elements must have a visible focus ring (focus-visible:ring-2 focus-visible:ring-sky-500 focus-visible:ring-offset-2 focus-visible:ring-offset-[#0B0D13]). Respect prefers-reduced-motion. Icon-only buttons must have aria-label.
- Responsive Tiers: >=xl full 4-pane; <xl Server SLA Sidebar hidden; <lg Contextual Sidebar becomes overlay drawer; <md Global Nav Strip becomes bottom tab bar.

### Deliverable
Generate a complete, single-page layout matching the structure of design/05-reference-implementation.md containing the Global Header, Global Nav Strip, Contextual Sidebar, Server SLA Sidebar, and a mock Chat Module in the Central Arena.
```

---

## Prompt: flat light

```text
You are an AI coding agent tasked with implementing the Telos application shell and chat module.

### Context & Grounding
Telos is a self-hosted sovereign platform designed to merge chat/voice, media streaming, e-book library, and file management into one single-origin application. The interface uses a dense, functional 4-pane layout containing:
1. Global Header (height: h-14)
2. Global Nav Strip (width: 72px)
3. Contextual Sidebar (width: w-64)
4. Central Arena (takes remaining workspace)
5. Server SLA Sidebar (width: w-64, visible on screens >= xl)

The slogans are "your server, your community" and "be on the net, but not of the net" (both always lowercase).

### Theme & Palette (Flat Light)
Implement the theme using these exact hex values:
- App Background: #F1F5F9 (slate-100)
- Panel/Card Background: #FFFFFF
- Elevated/Hover: #E2E8F0 (slate-200)
- Text Primary: #111827 (gray-900)
- Text Secondary: #64748B (slate-500)
- Text Tertiary: #94A3B8 (slate-400)
- Primary Accent (Sovereign Blue): #0EA5E9
- Badge/Wash: #0EA5E9 at 10% alpha background with #0284C7 (sky-600) text

### Flat Discipline (Strict)
This theme is strictly flat.
- ABSOLUTELY NO box-shadows, gradients, or blurred backdrops (do not use shadow utility classes or backdrop blur effects).
- Visual separation must be achieved entirely via solid color surfaces and 1px borders (border-slate-200 or border-slate-300).
- Accent colors: At most one Sovereign Blue emphasis per visual region.

### Typography
- UI/Headers/Body: Inter, Geist Sans, system-ui fallback stack.
- Metrics/Logs/Paths/Code: JetBrains Mono, Fira Code fallback.
- Book reader module reading view: Serif font stack (the only exception).

### Semantic States
States must be carried structurally (via icons, text, and weight), never by extra semantic colors (do not use red, green, yellow, or other status colors):
- Irreversible destructive (delete/purge): Stark-white/dark text filled button + explicit confirmation step naming the target. Icon: Trash2 or AlertTriangle.
- Recoverable destructive (leave/close): Stark-white button, no confirmation.
- Error: AlertTriangle icon + short label + monospace detail line in #64748B. Border is border-slate-300. No red tint.
- Live/Active: Pulsing Sovereign Blue dot (animate-pulse + motion-reduce:animate-none) + label.
- Success/Confirmation: Transient Check icon in #0284C7 (sky-600) that fades after ~2s. No green.
- Progress: Sovereign Blue bars on #E2E8F0 tracks.

### UI Quality & Polish
- Icons: lucide-react exclusively. No emojis anywhere.
- Radii: Inputs/buttons use 8px (rounded-lg). Structural cards use 12-16px (rounded-xl/rounded-2xl).
- Keyboard Navigation: Ctrl/Cmd+K to search; Ctrl/Cmd+1..4 to switch modules; Alt+Up/Down for sidebar channel/library navigation; Esc to close drawer/overlay.
- Accessibility: Interactive elements must have a visible focus ring (focus-visible:ring-2 focus-visible:ring-sky-500 focus-visible:ring-offset-2 focus-visible:ring-offset-white). Respect prefers-reduced-motion. Icon-only buttons must have aria-label.
- Responsive Tiers: >=xl full 4-pane; <xl Server SLA Sidebar hidden; <lg Contextual Sidebar becomes overlay drawer; <md Global Nav Strip becomes bottom tab bar.

### Deliverable
Generate a complete, single-page layout matching the structure of design/05-reference-implementation.md containing the Global Header, Global Nav Strip, Contextual Sidebar, Server SLA Sidebar, and a mock Chat Module in the Central Arena.
```

---

## Prompt: vaporwave

```text
You are an AI coding agent tasked with implementing the Telos application shell and chat module.

### Context & Grounding
Telos is a self-hosted sovereign platform designed to merge chat/voice, media streaming, e-book library, and file management into one single-origin application. The interface uses a dense, functional 4-pane layout containing:
1. Global Header (height: h-14)
2. Global Nav Strip (width: 72px)
3. Contextual Sidebar (width: w-64)
4. Central Arena (takes remaining workspace)
5. Server SLA Sidebar (width: w-64, visible on screens >= xl)

The slogans are "your server, your community" and "be on the net, but not of the net" (both always lowercase).

### Theme & Palette (Vaporwave Classic Neon)
Implement the theme using these exact hex values or CSS custom variables (never Tailwind color-family classes):
- App Background (Surface): #0D0221
- Panel/Card Background: #1A0B3B
- Border: #2E1A5E
- Text Primary: #F8F8FF
- Text Secondary: #C8BFE7
- Primary Accent/Action (Hot Pink): #FF71CE
- Secondary/Active/Live (Neon Cyan): #01CDFE
- Tertiary/Badge Wash (Purple Wash): #B967FF at 15% alpha background with #B967FF text

### Flat Discipline with Glow Exceptions
The layout is flat with two specific exceptions:
- No box-shadows, no blurred backdrops, no gradients (do not use shadow utility classes or backdrop blur effects).
- Exception 1 (Neon Glow): A subtle CSS glow (box-shadow property, e.g. box-shadow: 0 0 12px rgba(1, 205, 254, 0.35);) is allowed ONLY on live/active indicators and the logo. Never use Tailwind shadow classes.
- Exception 2 (Vaporwave Logo): A pink-to-cyan gradient (#FF71CE to #01CDFE) is permitted ONLY on the logo variant.
- Fallback Logo: Standard monochrome mark inheriting currentColor when the owner-supplied logo (expected at resources/Telos_vaporwave.png) is not found.

### Typography
- UI/Headers/Body: Inter, Geist Sans, system-ui fallback stack.
- Metrics/Logs/Paths/Code: JetBrains Mono, Fira Code fallback.
- Book reader module reading view: Serif font stack (the only exception).

### Semantic States
States must be carried structurally (via icons, text, and weight), never by extra semantic colors (do not use red, green, yellow, or other status colors):
- Irreversible destructive (delete/purge): Stark-white/dark text filled button + explicit confirmation step naming the target. Icon: Trash2 or AlertTriangle.
- Recoverable destructive (leave/close): Stark-white button, no confirmation.
- Error: AlertTriangle icon + short label + monospace detail line in #C8BFE7. Border is #2E1A5E. No red tint.
- Live/Active: Pulsing Neon Cyan dot (animate-pulse + motion-reduce:animate-none) + label + neon cyan glow.
- Success/Confirmation: Transient Check icon in #01CDFE that fades after ~2s. No green.
- Progress: Hot Pink bars on #2E1A5E tracks.

### UI Quality & Polish
- Icons: lucide-react exclusively. No emojis anywhere.
- Radii: Inputs/buttons use 8px (rounded-lg). Structural cards use 12-16px (rounded-xl/rounded-2xl).
- Keyboard Navigation: Ctrl/Cmd+K to search; Ctrl/Cmd+1..4 to switch modules; Alt+Up/Down for sidebar channel/library navigation; Esc to close drawer/overlay.
- Accessibility: Interactive elements must have a visible focus ring (focus-visible:ring-2 focus-visible:ring-[#01CDFE] focus-visible:ring-offset-2 focus-visible:ring-offset-[#0D0221]). Respect prefers-reduced-motion. Icon-only buttons must have aria-label.
- Responsive Tiers: >=xl full 4-pane; <xl Server SLA Sidebar hidden; <lg Contextual Sidebar becomes overlay drawer; <md Global Nav Strip becomes bottom tab bar.

### Deliverable
Generate a complete, single-page layout matching the structure of design/05-reference-implementation.md containing the Global Header, Global Nav Strip, Contextual Sidebar, Server SLA Sidebar, and a mock Chat Module in the Central Arena.
```

---

*(informative)* Prompts deliberately restate token values so they are fully functional standalone. The design tokens spec remains the source of truth if any discrepancies arise.
