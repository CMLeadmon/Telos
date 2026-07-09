# Telos Documentation Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `documentation/design.md` and `documentation/implementation.md` with an 11-file, internally consistent documentation set per the approved spec at `docs/superpowers/specs/2026-07-08-telos-docs-rewrite-design.md`.

**Architecture:** Pure documentation work — each task authors one markdown file under `documentation/design/` or `documentation/architecture/`, verifies it with greps/YAML parsing, and commits. The final task adds the index README, deletes the two originals (preserved at git commit `8bf9c24`), and runs the full verification suite.

**Tech Stack:** Markdown, embedded YAML/TypeScript/TSX code blocks, `python3` + PyYAML for YAML verification, `grep` for artifact/palette checks.

## Global Constraints

Every file written by every task MUST honor these. Copy values exactly.

- **Audience preamble:** every file begins (after its H1) with: `> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.`
- **Accent color:** "Sovereign Blue" = `#0EA5E9` (Tailwind `sky-500`). Badge/wash form = `sky-400` text on `sky-500/10`. NO `cyan-*`, `emerald`, `amber`, `rose`, `red`, `green`, `yellow` Tailwind classes anywhere. The words "red, green, yellow, amber, emerald" may appear ONLY in `design/02-design-tokens.md` prose listing forbidden colors.
- **Palette:** app bg `#0B0D13`, panel bg `#121620`, elevated/hover `slate-800`, primary text `#F8FAFC` (`slate-50`), secondary text `slate-400` (use `slate-500` only for large/adjacent-context text).
- **Typography:** UI = Inter, Geist Sans, system-ui fallback stack; system/metrics/code = JetBrains Mono, Fira Code fallback.
- **Icons:** `lucide-react` exclusively; no emojis anywhere in the docs (prose or examples).
- **Slogans (verbatim, lowercase):** `your server, your community` (community-facing) and `be on the net, but not of the net` (philosophical/technical).
- **Radii:** inputs/buttons 8px (`rounded-lg`); structural cards 12–16px (`rounded-xl`/`rounded-2xl`). (Corrects the original's "md (8px)" — Tailwind `rounded-md` is 6px.)
- **No artifacts:** no `[cite:` anywhere; no `TBD`/`TODO`; no hardcoded secrets (all credentials in code blocks are `${VAR}` compose references or `change-me` placeholders in `.env.example` only).
- **Grimmory facts (verified 2026-07-08):** repo `https://github.com/grimmory-tools/grimmory`, site `https://grimmory.org`, license AGPL-3.0, image `grimmory/grimmory:latest` (alt `ghcr.io/grimmory-tools/grimmory`), port `6060`, env `USER_ID`/`GROUP_ID`/`TZ`/`DATABASE_URL` (JDBC MariaDB)/`DATABASE_USERNAME`/`DATABASE_PASSWORD`/`API_DOCS_ENABLED`/`DISK_TYPE=LOCAL`, volumes `/app/data` `/books` `/bookdrop`, healthcheck `GET /api/v1/healthcheck`. Context: BookLore was withdrawn by its maintainer in March 2026; Grimmory is the community successor.
- **Licenses:** Jellyfin GPL-2.0 (`https://github.com/jellyfin/jellyfin`), Grimmory AGPL-3.0, LiveKit Apache-2.0 (`https://github.com/livekit/livekit`).
- **Responsive tiers:** ≥`xl` full 4-pane; <`xl` SLA sidebar hidden; <`lg` contextual sidebar becomes overlay drawer; <`md` left nav strip becomes bottom tab bar.
- **Cross-links:** relative markdown links between docs (e.g., `../design/02-design-tokens.md`); logo renders referenced as `../../resources/Telos_1.png` etc.
- **Theming (amendment):** three themes via `data-theme="light" | "dark" | "vaporwave"` CSS custom properties on the root element; dark is default; selection persisted. The header theme control is a three-state selector with lucide icons Sun (light), Moon (dark), Waves (vaporwave).
- **Flat rule (amendment):** light and dark themes are strictly flat — no box-shadows, no backdrop-blur, no gradients; separation via 1px borders and surface-color steps. Tailwind `shadow-*` and `backdrop-blur*` classes are banned in every doc (verify grep pattern: `shadow-[a-z0-9]|backdrop-blur`).
- **Vaporwave palette (amendment, exact):** surface `#0D0221`, panel `#1A0B3B`, border `#2E1A5E`, text `#F8F8FF`, secondary `#C8BFE7`; accents: hot pink `#FF71CE` (takes the primary-action role Sovereign Blue has in light/dark), neon cyan `#01CDFE` (links/active/live), purple `#B967FF` (badge-wash role, e.g. `#B967FF` at ~15% alpha bg with `#B967FF` text). Express ONLY as hex values or CSS vars — never Tailwind color-family classes. Approximate WCAG ratios on `#0D0221`: text ≈18:1, pink ≈8:1, cyan ≈11:1, purple ≈6:1, secondary ≈12:1. Semantic-state patterns (tokens §6) remain structural and theme-independent.
- **Vaporwave glow exception (amendment):** vaporwave alone may use neon glow, ONLY on live/active indicators and the logo, written as a CSS property (e.g. `box-shadow: 0 0 12px rgba(1, 205, 254, 0.35);`) — never as a Tailwind class, so the flat-rule grep stays silent.
- **Vaporwave logo (amendment):** artwork is supplied by the project owner; expected path `resources/Telos_vaporwave.png`; in vaporwave mode the mark swaps to this variant; until the asset lands, implementations fall back to the standard monochrome mark inheriting `currentColor`. Gradients are permitted only in this logo variant.
- **Agent-agnostic docs (amendment):** everything under `documentation/` and the repo-root `AGENTS.md` must be executable by any coding agent (Antigravity, Claude, Cursor): plain markdown, plain shell verification commands, no references to session-specific paths or Claude-specific tooling.
- **Commit format:** each task commits with a `docs:` prefix message ending in the trailer `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- **Verification helper (used by several tasks; create once in Task 1, Step 2):** save as `/tmp/claude-1000/-var-home-cleadmon-Projects-Telos/f9542da3-740c-479c-b6b5-ded02b7bccea/scratchpad/check_yaml.py`:

```python
import pathlib, re, sys
import yaml

failures = 0
for p in sorted(pathlib.Path("documentation").rglob("*.md")):
    text = p.read_text()
    for i, block in enumerate(re.findall(r"```ya?ml\n(.*?)```", text, re.S), 1):
        try:
            list(yaml.safe_load_all(block))
        except yaml.YAMLError as e:
            failures += 1
            print(f"{p}: yaml block {i} FAILED: {e}")
print("YAML OK" if not failures else f"{failures} YAML failures")
sys.exit(1 if failures else 0)
```

---

### Task 1: `documentation/design/01-brand-identity.md`

**Files:**
- Create: `documentation/design/01-brand-identity.md`
- Create (scratchpad): `check_yaml.py` per Global Constraints

**Interfaces:**
- Produces: the logo composition rule ("SVG mark + HTML wordmark") and slogan strings that Tasks 3, 5, and 11 reference.

- [ ] **Step 1: Write the file** with H1 `# Brand Identity`, the audience preamble, then these sections:

1. **Philosophy** — Telos is an infrastructure of digital sovereignty: a self-hosted, uncompromised digital home. Visual identity = "premium utility"; ancient utility meets sovereign futurism. *(informative)* note: *telos* (τέλος) is Greek for "end, purpose, goal".
2. **Slogans** — table with the two verbatim slogans and their usage contexts (community-facing vs. philosophical/technical). Rule: always lowercase, never punctuated with an exclamation mark.
3. **The Sovereign Ouroboros** — a minimalist continuous serpent loop enclosing the wordmark. Canonical renders: link all three files in `../../resources/`. Rules: strictly monochromatic; inherits `currentColor`; eye is a cutout filled with the surface color behind the mark (`fill-[#0B0D13]` on dark surfaces, `fill-white` on light).
4. **Logo composition rule** — in-app lockup = inline **SVG mark + adjacent HTML wordmark** (wordmark styled in the UI font stack). SVG `<text>` elements are forbidden (inconsistent cross-platform rendering); full-lockup SVGs for export/marketing must use outlined `<path>` data generated from a design tool. Include this exact mark snippet:

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

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD" documentation/design/01-brand-identity.md`
Expected: no output.
Run: `python3 <scratchpad>/check_yaml.py`
Expected: `YAML OK`.

- [ ] **Step 3: Commit**

```bash
git add documentation/design/01-brand-identity.md
git commit -m "docs: add brand identity spec (design/01)"
```

---

### Task 2: `documentation/design/02-design-tokens.md`

**Files:**
- Create: `documentation/design/02-design-tokens.md`

**Interfaces:**
- Produces: token names ("Sovereign Blue", "Deep Void", "Dark Slate") and the semantic-state patterns that Tasks 3–5 and 10 must follow.

- [ ] **Step 1: Write the file** with H1 `# Design Tokens`, audience preamble, sections:

1. **Color system** — open with the hard rule: grayscale neutrals + stark white + one blue. This file is the ONLY place allowed to name the forbidden hues: "Do not introduce semantic accent hues (red, green, yellow, amber, emerald)." Then three tables:
   - *Surfaces (dark default):* Deep Void `#0B0D13` (app bg, `slate-950`-adjacent), Dark Slate `#121620` (panels/cards), Elevated `slate-800` / `slate-800/50` (hover).
   - *Text:* Stark White `#F8FAFC`/`slate-50` (primary), `slate-400` (secondary), `slate-500` (large/tertiary only — see contrast note).
   - *Accent:* Sovereign Blue `#0EA5E9` (`sky-500`) — active states, primary buttons, AI/oracle features, progress bars; Blue Wash `sky-500/10` bg + `sky-400` text — badges, active voice states. Rule: at most one Sovereign Blue emphasis per visual region.
2. **Light mode** — bg `#F1F5F9`, panels `#FFFFFF`, text `#111827`, borders `slate-200`/`slate-300`; accent unchanged.
3. **Typography** — table: UI/headers/body = Inter, Geist Sans, system-ui; metrics/logs/code/file paths = JetBrains Mono, Fira Code. Reader view (books module) may use a serif stack — the single exception, scoped to reading content.
4. **Iconography** — `lucide-react` only; monochromatic outlines; default size 16–20px; NO EMOJIS.
5. **Borders & radii** — borders `border-slate-800` (dark) / `border-slate-200` (light); radii per Global Constraints, stating the px values and Tailwind classes.
6. **Semantic states without semantic color** — the load-bearing new section. Table of state → treatment → example:
   - *Destructive (irreversible: delete file, purge channel):* stark-white filled button (`bg-slate-50 text-slate-900`) + explicit confirmation step naming the target; icon `Trash2` or `AlertTriangle`.
   - *Destructive (recoverable: leave voice, close session):* same stark-white emphasis, no confirmation.
   - *Error:* `AlertTriangle` icon + short label + monospace detail line in `slate-400`; container `border-slate-700`, never a red tint.
   - *Live/active (voice, streaming, recording):* pulsing Sovereign Blue dot (`animate-pulse` + `motion-reduce:animate-none`) + label.
   - *Success/confirmation:* transient `Check` icon in `sky-400`, fades after ~2s; no green.
   - *Progress:* Sovereign Blue bars on `slate-800` tracks.
   Rationale line: meaning is carried by icon + weight + copy, so the single-accent brand survives every UI state.
7. **Motion** — durations 150ms (hover) / 300ms (layout); always pair `animate-*` with `motion-reduce:` variants.

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|cyan-" documentation/design/02-design-tokens.md`
Expected: no output (forbidden-hue words appear, but no `cyan-` Tailwind class).

- [ ] **Step 3: Commit**

```bash
git add documentation/design/02-design-tokens.md
git commit -m "docs: add design tokens incl. semantic-states pattern (design/02)"
```

---

### Task 3: `documentation/design/03-application-shell.md`

**Files:**
- Create: `documentation/design/03-application-shell.md`

**Interfaces:**
- Consumes: token names from Task 2; logo composition rule from Task 1.
- Produces: pane names (Global Header, Global Nav Strip, Contextual Sidebar, Central Arena, SLA Sidebar) used by Tasks 4, 5, 11.

- [ ] **Step 1: Write the file** with H1 `# Application Shell`, audience preamble, sections:

1. **Layout overview** — 4-pane horizontal flex under a global header; include this ASCII diagram:

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

2. **Global Header** — left: logo lockup per `01-brand-identity.md`; center: global search, placeholder verbatim `Title, Author, Series, Genre, or Tags...`, focus ring Sovereign Blue; right: Activity, Sparkles, Settings icons, voice status indicator, Sun/Moon theme toggle, `SOVEREIGNTY MANIFESTO` toggle button.
3. **Global Nav Strip** — 72px; module switchers with exact lucide icons: Chat=`MessageSquare`, Stream=`Tv`, Books=`BookOpen`, Files=`Folder`; active state = Blue Wash pill; bottom: user avatar monogram circle, hover reveals local node details.
4. **Contextual Sidebar** — `w-64`; content owned by active module (see `04-modules.md`); footer shows active local nodes/peers + encrypted tunnel status in mono font.
5. **Central Arena** — module workspace; never hosts global chrome.
6. **Server SLA Sidebar** — `w-64`, visible ≥`xl`; "Sovereign Node SLA" header, server specs (Host OS, DB Engine) in mono, both slogans in stylized boxes, connected peers list.
7. **Responsive behavior** — table of the four tiers from Global Constraints, plus: voice call bar is a floating overlay at all sizes; search collapses to an icon <`md`.
8. **Accessibility** — subsections:
   - *Contrast (WCAG 2.1 AA):* table of approved pairs with approximate ratios — `#F8FAFC` on `#0B0D13` ≈ 17:1 (pass), `slate-400` on `#0B0D13` ≈ 7:1 (pass), `sky-500` on `#0B0D13` ≈ 6.8:1 (pass), `slate-500` on `#0B0D13` ≈ 4.6:1 (large text only), `sky-400` on `sky-500/10`-over-`#121620` ≈ 8:1 (pass).
   - *Focus:* every interactive element gets `focus-visible:ring-2 focus-visible:ring-sky-500 focus-visible:ring-offset-2 focus-visible:ring-offset-[#0B0D13]`; never remove outlines without a replacement.
   - *Keyboard map:* table — `Ctrl/⌘+K` focus global search; `Ctrl/⌘+1…4` switch modules; `Alt+↑/↓` channel/library navigation within sidebar; `Ctrl/⌘+Shift+M` toggle mute when in voice; `Esc` closes drawer/overlay.
   - *Motion & screen readers:* respect `prefers-reduced-motion` per tokens doc; icon-only buttons require `aria-label`; live regions announce voice join/leave.

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|cyan-|emerald|amber|rose-" documentation/design/03-application-shell.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add documentation/design/03-application-shell.md
git commit -m "docs: add application shell spec with responsive + a11y (design/03)"
```

---

### Task 4: `documentation/design/04-modules.md`

**Files:**
- Create: `documentation/design/04-modules.md`

**Interfaces:**
- Consumes: pane names (Task 3), semantic-state patterns (Task 2).

- [ ] **Step 1: Write the file** with H1 `# Module Specifications`, audience preamble, then five H2 sections:

1. **Chat & Voice** — Sidebar: text channels prefixed with `Hash` icon; voice channels with Join/Leave states and nested active-user lists (active voice = Blue Wash + pulsing blue dot per tokens §6). Arena: header with `SUMMARIZE MISSED` button (`Bot` icon, Sovereign Blue); brand banner quoting `be on the net, but not of the net`; message anatomy = fully rounded monogram avatar, username, role badge (Host/Admin as Blue Wash badge), timestamp in `slate-500`; integrated bottom input.
2. **Media Streaming** — Sidebar: libraries (Movies, Documentaries, Audiobooks) + "Ingest Stream" URL tool. Arena: central HTML5 player (play/pause, timeline, duration, direct-stream bitrate badges in mono); below, grid of active feeds with Sovereign Blue progress bars attached flush to thumbnail bottoms.
3. **Books (Grimmory)** — Sidebar: Home (Dashboard, All Books, Authors), Libraries, Shelves with right-aligned numeric count badges. Library view: "Continue Listening" / "Continue Reading" horizontal rows, headers with thick Sovereign Blue underline; book cards with format badge (EPUB/PDF) top-left, centered title/author on neutral dark cover, `Play` overlay on hover. Reader view: serif reading stack (the sanctioned exception), font-size controls, `ANALYZE CHAPTER` button in footer (AI summary of current chapter).
4. **Files** — Sidebar: storage volume metrics (mono paths like `/mnt/ssd_nvme`, Sovereign Blue usage bars) + quick navigation. Arena: breadcrumbs + `New Directory` / `Upload` buttons; grid-list with Name, Size, Modified; selection opens bottom action bar with Rename, Download, and Delete — Delete follows the irreversible-destructive pattern (stark-white button + confirmation naming the file).
5. **Sovereignty Manifesto** — opened from the header toggle; explains "The Digital Enclosure Problem" and "The Telos Design Creed"; features a mono terminal block showing a `docker-compose.yml` excerpt with local loopback binds and `${VAR}` secret references (no literal secrets); link to `../architecture/02-deployment.md` for the real file.

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|cyan-|emerald|amber|rose-" documentation/design/04-modules.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add documentation/design/04-modules.md
git commit -m "docs: add module specifications (design/04)"
```

---

### Task 5: `documentation/design/05-reference-implementation.md`

**Files:**
- Create: `documentation/design/05-reference-implementation.md`

**Interfaces:**
- Consumes: everything from Tasks 1–4.

- [ ] **Step 1: Write the file** with H1 `# Reference Shell Implementation`, audience preamble, an *(informative)* architecture disclaimer (structural/aesthetic reference in React + Tailwind; adapt states/hooks/tags to the host framework — Next.js, Vue, Nuxt, SvelteKit all acceptable), then this complete corrected snippet as one `tsx` block:

```tsx
import React, { useState } from 'react';
import {
  Activity, BookOpen, Folder, MessageSquare, Moon, Search, Settings,
  Sparkles, Sun, Tv,
} from 'lucide-react';

const MODULES = [
  { id: 'chat', icon: MessageSquare, label: 'Chat' },
  { id: 'stream', icon: Tv, label: 'Stream' },
  { id: 'books', icon: BookOpen, label: 'Books' },
  { id: 'files', icon: Folder, label: 'Files' },
] as const;

export default function App() {
  const [isDarkMode, setIsDarkMode] = useState(true);
  const [activeModule, setActiveModule] = useState<'chat' | 'stream' | 'books' | 'files'>('chat');

  return (
    <div
      className={`flex min-h-screen flex-col font-sans transition-colors duration-300 ${
        isDarkMode ? 'bg-[#0B0D13] text-[#F8FAFC]' : 'bg-[#F1F5F9] text-[#111827]'
      }`}
    >
      {/* 1. Global Header */}
      <header
        className={`z-20 flex h-14 items-center justify-between border-b px-6 transition-colors ${
          isDarkMode ? 'border-slate-800 bg-[#121620]' : 'border-slate-200 bg-white shadow-sm'
        }`}
      >
        <div className="flex w-48 items-center gap-2">
          {/* Ouroboros mark + HTML wordmark (see design/01-brand-identity.md) */}
          <svg viewBox="0 0 100 100" className="h-10 w-10" role="img" aria-label="Telos ouroboros mark">
            <path d="M 33 14 A 40 40 0 1 1 12 40" fill="none" stroke="currentColor"
                  strokeWidth="4.5" strokeLinecap="round" />
            <path d="M 34 14 C 26 10, 18 16, 21 24 C 24 29, 32 27, 36 22 C 39 18, 38 15, 34 14 Z"
                  fill="currentColor" />
            <circle cx="26" cy="19" r="1.5" fill={isDarkMode ? '#0B0D13' : '#FFFFFF'} />
          </svg>
          <span className="text-lg font-semibold tracking-wide">Telos</span>
        </div>

        {/* Global search */}
        <div className="mx-8 hidden max-w-xl flex-1 md:flex">
          <div className="group relative w-full">
            <Search className="absolute left-4 top-2 h-4 w-4 text-slate-400 transition-colors group-focus-within:text-sky-500" />
            <input
              type="text"
              placeholder="Title, Author, Series, Genre, or Tags..."
              className={`w-full rounded-lg border py-1.5 pl-11 pr-4 text-sm transition-all focus:outline-none focus-visible:ring-2 focus-visible:ring-sky-500 ${
                isDarkMode
                  ? 'border-slate-700 bg-[#1A1E29] text-[#F8FAFC] placeholder-slate-500 focus:border-sky-500/50'
                  : 'border-slate-300 bg-slate-100 text-[#111827] placeholder-slate-500 focus:border-sky-400 focus:bg-white'
              }`}
            />
          </div>
        </div>

        {/* Quick actions */}
        <div className="flex w-48 items-center justify-end gap-3">
          <button aria-label="Activity" className="text-slate-400 hover:text-slate-200"><Activity className="h-4 w-4" /></button>
          <button aria-label="AI features" className="text-slate-400 hover:text-sky-400"><Sparkles className="h-4 w-4" /></button>
          <button aria-label="Settings" className="text-slate-400 hover:text-slate-200"><Settings className="h-4 w-4" /></button>
          <button aria-label="Toggle theme" onClick={() => setIsDarkMode(!isDarkMode)} className="text-slate-400 hover:text-slate-200">
            {isDarkMode ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </button>
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {/* 2. Global Nav Strip */}
        <nav
          className={`flex w-[72px] flex-shrink-0 flex-col items-center justify-between border-r py-4 transition-colors ${
            isDarkMode ? 'border-slate-800 bg-[#10131C]' : 'border-slate-200 bg-[#F8FAFC]'
          }`}
        >
          <div className="flex flex-col gap-2">
            {MODULES.map(({ id, icon: Icon, label }) => (
              <button
                key={id}
                aria-label={label}
                onClick={() => setActiveModule(id)}
                className={`rounded-xl p-3 transition-colors ${
                  activeModule === id
                    ? 'bg-sky-500/10 text-sky-400'
                    : 'text-slate-400 hover:bg-slate-800/50 hover:text-slate-200'
                }`}
              >
                <Icon className="h-5 w-5" />
              </button>
            ))}
          </div>
          <button
            aria-label="Profile: AD"
            className="flex h-10 w-10 items-center justify-center rounded-full bg-slate-800 text-xs font-semibold text-slate-200"
          >
            AD
          </button>
        </nav>

        {/* 3. Contextual Sidebar */}
        <aside
          className={`hidden w-64 flex-shrink-0 flex-col border-r transition-colors lg:flex ${
            isDarkMode ? 'border-slate-800 bg-[#151924]' : 'border-slate-200 bg-white'
          }`}
        >
          <div className="flex-1 overflow-y-auto py-3">{/* module sub-navigation */}</div>
        </aside>

        {/* 4. Central Arena */}
        <main className="flex flex-1 flex-col overflow-hidden">
          {/* active module renders here */}
        </main>

        {/* 5. Server SLA Sidebar */}
        <aside
          className={`hidden w-64 flex-shrink-0 flex-col border-l transition-colors xl:flex ${
            isDarkMode ? 'border-slate-800 bg-[#121621]' : 'border-slate-200 bg-white'
          }`}
        >
          {/* server specs, slogans, peers */}
        </aside>
      </div>
    </div>
  );
}
```

Close with a **Corrections from the original mockup** *(informative)* list — word it exactly like this so verification greps stay clean (no literal forbidden class strings): "cyan accent classes replaced with sky equivalents; invalid z-index utility replaced with `z-20`; SVG text-element wordmark replaced with HTML sibling; icon-only buttons gained `aria-label`; contextual sidebar hidden below `lg` per responsive spec."

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|cyan-|emerald|amber|rose-|z-25" documentation/design/05-reference-implementation.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add documentation/design/05-reference-implementation.md
git commit -m "docs: add corrected reference shell implementation (design/05)"
```

---

### Task 6: `documentation/architecture/01-system-overview.md`

**Files:**
- Create: `documentation/architecture/01-system-overview.md`

**Interfaces:**
- Produces: service names (`traefik`, `telos-core`, `postgres`, `redis`, `jellyfin`, `grimmory`, `grimmory-db`, `livekit`) and network names (`telos-ingress`, `telos-backend`, `telos-db`) used by Tasks 7–10.

- [ ] **Step 1: Write the file** with H1 `# System Overview`, audience preamble, sections:

1. **Problem** — fragmentation of self-hosted platforms (chat, streaming, catalog, files) causes resource overhead, inconsistent UX, configuration fatigue.
2. **Paradigm: Suite/Orchestration ("Strategy A")** — Telos is a single-port gateway + master UI wrapping dedicated headless open-source services in isolated containers. No rewriting of transcoders, parsers, or SFUs. To the user it reads as one native app.
3. **Service inventory** — table: service / image / role / networks / internal port. Values: `traefik` `traefik:v3.3` edge router (ingress; 80/443) · `telos-core` custom Go (or Rust) gateway (ingress, backend, db; 8080) · `postgres` `postgres:16-alpine` chat + metadata persistence (db; 5432) · `redis` `redis:7-alpine` presence/state/cache (backend; 6379) · `jellyfin` `jellyfin/jellyfin:latest` headless transcoding + HLS (ingress, backend; 8096) · `grimmory` `grimmory/grimmory:latest` headless publication catalog (ingress, backend, db; 6060) · `grimmory-db` `mariadb:10.11` Grimmory database (db; 3306) · `livekit` `livekit/livekit-server:v1.10` WebRTC SFU (ingress, backend; 7880 + published media ports).
4. **Topology diagram** — cleaned ASCII: Client → Traefik (:443) → path-split to `telos-core` (`/api/v1`, `/`, auth, WS chat), `livekit` (`/livekit` signaling; RTP/UDP flows directly client↔livekit on published ports, NOT through Traefik), `jellyfin` (`/jellyfin`), `grimmory` (`/grimmory`); `telos-core` → PostgreSQL + Redis; `livekit` → Redis; `jellyfin`/`grimmory` → shared host storage under `/mnt/storage/shared`.
5. **Key data flows** — chat (WS → core → Postgres, presence via Redis pub/sub); voice (JWT from core → LiveKit signaling via Traefik → direct UDP media); media (HLS segments proxied from Jellyfin); books (uploads land in `bookdrop`, Grimmory ingests + fetches metadata from Google Books / Open Library).
6. **Grimmory provenance** *(informative)* — one short paragraph: BookLore withdrawn March 2026; Grimmory (`github.com/grimmory-tools/grimmory`) is the community successor; MariaDB data format compatible.

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD" documentation/architecture/01-system-overview.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add documentation/architecture/01-system-overview.md
git commit -m "docs: add system overview (architecture/01)"
```

---

### Task 7: `documentation/architecture/02-deployment.md`

**Files:**
- Create: `documentation/architecture/02-deployment.md`

**Interfaces:**
- Consumes: service/network names from Task 6.
- Produces: the canonical `docker-compose.yml` and `.env.example` that Task 8's Traefik labels section refers back to.

- [ ] **Step 1: Write the file** with H1 `# Deployment`, audience preamble, sections:

1. **Network model** — table of the three networks: `telos-ingress` (exposed; traefik, telos-core, jellyfin, grimmory, livekit) / `telos-backend` (internal; telos-core, redis, jellyfin, grimmory, livekit) / `telos-db` (internal; postgres, grimmory-db, grimmory, telos-core). Note the two fixes vs. earlier drafts *(informative)*: Traefik-routed services must share `telos-ingress` with Traefik; `telos-core` must join `telos-db` to reach PostgreSQL.
2. **Secrets** — all credentials come from `.env` (never committed); compose uses `${VAR}` interpolation. Include `.env.example` as an `ini` block:

```ini
TELOS_DOMAIN=telos.local
APP_UID=1000
APP_GID=1000
TZ=Etc/UTC

POSTGRES_USER=telos
POSTGRES_PASSWORD=change-me
POSTGRES_DB=telos

REDIS_PASSWORD=change-me

LIVEKIT_API_KEY=change-me
LIVEKIT_API_SECRET=change-me

JELLYFIN_ADMIN_TOKEN=change-me
JELLYFIN_OIDC_SECRET=change-me
GRIMMORY_API_TOKEN=change-me

GRIMMORY_DB_NAME=grimmory
GRIMMORY_DB_USER=grimmory
GRIMMORY_DB_PASSWORD=change-me
MARIADB_ROOT_PASSWORD=change-me
```

3. **Compose file** — the complete corrected `docker-compose.yml` as one `yaml` block:

```yaml
networks:
  telos-ingress:
    name: telos-ingress
    driver: bridge
  telos-backend:
    name: telos-backend
    driver: bridge
    internal: true
  telos-db:
    name: telos-db
    driver: bridge
    internal: true

volumes:
  postgres_data:
  redis_data:
  jellyfin_config:
  grimmory_config:
  grimmory_db_data:

services:
  traefik:
    image: traefik:v3.3
    container_name: telos-traefik
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    networks:
      - telos-ingress
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./config/traefik.yaml:/etc/traefik/traefik.yaml:ro
      - ./config/certs:/certs:ro

  telos-core:
    build:
      context: ./backend
      dockerfile: Dockerfile
    container_name: telos-core
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    networks:
      - telos-ingress
      - telos-backend
      - telos-db
    environment:
      - DATABASE_URL=postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable
      - REDIS_URL=redis://:${REDIS_PASSWORD}@redis:6379/0
      - LIVEKIT_API_KEY=${LIVEKIT_API_KEY}
      - LIVEKIT_API_SECRET=${LIVEKIT_API_SECRET}
      - JELLYFIN_ADMIN_TOKEN=${JELLYFIN_ADMIN_TOKEN}
      - GRIMMORY_API_TOKEN=${GRIMMORY_API_TOKEN}
    volumes:
      - /mnt/storage/shared:/data/shared
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=telos-ingress"
      - "traefik.http.routers.telos-core.rule=Host(`${TELOS_DOMAIN}`)"
      - "traefik.http.routers.telos-core.entrypoints=websecure"
      - "traefik.http.routers.telos-core.middlewares=upload-limits"
      - "traefik.http.middlewares.upload-limits.buffering.maxRequestBodyBytes=104857600"
      - "traefik.http.services.telos-core.loadbalancer.server.port=8080"

  postgres:
    image: postgres:16-alpine
    container_name: telos-postgres
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    networks:
      - telos-db
    environment:
      - POSTGRES_USER=${POSTGRES_USER}
      - POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
      - POSTGRES_DB=${POSTGRES_DB}
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER} -d ${POSTGRES_DB}"]
      interval: 5s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    container_name: telos-redis
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    networks:
      - telos-backend
    command: redis-server --appendonly yes --requirepass ${REDIS_PASSWORD}
    volumes:
      - redis_data:/data
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a $$REDIS_PASSWORD ping | grep PONG"]
      interval: 5s
      timeout: 5s
      retries: 5

  jellyfin:
    # Pin to a specific release in production.
    image: jellyfin/jellyfin:latest
    container_name: telos-jellyfin
    restart: unless-stopped
    user: "${APP_UID}:${APP_GID}"
    networks:
      - telos-ingress
      - telos-backend
    environment:
      - TZ=${TZ}
      - JELLYFIN_PublishedServerUrl=https://${TELOS_DOMAIN}/jellyfin
    volumes:
      - jellyfin_config:/config
      - /mnt/storage/shared/media:/data/media:ro
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=telos-ingress"
      - "traefik.http.routers.jellyfin.rule=Host(`${TELOS_DOMAIN}`) && PathPrefix(`/jellyfin`)"
      - "traefik.http.routers.jellyfin.entrypoints=websecure"
      - "traefik.http.services.jellyfin.loadbalancer.server.port=8096"

  grimmory-db:
    image: mariadb:10.11
    container_name: telos-grimmory-db
    restart: unless-stopped
    networks:
      - telos-db
    environment:
      - MYSQL_ROOT_PASSWORD=${MARIADB_ROOT_PASSWORD}
      - MYSQL_DATABASE=${GRIMMORY_DB_NAME}
      - MYSQL_USER=${GRIMMORY_DB_USER}
      - MYSQL_PASSWORD=${GRIMMORY_DB_PASSWORD}
    volumes:
      - grimmory_db_data:/var/lib/mysql
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 10s
      timeout: 5s
      retries: 3

  grimmory:
    # Alternative registry: ghcr.io/grimmory-tools/grimmory
    image: grimmory/grimmory:latest
    container_name: telos-grimmory
    restart: unless-stopped
    depends_on:
      grimmory-db:
        condition: service_healthy
    networks:
      - telos-ingress
      - telos-backend
      - telos-db
    environment:
      - USER_ID=${APP_UID}
      - GROUP_ID=${APP_GID}
      - TZ=${TZ}
      - DATABASE_URL=jdbc:mariadb://grimmory-db:3306/${GRIMMORY_DB_NAME}
      - DATABASE_USERNAME=${GRIMMORY_DB_USER}
      - DATABASE_PASSWORD=${GRIMMORY_DB_PASSWORD}
      - API_DOCS_ENABLED=true
      - DISK_TYPE=LOCAL
    volumes:
      - grimmory_config:/app/data
      - /mnt/storage/shared/books:/books
      - /mnt/storage/shared/bookdrop:/bookdrop
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O - http://localhost:6060/api/v1/healthcheck"]
      interval: 60s
      timeout: 10s
      retries: 5
      start_period: 60s
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=telos-ingress"
      - "traefik.http.routers.grimmory.rule=Host(`${TELOS_DOMAIN}`) && PathPrefix(`/grimmory`)"
      - "traefik.http.routers.grimmory.entrypoints=websecure"
      - "traefik.http.services.grimmory.loadbalancer.server.port=6060"

  livekit:
    image: livekit/livekit-server:v1.10
    container_name: telos-livekit
    restart: unless-stopped
    command: --config /etc/livekit/config.yaml
    depends_on:
      redis:
        condition: service_healthy
    networks:
      - telos-ingress
      - telos-backend
    ports:
      - "7881:7881"                      # WebRTC over TCP fallback
      - "3478:3478/udp"                  # STUN/TURN
      - "50000-50100:50000-50100/udp"    # RTP media range
    volumes:
      - ./config/livekit.yaml:/etc/livekit/config.yaml:ro
    labels:
      - "traefik.enable=true"
      - "traefik.docker.network=telos-ingress"
      - "traefik.http.routers.livekit.rule=Host(`${TELOS_DOMAIN}`) && PathPrefix(`/livekit`)"
      - "traefik.http.routers.livekit.entrypoints=websecure"
      - "traefik.http.services.livekit.loadbalancer.server.port=7880"
```

4. **Media transport note** — Traefik proxies only LiveKit's HTTP/WebSocket signaling; RTP media flows directly between clients and the `livekit` container on the published UDP/TCP ports. Never route media through the HTTP proxy.
5. **Storage layout** — `/mnt/storage/shared/{media,books,bookdrop}` with roles as in the original (Jellyfin libraries read-only; uploads from the Telos file manager land in `bookdrop`; Grimmory's watcher ingests, pulls metadata from Google Books / Open Library, queues for review). `DISK_TYPE=LOCAL` explanation: transactional renames instead of unsafe concurrent writes.

- [ ] **Step 2: Verify**

Run: `python3 <scratchpad>/check_yaml.py`
Expected: `YAML OK`.
Run: `grep -nE "cite:|TODO|TBD|SecurePass|SecureRedis|SecureMariaDB|LK_API" documentation/architecture/02-deployment.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add documentation/architecture/02-deployment.md
git commit -m "docs: add deployment spec with corrected compose topology (architecture/02)"
```

---

### Task 8: `documentation/architecture/03-gateway-and-api.md`

**Files:**
- Create: `documentation/architecture/03-gateway-and-api.md`

**Interfaces:**
- Consumes: compose labels/networks from Task 7.
- Produces: the `/api/v1/*` endpoint names Task 9's frontend doc may reference.

- [ ] **Step 1: Write the file** with H1 `# Gateway & API Integration`, audience preamble, sections:

1. **Routing model** — single origin `https://${TELOS_DOMAIN}`; all routing declared as Docker labels on the services (single source of truth — the static file below carries only entrypoints/provider/TLS). Traefik v3 does not buffer responses by default, so HLS segments and Range requests stream through untouched; the only buffering middleware is the 100 MB upload limit on `telos-core`. Same-origin design means no CORS headers are needed.
2. **Static Traefik configuration** — complete `config/traefik.yaml` as a `yaml` block:

```yaml
entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint:
          to: websecure
          scheme: https
  websecure:
    address: ":443"

providers:
  docker:
    exposedByDefault: false
    network: telos-ingress

tls:
  certificates:
    - certFile: /certs/telos.crt
      keyFile: /certs/telos.key
```

3. **Headless API integration matrix** — markdown table with columns Component / Telos endpoint / Internal headless request / Aggregation logic, rows exactly: Media Stream `GET /api/v1/media` → Jellyfin `GET /Users/{userId}/Views`; Media Catalog `GET /api/v1/media/items` → `GET /Users/{userId}/Items`; Audio Stream `GET /api/v1/stream/audio/{id}` → `GET /Audio/{itemId}/stream`; Video Playback `GET /api/v1/stream/video/{id}` → `GET /Videos/{itemId}/hls/{playlistId}/stream.m3u8`; Digital Library `GET /api/v1/library/books` → Grimmory `GET /api/v1/books`; Library Facets `GET /api/v1/library/facets` → `GET /api/v1/books/facets`; Read Progress `POST /api/v1/library/progress` → `POST /api/v1/books/progress`. Keep the original one-line logic descriptions, cleaned.
4. **Unified identity (OIDC/SSO)** — core is the OIDC IdP at `/api/v1/auth/oidc`; proxy interceptor auto-provisions child-service profiles. Jellyfin via `jellyfin-plugin-sso`, configured with this `bash` block (secrets as env refs):

```bash
curl -X POST "https://${TELOS_DOMAIN}/jellyfin/sso/OID/Add/telos-idp?api_key=${JELLYFIN_ADMIN_TOKEN}" \
     -H "Content-Type: application/json" \
     -d '{
       "oidEndpoint": "https://'"${TELOS_DOMAIN}"'/api/v1/auth/oidc",
       "oidClientId": "telos-jellyfin-client",
       "oidSecret": "'"${JELLYFIN_OIDC_SECRET}"'",
       "enabled": true,
       "enableAuthorization": true,
       "enableAllFolders": true,
       "defaultUsernameClaim": "preferred_username"
     }'
```

Grimmory: built-in OIDC via environment variables; claim templates sync usernames, groups, and admin flags at startup.

- [ ] **Step 2: Verify**

Run: `python3 <scratchpad>/check_yaml.py`
Expected: `YAML OK`.
Run: `grep -nE "cite:|TODO|TBD|Access-Control-Allow-Origin|remote-ip" documentation/architecture/03-gateway-and-api.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add documentation/architecture/03-gateway-and-api.md
git commit -m "docs: add gateway config and API matrix (architecture/03)"
```

---

### Task 9: `documentation/architecture/04-frontend-architecture.md`

**Files:**
- Create: `documentation/architecture/04-frontend-architecture.md`

**Interfaces:**
- Consumes: design-system rules (Task 2), endpoints (Task 8).

- [ ] **Step 1: Write the file** with H1 `# Frontend Architecture`, audience preamble, sections:

1. **Principle: real-time state lives outside the component tree** — if WebRTC state lived in component state, route navigation would unmount it and drop calls; Zustand stores hold it at module scope instead.
2. **Voice session store** — complete corrected `typescript` block (livekit-client v2 API — `remoteParticipants`, `audioCaptureDefaults`; state reset driven by the `Disconnected` event):

```typescript
import { create } from 'zustand';
import { ConnectionState, Participant, Room, RoomEvent } from 'livekit-client';

interface VoiceSessionState {
  room: Room | null;
  activeChannelId: string | null;
  connectionStatus: ConnectionState;
  remoteParticipants: Participant[];
  isMuted: boolean;

  joinVoiceRoom: (gatewayUrl: string, userJwt: string, targetChannel: string) => Promise<void>;
  terminateVoiceSession: () => Promise<void>;
  toggleMicrophone: () => Promise<void>;
}

export const useVoiceSessionStore = create<VoiceSessionState>((set, get) => ({
  room: null,
  activeChannelId: null,
  connectionStatus: ConnectionState.Disconnected,
  remoteParticipants: [],
  isMuted: false,

  joinVoiceRoom: async (gatewayUrl, userJwt, targetChannel) => {
    // Tear down any existing call before joining a new one.
    await get().room?.disconnect();

    const room = new Room({
      adaptiveStream: true,
      dynacast: true,
      audioCaptureDefaults: {
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
      },
    });

    const syncParticipants = () =>
      set({ remoteParticipants: Array.from(room.remoteParticipants.values()) });

    room
      .on(RoomEvent.ConnectionStateChanged, (status) => set({ connectionStatus: status }))
      .on(RoomEvent.ParticipantConnected, syncParticipants)
      .on(RoomEvent.ParticipantDisconnected, syncParticipants)
      .on(RoomEvent.Disconnected, () =>
        set({
          room: null,
          activeChannelId: null,
          connectionStatus: ConnectionState.Disconnected,
          remoteParticipants: [],
        }),
      );

    set({ connectionStatus: ConnectionState.Connecting });
    try {
      await room.connect(gatewayUrl, userJwt);
      await room.localParticipant.setMicrophoneEnabled(true);
      set({
        room,
        activeChannelId: targetChannel,
        connectionStatus: ConnectionState.Connected,
        remoteParticipants: Array.from(room.remoteParticipants.values()),
        isMuted: false,
      });
    } catch (error) {
      set({ connectionStatus: ConnectionState.Disconnected });
      throw error;
    }
  },

  terminateVoiceSession: async () => {
    // State reset happens in the RoomEvent.Disconnected handler.
    await get().room?.disconnect();
  },

  toggleMicrophone: async () => {
    const { room, isMuted } = get();
    if (!room) return;
    await room.localParticipant.setMicrophoneEnabled(isMuted);
    set({ isMuted: !isMuted });
  },
}));
```

3. **Persistent app shell** — complete corrected `tsx` block (design-system compliant call bar; `ConnectionState` imported; recoverable-destructive "Leave" per tokens §6, so no confirmation):

```tsx
import React from 'react';
import { ConnectionState } from 'livekit-client';
import { Mic, MicOff, PhoneOff } from 'lucide-react';
import { useVoiceSessionStore } from '../stores/useVoiceSessionStore';
import { SidebarNavigation } from './SidebarNavigation';
import { SubModuleRenderer } from './SubModuleRenderer';

export const CoreAppShell: React.FC = () => {
  const { connectionStatus, activeChannelId, isMuted, toggleMicrophone, terminateVoiceSession } =
    useVoiceSessionStore();

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-slate-950 font-sans text-slate-50 antialiased">
      <SidebarNavigation />

      <main className="relative flex min-w-0 flex-1 flex-col bg-slate-900">
        <SubModuleRenderer />
      </main>

      {/* Floating call bar: mounted at shell level so navigation never drops the call. */}
      {connectionStatus === ConnectionState.Connected && activeChannelId && (
        <div className="absolute bottom-6 right-6 z-50 flex items-center gap-4 rounded-xl border border-sky-500/20 bg-slate-950 p-4">
          <div className="flex flex-col gap-0.5">
            <span className="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-wider text-sky-400">
              <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-sky-500 motion-reduce:animate-none" />
              Voice Active
            </span>
            <span className="font-mono text-xs text-slate-400">{activeChannelId}</span>
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={toggleMicrophone}
              aria-label={isMuted ? 'Unmute microphone' : 'Mute microphone'}
              className={`rounded-lg p-2 transition-colors ${
                isMuted
                  ? 'bg-slate-50 text-slate-900'
                  : 'bg-slate-800 text-slate-200 hover:bg-slate-700'
              }`}
            >
              {isMuted ? <MicOff className="h-4 w-4" /> : <Mic className="h-4 w-4" />}
            </button>
            <button
              onClick={terminateVoiceSession}
              aria-label="Leave voice channel"
              className="flex items-center gap-1.5 rounded-lg bg-slate-50 px-3 py-2 text-xs font-semibold text-slate-900 transition-colors hover:bg-white"
            >
              <PhoneOff className="h-4 w-4" />
              Leave
            </button>
          </div>
        </div>
      )}
    </div>
  );
};
```

4. **Module rendering** — `SubModuleRenderer` swaps module pages (chat/stream/books/files); page DOM is disposable, shell + stores are not. Recommended stack *(informative)*: React + TypeScript + Zustand, HLS.js for the media player; adapt to host framework per `../design/05-reference-implementation.md`.

5. **Theming state** — theme selection (`light` / `dark` / `vaporwave`) lives in a small persisted UI store (Zustand + localStorage) and is applied as a `data-theme` attribute on the root element; components consume the CSS custom properties defined in `../design/02-design-tokens.md` rather than branching per theme. The call bar above is flat per the design system's flat rule (solid surface + border, no elevation utilities).

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|emerald|amber|rose-|cyan-|audioDefaults|\.participants\b|shadow-[a-z0-9]|backdrop-blur" documentation/architecture/04-frontend-architecture.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add documentation/architecture/04-frontend-architecture.md
git commit -m "docs: add frontend architecture with corrected livekit code (architecture/04)"
```

---

### Task 10: `documentation/architecture/05-roadmap-and-licensing.md`

**Files:**
- Create: `documentation/architecture/05-roadmap-and-licensing.md`

**Interfaces:**
- Consumes: service names (Task 6).

- [ ] **Step 1: Write the file** with H1 `# Roadmap & Licensing`, audience preamble, sections:

1. **Phased roadmap** — table with columns Stage / Weeks / Scope / Primary tasks / Risk & mitigation, four rows: Phase 1 Foundation (1–6): Postgres schemas, Redis cache, WebSocket chat core, gateway proxy config; risk Redis state collisions → isolated key prefixes. Phase 2 Storage (7–12): volume mounts, headless Jellyfin paths, HLS.js player; risk disk-IO stalls → non-blocking Go channels. Phase 3 Catalog (13–18): Grimmory modules, watch folders, EPUB/PDF reader controls; risk metadata desync → database triggers. Phase 4 Real-Time (19–24): LiveKit server, Zustand voice handlers, latency tuning; risk packet jitter behind NAT → STUN/TURN.
2. **Copyleft boundary analysis** — derivative-work risk under GPL/AGPL if linked; Telos avoids it via four patterns: process separation (separate containers), standardized network APIs only (HTTP/JSON/sockets), no build-time linking of copyleft code, independent operability (any child can stop; gateway keeps serving the rest — "mere aggregation"). Consequence: Telos core/client may ship under MIT or Apache-2.0.
3. **Modification obligations** — modifying Jellyfin → release under GPL-2.0 (or later where applicable); modifying Grimmory → AGPL-3.0 §13 network source disclosure applies.
4. **Compliance verification** — audits must confirm no upstream binaries are statically compiled into the gateway executable; all inter-service calls over network interfaces.
5. **Attributions (Credits module + README template)** — table: Jellyfin / `https://github.com/jellyfin/jellyfin` / GPL-2.0 / transcoding + HLS · Grimmory / `https://github.com/grimmory-tools/grimmory` / AGPL-3.0 / e-book & comic catalog, metadata, reader backend · LiveKit / `https://github.com/livekit/livekit` / Apache-2.0 / WebRTC SFU, rooms, signaling. Plus one line noting infrastructure components (Traefik, PostgreSQL, Redis, MariaDB) under their own permissive/copyleft licenses. State that this table must render in-app under a dedicated "Credits" module and in the project README.

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|grimmory-tools\b.*Booklore successor claims" documentation/architecture/05-roadmap-and-licensing.md; grep -c "AGPL-3.0" documentation/architecture/05-roadmap-and-licensing.md`
Expected: first grep no output; second grep ≥ 1.

- [ ] **Step 3: Commit**

```bash
git add documentation/architecture/05-roadmap-and-licensing.md
git commit -m "docs: add roadmap and licensing analysis (architecture/05)"
```

---

### Task 11: `documentation/README.md`, delete originals, full verification

**Files:**
- Create: `documentation/README.md`
- Delete: `documentation/design.md`, `documentation/implementation.md`

**Interfaces:**
- Consumes: every file from Tasks 1–10 (links to all of them).

- [ ] **Step 1: Write `documentation/README.md`** — H1 `# Telos Documentation`, audience preamble, then:

1. **Vision** — 2–3 sentences: Telos merges Discord-style chat/voice, Jellyfin-style streaming, Grimmory-style publication management, and file management into one self-hosted, single-origin application; an infrastructure of digital sovereignty. Quote both slogans.
2. **Quick facts** — table: default host `telos.local`; stack Go/Rust core + React/TS/Zustand client; services Traefik, PostgreSQL 16, Redis 7, Jellyfin, Grimmory, MariaDB 10.11, LiveKit; themes flat light / flat dark (default) / vaporwave; license target MIT/Apache-2.0 core with GPL/AGPL headless children (see licensing doc).
3. **Document map** — two tables (Design, Architecture), one row per file with relative link and one-line purpose, exactly matching the 11 files created in Tasks 1–10 and 12–16 (design/01–06, architecture/01–05).
4. **Reading order** — for UI work start at `design/01`; implementing agents building the frontend should start from the ready-to-use prompts in `design/06-design-prompts.md` and the repo-root agent guide `../AGENTS.md`; for deployment `architecture/01` → `02`; note that `resources/Telos_[1-3].png` are the canonical logo renders and `resources/Telos_vaporwave.png` is the expected path for the owner-supplied vaporwave logo.
5. **History note** *(informative)* — this set supersedes the original `design.md`/`implementation.md`, preserved in git history.

- [ ] **Step 2: Delete the originals**

```bash
git rm documentation/design.md documentation/implementation.md
```

- [ ] **Step 3: Full verification suite**

```bash
grep -rn "cite:" documentation/ AGENTS.md ; echo "exit=$?"             # expect no matches, exit=1
grep -rnE "TODO|TBD" documentation/ AGENTS.md ; echo "exit=$?"         # expect no matches, exit=1
grep -rnE "cyan-|emerald|amber|rose-" documentation/ AGENTS.md \
  | grep -v "02-design-tokens.md" ; echo "exit=$?"                     # expect no matches, exit=1
grep -rnE "shadow-[a-z0-9]|backdrop-blur" documentation/ AGENTS.md      # expect no matches (flat rule)
grep -rnE "SecurePass|SecureRedis|SecureMariaDB|z-25" documentation/    # expect no matches
python3 <scratchpad>/check_yaml.py                                      # expect YAML OK
ls documentation/design documentation/architecture                      # expect 6 and 5 files
test -f AGENTS.md && echo "AGENTS.md present"                           # expect present
```

Then check every relative link resolves:

```bash
python3 - <<'EOF'
import pathlib, re, sys
bad = 0
for p in pathlib.Path("documentation").rglob("*.md"):
    for target in re.findall(r"\]\((?!https?://|#)([^)#]+)", p.read_text()):
        if not (p.parent / target).resolve().exists():
            bad += 1
            print(f"{p}: broken link -> {target}")
print("LINKS OK" if not bad else f"{bad} broken links")
sys.exit(1 if bad else 0)
EOF
```

Expected: `LINKS OK`.

- [ ] **Step 4: Commit**

```bash
git add documentation/README.md
git commit -m "docs: add documentation index; remove superseded monolithic docs"
```

---

## Amendment tasks (approved 2026-07-08): theming + Antigravity

Execution order for remaining work: 12 → 13 → 14 → 15 → 16 → 9 → 10 → 17 → 11.
IMPORTANT for Tasks 13, 16, 17 and 11: `resources/Telos_vaporwave.png` does not
exist yet (owner-supplied later) — reference that path as inline code only,
NEVER as a markdown link, or the link checker in Task 11 fails.

### Task 12: Retrofit `documentation/design/02-design-tokens.md` — theming + flat rule

**Files:**
- Modify: `documentation/design/02-design-tokens.md`

**Interfaces:**
- Consumes: existing sections 1–7 of the file.
- Produces: section "8. Theming" and the flat rule that Tasks 13–16 and 9 reference; the accent-role mapping (Sovereign Blue role ↔ hot pink in vaporwave).

- [ ] **Step 1: Edit the file**

1. In section 1 (Color system), after the hard rule, add one scoping sentence: the grayscale + single-blue rule governs the light and dark themes; the vaporwave theme (§8) substitutes its own accent set, but the ban on *semantic* accent hues (state meaning carried by color) holds in every theme — states stay structural per §6.
2. Amend the "at most one Sovereign Blue emphasis per visual region" rule to: at most one accent emphasis per visual region (Sovereign Blue in light/dark; hot pink in vaporwave).
3. In section 5 (Borders & radii) add an "Elevation (flat rule)" subsection: light and dark are strictly flat — no box-shadows, no backdrop-blur, no gradients; separation via 1px borders and surface steps; the only sanctioned deviations live in §8 (vaporwave glow) and the vaporwave logo variant (gradient, see `01-brand-identity.md`).
4. Append new section `## 8. Theming`:
   - Mechanism: themes applied as `data-theme="light" | "dark" | "vaporwave"` on the root element; tokens exposed as CSS custom properties; components consume vars, never branch per theme; dark is default; selection persisted (localStorage).
   - Vaporwave palette table (hex only, with role column): surface `#0D0221`, panel `#1A0B3B`, border `#2E1A5E`, text `#F8F8FF`, secondary `#C8BFE7`, hot pink `#FF71CE` primary actions (the role Sovereign Blue has in light/dark), neon cyan `#01CDFE` links/active/live, purple `#B967FF` badge wash (`#B967FF` at ~15% alpha bg + `#B967FF` text).
   - Glow exception: vaporwave alone may use neon glow, only on live/active indicators and the logo, written as a CSS property, e.g. `box-shadow: 0 0 12px rgba(1, 205, 254, 0.35);` — never a Tailwind class.
   - One line: semantic-state patterns in §6 are theme-independent; only the accent hue serving each pattern changes with the theme.
   - Cross-link: theme selector spec lives in `03-application-shell.md`; vaporwave logo variant in `01-brand-identity.md`.

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|cyan-|shadow-[a-z0-9]|backdrop-blur" documentation/design/02-design-tokens.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add documentation/design/02-design-tokens.md
git commit -m "docs: add theming section (flat rule + vaporwave) to design tokens (design/02)"
```

---

### Task 13: Retrofit `documentation/design/01-brand-identity.md` — vaporwave logo variant

**Files:**
- Modify: `documentation/design/01-brand-identity.md`

**Interfaces:**
- Consumes: tokens §8 (Task 12) for the glow rule.
- Produces: the vaporwave logo usage rules Tasks 15/16 reference.

- [ ] **Step 1: Edit the file** — append a new H2 `## Vaporwave logo variant`:

1. The variant artwork is supplied by the project owner; once delivered it lives at `resources/Telos_vaporwave.png` (inline code, NOT a markdown link — the file does not exist yet) as the canonical render.
2. Usage: shown ONLY when `data-theme="vaporwave"`; all other themes use the standard monochrome mark.
3. The in-app vector derived from the artwork may use a hot-pink→neon-cyan gradient (`#FF71CE` → `#01CDFE`) — the single sanctioned gradient in the product — and may carry the neon glow per the tokens doc §8.
4. Fallback: until the asset is integrated, implementations use the standard monochrome mark, which inherits vaporwave's text color via `currentColor`.
5. Cross-link tokens §8 (relative link with anchor).

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|cyan-|shadow-[a-z0-9]|backdrop-blur" documentation/design/01-brand-identity.md; grep -n "](.*Telos_vaporwave" documentation/design/01-brand-identity.md`
Expected: no output from either grep (second confirms no markdown link to the missing asset).

- [ ] **Step 3: Commit**

```bash
git add documentation/design/01-brand-identity.md
git commit -m "docs: spec vaporwave logo variant usage (design/01)"
```

---

### Task 14: Retrofit `documentation/design/03-application-shell.md` — theme selector + vaporwave a11y

**Files:**
- Modify: `documentation/design/03-application-shell.md`

**Interfaces:**
- Consumes: tokens §8 (Task 12).
- Produces: the three-state theme selector spec Tasks 15/16 reference.

- [ ] **Step 1: Edit the file**

1. In section 2 (Global Header): replace the "Sun/Moon theme toggle" item with: theme selector — a three-state control cycling light → dark → vaporwave, lucide icons Sun (light), Moon (dark), Waves (vaporwave), `aria-label` announcing the NEXT theme (e.g. `Switch theme (next: vaporwave)`); applies `data-theme` per the tokens doc §8 (cross-link).
2. In section 8 (Accessibility), contrast table: append five vaporwave rows — `#F8F8FF` on `#0D0221` ≈ 18:1 (pass); `#FF71CE` on `#0D0221` ≈ 8:1 (pass); `#01CDFE` on `#0D0221` ≈ 11:1 (pass); `#B967FF` on `#0D0221` ≈ 6:1 (pass); `#C8BFE7` on `#0D0221` ≈ 12:1 (pass).
3. In the Focus subsection add: the focus ring color follows the theme accent — `sky-500` in light/dark, `#01CDFE` in vaporwave (via the ring CSS var).

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|cyan-|emerald|amber|rose-|shadow-[a-z0-9]|backdrop-blur" documentation/design/03-application-shell.md`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add documentation/design/03-application-shell.md
git commit -m "docs: three-state theme selector + vaporwave contrast rows (design/03)"
```

---

### Task 15: Retrofit `documentation/design/05-reference-implementation.md` — flat, three-theme snippet

**Files:**
- Modify: `documentation/design/05-reference-implementation.md`

**Interfaces:**
- Consumes: tokens §8, shell theme selector (Tasks 12/14).

- [ ] **Step 1: Replace the tsx code block** with EXACTLY this (byte-for-byte):

```tsx
import React, { useState } from 'react';
import {
  Activity, BookOpen, Folder, MessageSquare, Moon, Search, Settings,
  Sparkles, Sun, Tv, Waves,
} from 'lucide-react';

const MODULES = [
  { id: 'chat', icon: MessageSquare, label: 'Chat' },
  { id: 'stream', icon: Tv, label: 'Stream' },
  { id: 'books', icon: BookOpen, label: 'Books' },
  { id: 'files', icon: Folder, label: 'Files' },
] as const;

const THEMES = ['light', 'dark', 'vaporwave'] as const;
type Theme = (typeof THEMES)[number];

// Structural reference only: production consumes the CSS custom properties
// defined in design/02-design-tokens.md (section 8) instead of class maps.
const T: Record<Theme, {
  app: string; header: string; nav: string; sidebar: string; sla: string;
  input: string; searchIcon: string; active: string; idle: string;
  avatar: string; eye: string;
}> = {
  light: {
    app: 'bg-[#F1F5F9] text-[#111827]',
    header: 'border-slate-200 bg-white',
    nav: 'border-slate-200 bg-[#F8FAFC]',
    sidebar: 'border-slate-200 bg-white',
    sla: 'border-slate-200 bg-white',
    input:
      'border-slate-300 bg-slate-100 text-[#111827] placeholder-slate-500 focus:border-sky-400 focus:bg-white focus-visible:ring-sky-500',
    searchIcon: 'group-focus-within:text-sky-500',
    active: 'bg-sky-500/10 text-sky-600',
    idle: 'text-slate-500 hover:bg-slate-200 hover:text-slate-700',
    avatar: 'bg-slate-200 text-slate-700',
    eye: '#FFFFFF',
  },
  dark: {
    app: 'bg-[#0B0D13] text-[#F8FAFC]',
    header: 'border-slate-800 bg-[#121620]',
    nav: 'border-slate-800 bg-[#10131C]',
    sidebar: 'border-slate-800 bg-[#151924]',
    sla: 'border-slate-800 bg-[#121621]',
    input:
      'border-slate-700 bg-[#1A1E29] text-[#F8FAFC] placeholder-slate-500 focus:border-sky-500/50 focus-visible:ring-sky-500',
    searchIcon: 'group-focus-within:text-sky-500',
    active: 'bg-sky-500/10 text-sky-400',
    idle: 'text-slate-400 hover:bg-slate-800/50 hover:text-slate-200',
    avatar: 'bg-slate-800 text-slate-200',
    eye: '#0B0D13',
  },
  vaporwave: {
    app: 'bg-[#0D0221] text-[#F8F8FF]',
    header: 'border-[#2E1A5E] bg-[#1A0B3B]',
    nav: 'border-[#2E1A5E] bg-[#120732]',
    sidebar: 'border-[#2E1A5E] bg-[#160939]',
    sla: 'border-[#2E1A5E] bg-[#160939]',
    input:
      'border-[#2E1A5E] bg-[#120732] text-[#F8F8FF] placeholder-[#C8BFE7] focus:border-[#01CDFE] focus-visible:ring-[#01CDFE]',
    searchIcon: 'group-focus-within:text-[#01CDFE]',
    active: 'bg-[#B967FF]/15 text-[#FF71CE]',
    idle: 'text-[#C8BFE7] hover:bg-[#2E1A5E]/60 hover:text-[#F8F8FF]',
    avatar: 'bg-[#2E1A5E] text-[#F8F8FF]',
    eye: '#0D0221',
  },
};

const THEME_ICONS: Record<Theme, typeof Sun> = {
  light: Sun,
  dark: Moon,
  vaporwave: Waves,
};

export default function App() {
  const [theme, setTheme] = useState<Theme>('dark');
  const [activeModule, setActiveModule] =
    useState<'chat' | 'stream' | 'books' | 'files'>('chat');

  const t = T[theme];
  const nextTheme = THEMES[(THEMES.indexOf(theme) + 1) % THEMES.length];
  const NextThemeIcon = THEME_ICONS[nextTheme];

  return (
    <div
      data-theme={theme}
      className={`flex min-h-screen flex-col font-sans transition-colors duration-300 ${t.app}`}
    >
      {/* 1. Global Header */}
      <header
        className={`z-20 flex h-14 items-center justify-between border-b px-6 transition-colors ${t.header}`}
      >
        <div className="flex w-48 items-center gap-2">
          {/* Ouroboros mark + HTML wordmark (design/01-brand-identity.md).
              Vaporwave mode swaps in the owner-supplied logo variant once it
              lands; this monochrome mark is the documented fallback. */}
          <svg viewBox="0 0 100 100" className="h-10 w-10" role="img" aria-label="Telos ouroboros mark">
            <path d="M 33 14 A 40 40 0 1 1 12 40" fill="none" stroke="currentColor"
                  strokeWidth="4.5" strokeLinecap="round" />
            <path d="M 34 14 C 26 10, 18 16, 21 24 C 24 29, 32 27, 36 22 C 39 18, 38 15, 34 14 Z"
                  fill="currentColor" />
            <circle cx="26" cy="19" r="1.5" fill={t.eye} />
          </svg>
          <span className="text-lg font-semibold tracking-wide">Telos</span>
        </div>

        {/* Global search */}
        <div className="mx-8 hidden max-w-xl flex-1 md:flex">
          <div className="group relative w-full">
            <Search className={`absolute left-4 top-2 h-4 w-4 text-slate-400 transition-colors ${t.searchIcon}`} />
            <input
              type="text"
              placeholder="Title, Author, Series, Genre, or Tags..."
              className={`w-full rounded-lg border py-1.5 pl-11 pr-4 text-sm transition-all focus:outline-none focus-visible:ring-2 ${t.input}`}
            />
          </div>
        </div>

        {/* Quick actions */}
        <div className="flex w-48 items-center justify-end gap-3">
          <button aria-label="Activity" className={t.idle}><Activity className="h-4 w-4" /></button>
          <button aria-label="AI features" className={t.idle}><Sparkles className="h-4 w-4" /></button>
          <button aria-label="Settings" className={t.idle}><Settings className="h-4 w-4" /></button>
          <button
            aria-label={`Switch theme (next: ${nextTheme})`}
            onClick={() => setTheme(nextTheme)}
            className={t.idle}
          >
            <NextThemeIcon className="h-4 w-4" />
          </button>
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {/* 2. Global Nav Strip */}
        <nav
          className={`flex w-[72px] flex-shrink-0 flex-col items-center justify-between border-r py-4 transition-colors ${t.nav}`}
        >
          <div className="flex flex-col gap-2">
            {MODULES.map(({ id, icon: Icon, label }) => (
              <button
                key={id}
                aria-label={label}
                onClick={() => setActiveModule(id)}
                className={`rounded-xl p-3 transition-colors ${
                  activeModule === id ? t.active : t.idle
                }`}
              >
                <Icon className="h-5 w-5" />
              </button>
            ))}
          </div>
          <button
            aria-label="Profile: AD"
            className={`flex h-10 w-10 items-center justify-center rounded-full text-xs font-semibold ${t.avatar}`}
          >
            AD
          </button>
        </nav>

        {/* 3. Contextual Sidebar */}
        <aside
          className={`hidden w-64 flex-shrink-0 flex-col border-r transition-colors lg:flex ${t.sidebar}`}
        >
          <div className="flex-1 overflow-y-auto py-3">{/* module sub-navigation */}</div>
        </aside>

        {/* 4. Central Arena */}
        <main className="flex flex-1 flex-col overflow-hidden">
          {/* active module renders here */}
        </main>

        {/* 5. Server SLA Sidebar */}
        <aside
          className={`hidden w-64 flex-shrink-0 flex-col border-l transition-colors xl:flex ${t.sla}`}
        >
          {/* server specs, slogans, peers */}
        </aside>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Update the corrections list** — append one item with EXACTLY this wording: "elevation utilities removed for the flat rule; boolean dark-mode state replaced by a three-theme selector (light, dark, vaporwave) per the tokens doc."

- [ ] **Step 3: Verify**

Run: `grep -nE "cite:|TODO|TBD|cyan-|emerald|amber|rose-|z-25|shadow-[a-z0-9]|backdrop-blur|isDarkMode" documentation/design/05-reference-implementation.md`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add documentation/design/05-reference-implementation.md
git commit -m "docs: flat three-theme reference shell (design/05)"
```

---

### Task 16: Create `documentation/design/06-design-prompts.md` — sample frontend design prompts

**Files:**
- Create: `documentation/design/06-design-prompts.md`

**Interfaces:**
- Consumes: tokens (Task 12), shell (Task 14), brand (Task 13), modules (design/04).

- [ ] **Step 1: Write the file** — H1 `# Frontend Design Prompts`, audience preamble, then:

1. **How to use** — these are ready-to-paste prompts for any coding agent (Antigravity, Claude Code, Cursor) implementing the Telos frontend; each is self-contained, but when the repo is available the agent should also read `02-design-tokens.md`, `03-application-shell.md`, and `04-modules.md` (relative links). One prompt per theme; build dark first (default), then light, then vaporwave.
2. Three H2 sections — "Prompt: flat dark (default)", "Prompt: flat light", "Prompt: vaporwave" — each containing ONE fenced ```text block with a complete prompt. Every prompt must include, in its own words:
   - Subject grounding: Telos, a self-hosted sovereign platform (chat/voice, media streaming, book library, file manager) in a 4-pane shell (Global Header, Global Nav Strip, Contextual Sidebar, Central Arena, Server SLA Sidebar); audience = self-hosters; the shell's job is calm, dense utility.
   - The theme's exact palette as hex values with roles (dark: `#0B0D13`/`#121620`/`#F8FAFC`/slate secondaries/accent `#0EA5E9`; light: `#F1F5F9`/`#FFFFFF`/`#111827`/slate borders/accent `#0EA5E9`; vaporwave: `#0D0221`/`#1A0B3B`/`#2E1A5E`/`#F8F8FF`/`#C8BFE7` + `#FF71CE` primary, `#01CDFE` links/active/live, `#B967FF` badge wash).
   - Flat discipline: no box-shadows, no backdrop-blur, no gradients; 1px borders + surface steps for separation. In the vaporwave prompt ONLY: the two sanctioned deviations — neon glow (CSS `box-shadow:` property form) on live/active indicators and the logo; gradient only in the logo variant (owner-supplied artwork expected at `resources/Telos_vaporwave.png`, inline code not a link; fallback = monochrome mark via currentColor).
   - Typography: Inter/Geist Sans UI + JetBrains Mono for metrics/paths/logs; serif only inside the book reader.
   - Semantic states carried structurally (icon + weight + copy + confirmation for irreversible actions), never by extra hues; at most one accent emphasis per visual region.
   - Iconography: lucide only, no emojis; radii 8px inputs/buttons, 12–16px cards.
   - Quality floor: responsive tiers (≥xl 4-pane; <xl SLA hidden; <lg sidebar drawer; <md bottom tab bar), visible `focus-visible` rings in the theme accent, `prefers-reduced-motion` respected, `aria-label` on icon-only controls.
   - Deliverable: the shell + one module screen (chat), matching `05-reference-implementation.md`'s structure.
3. Close with an *(informative)* note: prompts deliberately restate token values so they work standalone; the token doc remains the source of truth when they disagree.

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|cyan-|emerald|amber|rose-|shadow-[a-z0-9]|backdrop-blur" documentation/design/06-design-prompts.md; grep -n "](.*Telos_vaporwave" documentation/design/06-design-prompts.md`
Expected: no output from either.

- [ ] **Step 3: Commit**

```bash
git add documentation/design/06-design-prompts.md
git commit -m "docs: add ready-to-paste frontend design prompts (design/06)"
```

---

### Task 17: Create repo-root `AGENTS.md` — agent execution guide

**Files:**
- Create: `AGENTS.md` (repo root)

**Interfaces:**
- Consumes: the full documentation set (Tasks 1–16) and roadmap (Task 10).

- [ ] **Step 1: Write the file** — H1 `# Telos — Agent Guide`, then:

1. **What this repo is** — documentation-first repo for Telos (one paragraph, both slogans quoted); no application code yet; these docs are the buildable spec.
2. **Read this first** — table mapping intent → entry point: build the frontend → `documentation/design/06-design-prompts.md` (ready-to-paste prompts) then `documentation/design/01`–`05`; deploy/infra → `documentation/architecture/01`–`03`; frontend state → `documentation/architecture/04`; roadmap/licensing → `documentation/architecture/05`; full index → `documentation/README.md`.
3. **Build order** — condensed from the roadmap: Phase 1 chat core (Postgres/Redis/WS gateway), Phase 2 storage+media (volumes, Jellyfin, HLS.js), Phase 3 catalog (Grimmory, watch folders, reader), Phase 4 real-time (LiveKit, voice store).
4. **Hard constraints digest** — grayscale + single accent per theme; three themes (flat light, flat dark default, vaporwave per tokens §8); flat rule; lucide icons only, no emojis; semantic states structural; secrets via `.env` only; copyleft boundary: never link Jellyfin/Grimmory code into the core (process separation, see licensing doc).
5. **Verify your work** — plain-shell block any agent can run: the cite/TODO grep, the palette-class grep, the flat-rule grep, a python one-liner YAML parse of fenced blocks, `docker compose config` once a compose file exists in the implementation.
6. Note: `resources/Telos_[1-3].png` canonical logo renders; `resources/Telos_vaporwave.png` expected owner-supplied vaporwave variant (inline code, not a link).

- [ ] **Step 2: Verify**

Run: `grep -nE "cite:|TODO|TBD|cyan-|emerald|amber|rose-|shadow-[a-z0-9]|backdrop-blur" AGENTS.md; grep -n "](.*Telos_vaporwave" AGENTS.md`
Expected: no output from either. Then check AGENTS.md's own relative links resolve:

```bash
python3 - <<'EOF'
import pathlib, re, sys
p = pathlib.Path("AGENTS.md")
bad = [t for t in re.findall(r"\]\((?!https?://|#)([^)#]+)", p.read_text())
       if not (p.parent / t).resolve().exists()]
print("LINKS OK" if not bad else f"broken: {bad}")
sys.exit(1 if bad else 0)
EOF
```

Expected: `LINKS OK`.

- [ ] **Step 3: Commit**

```bash
git add AGENTS.md
git commit -m "docs: add repo-root AGENTS.md agent execution guide"
```
