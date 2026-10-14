# Container hardening

Every Telos service runs under the same controls, stated per service in
`docker-compose.yml` and judged against one policy, `config/runtime-hardening.json`.
Two checks compare them, and neither trusts the other:

```text
# What Compose will run (rendered by the host's Podman Compose provider; nothing starts):
scripts/verify-runtime-hardening.sh --compose docker-compose.yml \
    --env-file <node.env or tests/fixtures/compose.env> --policy config/runtime-hardening.json

# What is actually running (needs root on the node):
sudo scripts/verify-runtime-hardening.sh --runtime --policy config/runtime-hardening.json
```

Exit 0 means every control holds; 1 names each finding; 3 means there is no Podman or
Compose provider to inspect (`not_run`, never a pass). A service the policy does not
name, or a policy service Compose lacks, is a finding. Running the live check is how
you learn that a container was started from an older Compose file: the file can say
the right thing while the container does not.

## What the policy requires

| Control | Static (rendered model) | Live (PID 1 and `podman inspect`) |
|---|---|---|
| Non-root identity | `user` stated, not root, equals the policy identity | effective UID/GID from `/proc/1/status` are not 0 and match |
| No added privilege | `cap_drop: [ALL]`, no `cap_add` beyond the policy, `no-new-privileges` | `CapEff` holds nothing outside the policy, `NoNewPrivs: 1` |
| Read-only root | `read_only: true` | `ReadonlyRootfs` |
| Writable paths | only the policy's volumes and bounded tmpfs | every writable mount is in the policy; no runtime socket or host root |
| No host sharing | no `privileged`, host PID/IPC/network, devices | the same, from the container's host configuration |
| Resource bounds | CPU, memory, PIDs, `nofile` present and within the table below | the limits Podman applied |
| Logs | `k8s-file` with `max-size` ≤ 10 MiB | driver and size as applied |
| Lifecycle | restart policy, `stop_grace_period`, health check | state, health, restart policy |
| Exposure | exact networks and published ports | attached networks, published ports, listening sockets |
| Identity of the image | digest equals `release/images.lock` | the digest Podman recorded; Telos-built images must be candidate artifacts |

Resource ceilings (CPUs / memory / PIDs / `nofile`): Traefik 0.5 / 256 MiB / 128 / 8192 ·
gateway 2 / 1 GiB / 256 / 16384 · client 0.25 / 128 MiB / 64 / 4096 · PostgreSQL
2 / 2 GiB / 256 / 16384 · Redis 0.5 / 512 MiB / 128 / 8192 · egress proxy
0.5 / 512 MiB / 128 / 8192 · ClamAV 2 / 2 GiB / 256 / 8192 · Jellyfin
4 / 4 GiB / 512 / 32768 · MariaDB 2 / 2 GiB / 256 / 16384 · Grimmory
2 / 2 GiB / 512 / 16384. A host smaller than 4 cores, 16 GiB of memory and 20 GiB of
free shared storage is refused by the installer rather than overcommitted.

## Exceptions

An exception names its control, the reason, its compensating controls and an expiry
date; the verifier reports an expired one and stops excusing it. Today there are two,
both `no-new-privileges`, for PostgreSQL and MariaDB. On Ubuntu 26.04 that option
leaves a container's processes under different AppArmor labels (crun's name-only
profile stacked under Podman's `containers-default`), and AppArmor then denies signals
between them: the entrypoint cannot stop its temporary initialisation server and the
server cannot signal its own postmaster. Both still run non-root with every capability
dropped, a read-only root, an internal-only network and the default AppArmor profile.
Remove the exceptions when the host no longer stacks that profile (see
`docs/implementation/design-alterations.md`, A16).

## Identities and ownership

* The gateway runs as `10001:10001`, the client as `101:101`, Traefik as `65532:65532`;
  PostgreSQL, Redis, MariaDB, ClamAV and the egress proxy as their images' own service
  users; Jellyfin and Grimmory as the node's `APP_UID:APP_GID`.
* Grimmory's image starts as root, creates a user and `su-exec`s to it. Telos runs the
  same JVM command directly as the app identity instead (A19), so the container never
  has root, a writable root or capabilities.
* Named volumes the image does not pre-create are given an owner so a non-root
  container can write them: `traefik_acme` (65532) and `grimmory_config` (`APP_UID`).
  Compose applies a volume's options only at creation, so `telos start` also fixes the
  owner of a volume made by an earlier release.
* Traefik listens on 8080/8443 inside its container; the host still serves 80/443. On a
  private-CA node the certificate directory, `server.crt` and `server.key` must be
  readable by UID 65532 (key mode 0640, group 65532, or 0600 root-only is refused);
  `telos start` and `telos doctor` name an unreadable path before it becomes a TLS
  error on a stack whose containers all look healthy.

## Known limits

* Podman Compose 1.6.0 honours `stop_grace_period` when it stops the stack (`telos
  stop`), not when it creates a container, so a container stopped by the host's
  shutdown uses Podman's 10 s. Run `telos stop` before a planned reboot. The gateway's
  staged shutdown needs up to 55 s.
* Podman's `k8s-file` log driver truncates at `max-size`; it has no file count, so the
  bound is the size.
* The live check reads `/proc/<pid>` from the host. It must run as root on the node.
