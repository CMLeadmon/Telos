# Container Hardening and Operational Runtime Security Policy

This document records the intended runtime hardening posture. It is not
evidence that any control is currently enforced.

## Verification status

`scripts/verify-runtime-hardening.sh` returns `not_run` (exit 3) until Phase 4
provides effective-runtime inspection and evidence. Do not claim the controls
below are enforced before that evidence exists.

## Target runtime controls (unverified)

1. **Non-root identities**: verify each service runs as its declared non-root
   user (for example, `telos-core` as UID 10001).
2. **Capability dropping**: verify containers drop Linux capabilities and set
   `no-new-privileges: true` where configured.
3. **Read-only root filesystems**: verify service roots are read-only except
   for declared volumes and bounded `tmpfs` mounts.
4. **Resource limits**: verify declared CPU, memory, PID, and `nofile` limits.
5. **Network exposure**: verify only Traefik's HTTP/HTTPS entrypoints are
   published and other services remain on their intended container networks.
