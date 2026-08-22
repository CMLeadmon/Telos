# Telos Network Exposure Audit Guide

This document details permitted open host ports and internal service boundaries across the Telos platform stack.

## Allowed Public Ports
- TCP 80 / 443 (HTTP/HTTPS via Traefik)

No UDP media ports or additional service ports are published on host
interfaces. Jellyfin, Grimmory, PostgreSQL, Redis, ClamAV, and the controlled
egress proxy remain on their intended container networks.
