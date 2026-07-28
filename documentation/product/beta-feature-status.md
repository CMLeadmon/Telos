# Telos Beta Feature Status

> Normative product-truth matrix for the invite-only beta. Every visible
> product surface must match this document; incomplete functionality must not
> remain visible or advertised.

Approved by the beta-readiness remediation design (2026-07-18) and refocused by
the less-is-more redesign (2026-07-27). The beta ships a community library for
commentary on and storage of media, on one Internet-exposed, operator-controlled
server for a trusted, invite-only community. Three modules — Library, Stream,
Chat — plus Settings.

## In the beta

| Capability | Status |
|---|---|
| Invite-only accounts, first-owner bootstrap, sessions, roles/permissions (Owner / Administrator / Moderator / Member) | Shipping |
| Channel chat with one-level threads, reactions, pins, presence, mentions | Shipping |
| Recipient-scoped user-event stream (WebSocket catch-up + live delivery) for mentions and replies | Shipping |
| Jellyfin streaming with an in-app HLS player (playback rate, Picture-in-Picture) | Shipping |
| E-book catalog (Grimmory) in a Library with facet filtering; in-app EPUB and PDF readers with durable progress | Shipping |
| Commentary on any media target — book highlights plus comments on streamed items and files — private by default, explicitly community-shareable, replies, author edit/delete, moderator removal | Shipping |
| Files browser folded into Library (Books \| Files segment): upload (scanned by ClamAV), folders, download, delete | Shipping |
| Channel CRUD and role overrides via `manage_channels` | Shipping |
| Settings: profile, security, appearance (Synthwave/Ink themes), Credits; admin members/invites/roles/channels | Shipping |
| Encrypted off-node backups (14 daily / 8 weekly), documented restore | Shipping |

## Outside beta scope

| Capability | Status |
|---|---|
| Voice rooms, LiveKit, TURN | Removed in the less-is-more redesign. No endpoint, schema, container, or dependency remains. |
| Host-controlled synchronized Watch Parties | Removed. No endpoint or schema remains. |
| Durable notification inbox / bell | Removed. Mentions and replies are delivered over the user-event stream, not a stored inbox. |
| My List (saved-media shelf) | Removed. |
| Oracle, AI summaries, or any AI feature, API, runtime, or control | Removed. No beta endpoint or schema is reserved for a future AI module. |
| OIDC federation / per-service user provisioning | Not available; not presented as an integration. |
| Web Push or email notification delivery | Not available; awareness is in-app only. |
| Administrative password replacement | Not available; operators may disable an account, and members change a known current password. |
| High availability / multi-node deployment | Not available; scheduled maintenance windows are the availability model. |

## Network and privacy posture

Members reach the node over direct HTTPS via Traefik, or over a private
network the operator runs. Outbound traffic is limited to operator-enabled
integrations: ACME issuance, ClamAV signature updates, metadata providers,
and encrypted operator-selected off-node backup targets. Telos does not claim
that nothing ever leaves the node.

ClamAV, Jellyfin, and Grimmory have **no direct Internet path**: they sit on an
internal network and reach the outside world only through a deny-by-default
egress proxy (`telos-egress-proxy`) that permits only a small reviewed list of
provider hostnames on TCP 443 (see
[controlled-egress](../operations/controlled-egress.md)). Enrichment fails
closed — a metadata lookup that cannot reach an approved provider degrades that
feature rather than opening an uncontrolled path.

## Support targets

100 registered members, 25 concurrent authenticated users, 10 chat/commentary
events per second; current desktop Chrome/Firefox/Safari/Edge and current iOS
Safari / Android Chrome; RPO ≤ 24 h and RTO ≤ 4 h.
