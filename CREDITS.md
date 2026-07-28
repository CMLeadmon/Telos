# Credits

Telos (the gateway and client in this repository) is licensed under the
[Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0) — see
[LICENSE](./LICENSE) and [NOTICE](./NOTICE).

Telos exists because of the projects below. Every isolated service runs as a
separate process behind a container boundary and is reached only over its
documented API; no copyleft source is linked or compiled into Telos.

This file is the canonical Credits matrix. The in-app Settings → Credits
surface renders a reviewed static projection of the same entries
(`frontend/src/components/settings/CreditsSection.tsx`); the two must stay in
agreement.

## Isolated services

| Name | Role in Telos | Source | License |
|---|---|---|---|
| Jellyfin | Media transcoding, HLS generation, and streaming (isolated service) | [https://github.com/jellyfin/jellyfin](https://github.com/jellyfin/jellyfin) | [GPL-2.0](https://github.com/jellyfin/jellyfin/blob/master/LICENSE) |
| Grimmory | E-book catalog, metadata, and library management (isolated service) | [https://github.com/grimmory-tools/grimmory](https://github.com/grimmory-tools/grimmory) | [AGPL-3.0](https://www.gnu.org/licenses/agpl-3.0.html) |
| ClamAV | Malware scanning for uploads (isolated service) | [https://github.com/Cisco-Talos/clamav](https://github.com/Cisco-Talos/clamav) | [GPL-2.0](https://github.com/Cisco-Talos/clamav/blob/main/COPYING.txt) |

## Infrastructure

| Name | Role in Telos | Source | License |
|---|---|---|---|
| Traefik | Edge router, TLS termination, single-origin ingress | [https://github.com/traefik/traefik](https://github.com/traefik/traefik) | [MIT](https://github.com/traefik/traefik/blob/master/LICENSE.md) |
| PostgreSQL | Authoritative durable data store | [https://github.com/postgres/postgres](https://github.com/postgres/postgres) | [PostgreSQL License](https://www.postgresql.org/about/licence/) |
| Redis | Rebuildable cache, presence, pub/sub, rate limiting | [https://github.com/redis/redis](https://github.com/redis/redis) | [RSALv2/SSPLv1](https://github.com/redis/redis/blob/unstable/LICENSE.txt) |
| MariaDB | Grimmory's database (isolated with Grimmory) | [https://github.com/MariaDB/server](https://github.com/MariaDB/server) | [GPL-2.0](https://github.com/MariaDB/server/blob/main/COPYING) |
