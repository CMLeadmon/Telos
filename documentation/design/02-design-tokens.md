# Design Tokens

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

This file is the single source of truth for color, type, iconography, radii, semantic-state patterns, and motion. Every later design document builds on the values defined here. For brand philosophy, slogans, and the Sovereign Ouroboros mark, see [`01-brand-identity.md`](./01-brand-identity.md).

## 1. Color system

The palette is grayscale neutrals, a stark white, and exactly one blue. This is the only file in the documentation set permitted to name the forbidden hues, and only to state the prohibition: do not introduce semantic accent hues (red, green, yellow, amber, emerald). No other document, and no implementation code, may reference these hues in any form.

### Surfaces (dark default)

| Token | Value | Tailwind | Usage |
| --- | --- | --- | --- |
| Deep Void | `#0B0D13` | `slate-950`-adjacent | App background |
| Dark Slate | `#121620` | — | Panels, cards |
| Elevated | — | `slate-800` / `slate-800/50` | Hover states |

### Text

| Token | Value | Tailwind | Usage |
| --- | --- | --- | --- |
| Stark White | `#F8FAFC` | `slate-50` | Primary text |
| Secondary | — | `slate-400` | Secondary text |
| Tertiary | — | `slate-500` | Large text or adjacent-context text only — see contrast note below |

Contrast note: `slate-500` on Deep Void or Dark Slate does not meet body-text contrast requirements. Reserve it for large type (headings, display numerals) or text set immediately adjacent to higher-contrast context that establishes meaning. Never use `slate-500` for small body copy or standalone labels.

### Accent

| Token | Value | Tailwind | Usage |
| --- | --- | --- | --- |
| Sovereign Blue | `#0EA5E9` | `sky-500` | Active states, primary buttons, AI/oracle features, progress bars |
| Blue Wash | — | `sky-500/10` bg, `sky-400` text | Badges, active voice states |

Rule: at most one Sovereign Blue emphasis per visual region. Do not stack multiple high-saturation blue elements (a filled button and a pulsing dot and a progress bar) in the same visual region — pick the single element that most needs emphasis.

## 2. Light mode

| Token | Value | Tailwind | Usage |
| --- | --- | --- | --- |
| Background | `#F1F5F9` | — | App background |
| Panels | `#FFFFFF` | — | Panels, cards |
| Text | `#111827` | — | Primary text |
| Border | — | `slate-200` / `slate-300` | Dividers, outlines |

The accent palette (Sovereign Blue, Blue Wash) is unchanged between dark and light mode.

## 3. Typography

| Context | Stack |
| --- | --- |
| UI, headers, body | Inter, Geist Sans, system-ui |
| Metrics, logs, code, file paths | JetBrains Mono, Fira Code |

The reader view (books module) is the single scoped exception and may use a serif stack for reading content only. No other surface may deviate from the two stacks above.

## 4. Iconography

Icons come from `lucide-react` exclusively. All icons are monochromatic outlines inheriting `currentColor`. Default size is 16–20px. Emojis are forbidden everywhere in the product and in documentation, including prose and examples.

## 5. Borders & radii

| Element class | Radius | Tailwind |
| --- | --- | --- |
| Inputs, buttons | 8px | `rounded-lg` |
| Structural cards | 12–16px | `rounded-xl` / `rounded-2xl` |

Border color: `border-slate-800` on dark surfaces, `border-slate-200` on light surfaces.

## 6. Semantic states without semantic color

Telos communicates state through icon, weight, and copy — never through hue. The following six states are exhaustive; do not introduce additional states or merge these rows.

| State | Treatment | Example |
| --- | --- | --- |
| Destructive (irreversible — delete file, purge channel) | Stark-white filled button (`bg-slate-50 text-slate-900`) plus an explicit confirmation step that names the target | Icon `Trash2` or `AlertTriangle` |
| Destructive (recoverable — leave voice, close session) | Same stark-white emphasis as above, no confirmation step required | Icon `Trash2` or `AlertTriangle` |
| Error | `AlertTriangle` icon plus a short label plus a monospace detail line in `slate-400`; container border `border-slate-700`, never a tinted border | Failed upload, connection error |
| Live/active (voice, streaming, recording) | Pulsing Sovereign Blue dot (`animate-pulse` paired with `motion-reduce:animate-none`) plus a label | Recording indicator, live voice channel |
| Success/confirmation | Transient `Check` icon in `sky-400`, fades after approximately 2 seconds | Saved settings, completed action |
| Progress | Sovereign Blue bars on `slate-800` tracks | File upload, transcode progress |

Rationale *(informative)*: meaning is carried by icon, weight, and copy rather than hue, so the single-accent brand survives every UI state without borrowing color from a palette Telos does not use.

## 7. Motion

| Interaction | Duration |
| --- | --- |
| Hover | 150ms |
| Layout change | 300ms |

Every `animate-*` utility must be paired with its `motion-reduce:` variant so motion-sensitive users see static equivalents.
