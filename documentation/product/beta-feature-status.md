# Telos Beta Feature Status

> Normative product-truth matrix for the invite-only beta. Every visible
> product surface must match this document; incomplete functionality must not
> remain visible or advertised.

Approved by the beta-readiness remediation design (2026-07-18). The beta ships
the complete advertised non-AI product on one Internet-exposed,
operator-controlled server for a trusted, invite-only community.

## In the beta

| Capability | Status |
|---|---|
| Invite-only accounts, first-owner bootstrap, sessions, roles/permissions | Shipping |
| Channel chat with one-level threads, reactions, pins, presence | Shipping (threads land in Phase 5 of the remediation program) |
| Durable in-app notification inbox (mentions, thread replies, shared-annotation replies, Watch Party invitations, security/admin events) | Shipping (Phase 5) |
| Voice rooms (LiveKit, microphone audio only) | Shipping |
| Jellyfin streaming with HLS player and durable per-user My List | Shipping (My List lands in Phase 5) |
| Host-controlled synchronized Watch Parties linked to existing chat/voice | Shipping (Phase 5) |
| E-book catalog (Grimmory), in-app EPUB reader with durable progress | Shipping |
| In-app PDF reader with durable progress | Shipping (Phase 5) |
| EPUB/PDF annotations — private by default, explicitly community-shareable, replies, author edit/delete, moderator removal | Shipping (Phase 5) |
| File module: upload (scanned by ClamAV), folders, download, delete | Shipping |
| Channel CRUD and role overrides via `manage_channels` | Shipping (Phase 5) |
| Settings: profile, security, appearance (Synthwave/Ink themes), voice & audio, Credits; admin members/invites/roles | Shipping |
| Encrypted off-node backups (14 daily / 8 weekly), documented restore | Shipping (Phase 3/7 of the remediation program) |

## Outside beta scope

| Capability | Status |
|---|---|
| Oracle, AI summaries, or any AI feature, API, runtime, or control | Removed. No beta endpoint or schema is reserved for a future AI module. |
| OIDC federation / per-service user provisioning | Not available; not presented as an integration. |
| Web Push or email notification delivery | Not available; notifications are in-app only. |
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

100 registered members, 25 concurrent authenticated users, 10 chat/annotation
events per second, 5 concurrent Watch Parties, 25 voice participants; current
desktop Chrome/Firefox/Safari/Edge and current iOS Safari / Android Chrome;
RPO ≤ 24 h and RTO ≤ 4 h.
