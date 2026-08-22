# Container Hardening and Operational Runtime Security Policy

This document describes the runtime container hardening, resource allocation, and network isolation policies enforced across the Telos platform stack.

## Runtime Security Guarantees

1. **Non-Root Identities**: Every service runs as an explicit non-root user (e.g. `telos-core` runs as UID 10001).
2. **Capability Dropping**: All containers drop all Linux capabilities (`cap_drop: [ALL]`) and set `no-new-privileges: true`.
3. **Read-Only Root Filesystems**: Service root filesystems are read-only except for explicitly mounted volumes and bounded `tmpfs` mounts.
4. **Resource Limits**: Every container defines explicit CPU, memory, PIDs, and `nofile` ulimits to prevent resource exhaustion.
5. **Network Exposure**: Only HTTP/HTTPS via Traefik are exposed on host interfaces. Internal databases, Redis, ClamAV, Jellyfin, and Grimmory operate strictly on isolated container networks.
