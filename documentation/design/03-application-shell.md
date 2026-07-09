# Application Shell

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

This file defines the global application shell: the persistent 4-pane layout, its header, its responsive collapse behavior, and its accessibility contract. Color, type, icon, radius, and motion values are defined in [`02-design-tokens.md`](./02-design-tokens.md) and are referenced here by name, not restated. The logo lockup and slogans are defined in [`01-brand-identity.md`](./01-brand-identity.md) and are referenced here by name, not restated.

## 1. Layout overview

The shell is a 4-pane horizontal flex layout beneath a fixed global header. Five named regions exist and every later document that references the shell must use these exact names: **Global Header**, **Global Nav Strip**, **Contextual Sidebar**, **Central Arena**, **Server SLA Sidebar**.

```
┌────────────────────────── Global Header (h-14) ──────────────────────────┐
│ [Ouroboros+wordmark]      [Global Search]      [Actions · Voice · Theme] │
├──────┬───────────────┬──────────────────────────────────┬────────────────┤
│ Nav  │  Contextual   │                                  │  Server SLA    │
│ strip│   sidebar     │          Central Arena           │   sidebar      │
│ 72px │    w-64       │            (flex-1)              │  w-64, ≥xl     │
│      │               │                                  │                │
└──────┴───────────────┴──────────────────────────────────┴────────────────┘
```

The Global Header spans the full viewport width at `h-14`. Below it, four panes sit side by side: the Global Nav Strip (`72px` fixed), the Contextual Sidebar (`w-64` fixed), the Central Arena (`flex-1`, fills remaining width), and the Server SLA Sidebar (`w-64` fixed, visible only at `xl` and above). See §7 for how these panes collapse below `xl`.

## 2. Global Header

The Global Header is `h-14` and divides into three zones:

- **Left:** the logo lockup, composed exactly per the logo composition rule in [`01-brand-identity.md`](./01-brand-identity.md#logo-composition-rule). No local variant of the mark or wordmark is defined in this document.
- **Center:** the global search input. Placeholder text, verbatim: `Title, Author, Series, Genre, or Tags...`. The input's focus state uses the Sovereign Blue focus ring defined in §8 (*Focus*).
- **Right:** three icon buttons — `Activity`, `Sparkles`, `Settings` — followed by a voice status indicator, a theme selector, and the `SOVEREIGNTY MANIFESTO` toggle button.

The theme selector is a three-state control cycling light → dark → vaporwave, using `lucide-react` icons `Sun` (light), `Moon` (dark), and `Waves` (vaporwave). Its `aria-label` announces the *next* theme the control will switch to, e.g. `Switch theme (next: vaporwave)`. Selecting a state applies `data-theme` per the theming mechanism in [`02-design-tokens.md` §8](./02-design-tokens.md#8-theming).

All icons in the Global Header are `lucide-react` icons per [`02-design-tokens.md` §4](./02-design-tokens.md#4-iconography). Icon-only buttons require an `aria-label` per §8 (*Motion & screen readers*).

## 3. Global Nav Strip

The Global Nav Strip is `72px` wide and hosts the module switchers, one icon button per module, using these exact `lucide-react` icons:

| Module | Icon |
| --- | --- |
| Chat | `MessageSquare` |
| Stream | `Tv` |
| Books | `BookOpen` |
| Files | `Folder` |

The active module's switcher is rendered as a Blue Wash pill (`sky-500/10` background, `sky-400` icon/text) per [`02-design-tokens.md` §1](./02-design-tokens.md#accent).

At the bottom of the Global Nav Strip is a user avatar rendered as a monogram circle. Hovering the avatar reveals local node details (the node the user is currently connected to).

## 4. Contextual Sidebar

The Contextual Sidebar is `w-64`. Its content is owned entirely by the active module; the shell defines only the container, not the contents — see `04-modules.md` for per-module sidebar content.

The sidebar footer is shell-owned and persists across modules. It shows the list of active local nodes/peers and the encrypted tunnel status, both rendered in the monospace stack per [`02-design-tokens.md` §3](./02-design-tokens.md#3-typography).

## 5. Central Arena

The Central Arena is the `flex-1` module workspace. It renders the active module's primary content and never hosts global chrome (no header, nav, or shell-level controls render inside the Central Arena). Anything global belongs in the Global Header, Global Nav Strip, Contextual Sidebar footer, or Server SLA Sidebar.

## 6. Server SLA Sidebar

The Server SLA Sidebar is `w-64` and visible only at `xl` and above (see §7). It contains, top to bottom:

- A header reading "Sovereign Node SLA".
- Server specs — Host OS, DB Engine — rendered in the monospace stack per [`02-design-tokens.md` §3](./02-design-tokens.md#3-typography).
- Both slogans from [`01-brand-identity.md`](./01-brand-identity.md#slogans), each in its own stylized box, verbatim and lowercase: `your server, your community` and `be on the net, but not of the net`.
- A connected peers list.

## 7. Responsive behavior

| Tier | Behavior |
| --- | --- |
| ≥ `xl` | Full 4-pane layout; all panes visible simultaneously. |
| < `xl` | Server SLA Sidebar is hidden. |
| < `lg` | Contextual Sidebar becomes an overlay drawer instead of an inline pane. |
| < `md` | Global Nav Strip becomes a bottom tab bar instead of a left-side strip. |

Two behaviors hold at every tier: the voice call bar is a floating overlay regardless of viewport size, and the global search input collapses to an icon-only control below `md`.

## 8. Accessibility

### Contrast (WCAG 2.1 AA)

| Foreground | Background | Approximate ratio | Verdict |
| --- | --- | --- | --- |
| `#F8FAFC` | `#0B0D13` | 17:1 | Pass |
| `slate-400` | `#0B0D13` | 7:1 | Pass |
| `sky-500` | `#0B0D13` | 6.8:1 | Pass |
| `slate-500` | `#0B0D13` | 4.6:1 | Large text only |
| `sky-400` on `sky-500/10` | over `#121620` | 8:1 | Pass |
| `#F8F8FF` | `#0D0221` | 18:1 | Pass |
| `#FF71CE` | `#0D0221` | 8:1 | Pass |
| `#01CDFE` | `#0D0221` | 11:1 | Pass |
| `#B967FF` | `#0D0221` | 6:1 | Pass |
| `#C8BFE7` | `#0D0221` | 12:1 | Pass |

### Focus

Every interactive element receives the following focus-visible treatment:

```
focus-visible:ring-2 focus-visible:ring-sky-500 focus-visible:ring-offset-2 focus-visible:ring-offset-[#0B0D13]
```

Outlines must never be removed without this replacement in place.

The ring color follows the theme accent: `sky-500` in light and dark themes, `#01CDFE` in vaporwave, applied via the ring CSS var so no per-component override is needed.

### Keyboard map

| Shortcut | Action |
| --- | --- |
| `Ctrl/⌘+K` | Focus global search |
| `Ctrl/⌘+1…4` | Switch modules |
| `Alt+↑/↓` | Channel/library navigation within sidebar |
| `Ctrl/⌘+Shift+M` | Toggle mute when in voice |
| `Esc` | Close drawer/overlay |

### Motion & screen readers

Motion respects `prefers-reduced-motion` per the motion rules in [`02-design-tokens.md` §7](./02-design-tokens.md#7-motion). Icon-only buttons require an `aria-label`. Live regions announce voice join/leave events.
