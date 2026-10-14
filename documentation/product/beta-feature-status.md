# Telos Beta Feature Status

> Normative product-truth matrix for the invite-only beta. Every visible
> product surface must match this document; incomplete functionality must not
> remain visible or advertised as shipping.

Telos is a community library for commentary on and storage of media. Its four
community-content modules are Chat, Stream, Library, and Files; Settings is the
separate account and administration surface.

## Current product surfaces

| Capability | Status |
|---|---|
| Chat: invite-only accounts, first-owner bootstrap, sessions, roles/permissions, channel chat, threads, reactions, pins, presence, mentions, and recipient-scoped user events | Shipping in the current application |
| Stream: Jellyfin media browsing and in-app HLS playback | Shipping in the current application |
| Library: EPUB/PDF catalog, readers, facets, stable catalog IDs, and member continuity | Shipping in the current application |
| Library audiobooks: Grimmory catalog records, playback information, signed stream tickets, track streams, and continuity | Shipping in the current application; backend and frontend tests cover catalog, playback, ticket, and continuity behavior |
| Files: scanned uploads, folders, browser, download, delete, and file commentary | Shipping in the current application |
| Commentary: book highlights plus comments on streamed items and files, private by default with sharing, replies, author edits/deletes, and moderator removal | Shipping in the current application |
| Settings: profile, security, appearance, credits, and administration of members, invites, roles, and channels | Shipping in the current application |

## Delivery boundary and support matrix

The beta target is a standalone client/server deployment: headless
`telos-core` provides the API and internal services, while a hosted browser is
served by `telos-client` on the same public origin. The static client container
and the Traefik route split are implemented; evidence from a clean Ubuntu 24.04
installation is still pending, so this is not yet a certified deployment.

| Delivery boundary | Status |
|---|---|
| Hosted-browser delivery | implemented-awaiting-evidence |
| Desktop distribution | Unsigned invite-only beta packages; blocked until Phase 3 evidence |
| HTTPS trust | Platform-trusted HTTPS only |
| Browser matrix | Chrome, Firefox, Edge, desktop Safari, iOS Safari, Android Chrome |
| Backup/recovery | Implemented and rehearsed; not shipping until Phase 5 candidate evidence |

The server targets **Ubuntu 24.04+ only**, with `ubuntu-24.04` as the minimum
reference release, `x86_64` as the initial architecture, and Podman `5.0.0` or
newer. Windows, macOS, WSL/Docker Desktop, and other Linux server distributions
are out of scope, including best-effort support. Newer Ubuntu combinations need
release-specific verification before being advertised as verified.

The initial desktop target is `linux-x86_64`. The `windows-x86_64` and
`macos-universal` clients remain planned for later; they do not block initial
delivery. Linux desktop packages are unsigned, invite-only beta packages and
remain blocked until Phase 3 evidence exists. Browser support is the hosted
service on current Chrome, Firefox, Edge, desktop Safari, iOS Safari, and
Android Chrome once the Phase 2 deployment evidence exists. Windows and Mac
browsers remain in scope. See [Platform Scope and Delivery Order](platform-scope.md).

Only platform-trusted HTTPS is supported: use public ACME certificates or an
operator-installed private CA trusted by the platform. Self-signed certificate
and certificate-pinning overrides are not a beta trust path.

Encrypted off-node backup and recovery are blocked until Phase 5 candidate evidence exists.
They require verified encrypted snapshots, 14 daily and 8 weekly retention,
and a separate-host restore before the RPO of at most 24 hours and RTO of at
most 4 hours can be claimed.

## Removed and unavailable surfaces

| Capability | Status |
|---|---|
| Voice rooms, LiveKit, and TURN | Removed. No supported endpoint, schema, container, or dependency remains. |
| Host-controlled synchronized Watch Parties | Removed. |
| Durable notification inbox or bell | Removed; mentions and replies use the user-event stream. |
| My List saved-media shelf | Removed. |
| Oracle, AI summaries, or AI API/runtime/control | Removed. |
| OIDC federation, web push, email notifications, administrative password replacement, high availability, and mobile-native packages | Not available in this beta boundary. |
