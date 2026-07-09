# Brand Identity

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

## Philosophy

Telos is an infrastructure of digital sovereignty: a self-hosted, uncompromised digital home. The visual identity expresses "premium utility" — ancient utility meets sovereign futurism. Every surface, mark, and word in the product must read as deliberate, not decorative.

*(informative)* *Telos* (τέλος) is Greek for "end, purpose, goal."

## Slogans

Use these two slogans verbatim. Always lowercase. Never punctuated with an exclamation mark.

| Slogan | Usage context |
| --- | --- |
| `your server, your community` | Community-facing |
| `be on the net, but not of the net` | Philosophical/technical |

## The Sovereign Ouroboros

The Sovereign Ouroboros is a minimalist continuous serpent loop enclosing the wordmark.

Canonical renders:

- [`../../resources/Telos_1.png`](../../resources/Telos_1.png)
- [`../../resources/Telos_2.png`](../../resources/Telos_2.png)
- [`../../resources/Telos_3.png`](../../resources/Telos_3.png)

Rules:

- The mark is strictly monochromatic.
- The mark inherits `currentColor`.
- The eye is a cutout filled with the surface color behind the mark: `fill-[#0B0D13]` on dark surfaces, `fill-white` on light surfaces.

## Logo composition rule

The in-app lockup is an inline **SVG mark + adjacent HTML wordmark**. The wordmark is styled in the UI font stack, not baked into the SVG.

SVG `<text>` elements are forbidden in the mark because they render inconsistently across platforms. Full-lockup SVGs for export and marketing use must use outlined `<path>` data generated from a design tool, never live text.

Use this exact mark snippet:

```html
<svg viewBox="0 0 100 100" class="h-10 w-10" role="img" aria-label="Telos ouroboros mark">
  <path d="M 33 14 A 40 40 0 1 1 12 40" fill="none" stroke="currentColor"
        stroke-width="4.5" stroke-linecap="round" />
  <path d="M 34 14 C 26 10, 18 16, 21 24 C 24 29, 32 27, 36 22 C 39 18, 38 15, 34 14 Z"
        fill="currentColor" />
  <circle cx="26" cy="19" r="1.5" class="fill-[#0B0D13]" />
</svg>
<span class="text-lg font-semibold tracking-wide">Telos</span>
```

## Vaporwave logo variant

The vaporwave theme uses a distinct rendering of the mark rather than the standard monochrome lockup above.

- The variant artwork is supplied by the project owner. Once delivered, it lives at `resources/Telos_vaporwave.png` as the canonical render.
- Usage: the variant is shown ONLY when `data-theme="vaporwave"`. Every other theme uses the standard monochrome mark defined above.
- The in-app vector derived from the artwork may use a hot-pink-to-neon-cyan gradient (`#FF71CE` → `#01CDFE`) — the single sanctioned gradient in the product — and may carry the neon glow permitted for vaporwave per the tokens doc's [Theming §8](./02-design-tokens.md#8-theming) glow exception.
- Fallback: until the asset is integrated, implementations use the standard monochrome mark, which inherits vaporwave's text color via `currentColor`.
- Outside of this variant, the flat rule holds: no blurred backdrops and no shadows on the standard mark in light or dark mode.
