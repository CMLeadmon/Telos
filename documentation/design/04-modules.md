# Module Specifications

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

This file specifies the per-module contents of the two module-owned panes defined in [`03-application-shell.md`](./03-application-shell.md): the **Contextual Sidebar** and the **Central Arena**. Throughout this document, "Sidebar" is shorthand for the Contextual Sidebar and "Arena" is shorthand for the Central Arena — both are shell-level regions whose containers are defined in `03-application-shell.md` §4 and §5; this document defines only what each module renders inside them. Color, type, iconography, radii, and semantic-state values are defined in [`02-design-tokens.md`](./02-design-tokens.md) and are referenced here by name, not restated. Slogans and the Sovereign Ouroboros mark are defined in [`01-brand-identity.md`](./01-brand-identity.md) and are referenced here by name, not restated.

Five modules exist: Chat & Voice, Media Streaming, Books (Grimmory), Files, and the Sovereignty Manifesto.

## 1. Chat & Voice

### Sidebar

The Sidebar lists channels in two groups: text channels and voice channels.

- Text channels are prefixed with the `Hash` icon.
- Voice channels show a Join/Leave affordance and, when occupied, a nested list of active users beneath the channel row. A user actively transmitting in a voice channel is rendered per the live/active state in [`02-design-tokens.md` §6](./02-design-tokens.md#6-semantic-states-without-semantic-color): Blue Wash plus a pulsing blue dot.

### Arena

- **Header:** contains a `SUMMARIZE MISSED` button — `Bot` icon, Sovereign Blue emphasis — that requests an AI-generated summary of messages the user has not yet read.
- **Brand banner:** displays the philosophical/technical slogan from [`01-brand-identity.md`](./01-brand-identity.md#slogans), verbatim and lowercase: `be on the net, but not of the net`.
- **Message anatomy:** a fully rounded monogram avatar, the username, a role badge (`Host` or `Admin`, rendered as a Blue Wash badge), and a timestamp set in `slate-500` (permitted here because the timestamp sits immediately adjacent to the higher-contrast username, per the adjacent-context allowance in [`02-design-tokens.md` §1](./02-design-tokens.md#text)).
- **Input:** a single input control integrated into the bottom edge of the Arena; no separate floating composer.

## 2. Media Streaming

### Sidebar

- Library entries: Movies, Documentaries, Audiobooks.
- An "Ingest Stream" tool accepting a direct-stream URL.

### Arena

- A central HTML5 player exposing play/pause, a scrub timeline, and elapsed/total duration. Bitrate badges for direct-stream sessions are rendered in the monospace stack per [`02-design-tokens.md` §3](./02-design-tokens.md#3-typography).
- Below the player, a grid of active feeds. Each feed thumbnail has a Sovereign Blue progress bar (progress state per [`02-design-tokens.md` §6](./02-design-tokens.md#6-semantic-states-without-semantic-color)) attached flush to the bottom edge of the thumbnail.

## 3. Books (Grimmory)

### Sidebar

- Home group: Dashboard, All Books, Authors.
- Libraries group.
- Shelves group, each shelf row right-aligned with a numeric count badge.

### Arena — Library view

- Two horizontal rows, "Continue Listening" and "Continue Reading", each headed by a label with a thick Sovereign Blue underline.
- Book cards show a format badge (`EPUB` or `PDF`) in the top-left corner, a centered title and author on a neutral dark cover, and a `Play` icon overlay that appears on hover.

### Arena — Reader view

- Reading content is set in the serif reading stack, the sole sanctioned exception to the two-stack typography rule defined in [`02-design-tokens.md` §3](./02-design-tokens.md#3-typography).
- Font-size controls are available to the reader.
- The footer contains an `ANALYZE CHAPTER` button that requests an AI-generated summary of the chapter currently in view.

## 4. Files

### Sidebar

- Storage volume metrics: each volume shows its mount path in the monospace stack (for example, `/mnt/ssd_nvme`) alongside a Sovereign Blue usage bar (progress state per [`02-design-tokens.md` §6](./02-design-tokens.md#6-semantic-states-without-semantic-color)).
- A quick-navigation list beneath the volume metrics.

### Arena

- A breadcrumb trail followed by `New Directory` and `Upload` action buttons.
- A grid-list of directory contents with columns Name, Size, Modified.
- Selecting one or more rows opens a bottom action bar with three actions: Rename, Download, and Delete.
  - Delete follows the irreversible-destructive pattern defined in [`02-design-tokens.md` §6](./02-design-tokens.md#6-semantic-states-without-semantic-color): a stark-white filled button plus an explicit confirmation step that names the file being deleted.

## 5. Sovereignty Manifesto

The Sovereignty Manifesto is opened from the `SOVEREIGNTY MANIFESTO` toggle button in the Global Header (see [`03-application-shell.md` §2](./03-application-shell.md#2-global-header)). It is prose content, not a persistent pane, and contains two named sections:

- **The Digital Enclosure Problem** — explains the platform-lock-in and data-enclosure conditions Telos exists to escape.
- **The Telos Design Creed** — explains the design principles (single accent, semantic-state-without-color, self-hosted-first) that follow from that problem.

The manifesto includes a terminal-styled block, set in the monospace stack, showing an illustrative `docker-compose.yml` excerpt. The excerpt uses local loopback port bindings and `${VAR}`-style references for every credential or secret; no literal secret values appear:

```yaml
services:
  traefik:
    ports:
      - "127.0.0.1:443:443"   # loopback-only bind: reachable via your tunnel, invisible to the open net

  telos-core:
    build:
      context: ./backend       # built from source — Telos ships no published image
    environment:
      - DATABASE_URL=postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}
```

For the authoritative, complete deployment configuration, see [`../architecture/02-deployment.md`](../architecture/02-deployment.md).
