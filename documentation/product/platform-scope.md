# Platform Scope and Delivery Order

**Decision date: 2026-10-12. Status: adopted at the project owner's request.**

This document supersedes platform requirements in earlier specifications,
execution plans, agent guides, and build instructions. It changes the delivery
boundary; it does not certify the current implementation as ready to ship.

## Server: Ubuntu 24.04 and newer only

The server, its installer/operator CLI, storage integration, container
orchestration, backup, restore, upgrade, and monitoring target **Ubuntu 24.04+**.
Windows, macOS, WSL/Docker Desktop deployments, and other Linux distributions
are out of scope, including best-effort support. An Ubuntu guest is an Ubuntu
server; its hypervisor host does not become a supported server platform.

The initial reference architecture remains x86_64 with Podman 5.0.0 or newer.
Ubuntu 24.04 is the minimum supported release, not a promise that every future
Ubuntu/runtime combination has already been tested. Certify the baseline on
24.04 and repeat clean-install, storage, upgrade, and recovery checks on newer
Ubuntu releases before advertising those combinations as verified. Select and
document a reproducible Podman 5+ installation source during deployment work.

Linux kernel storage primitives and systemd are legitimate server assumptions.
Do not spend delivery effort on Windows/macOS server installers, runtime
adapters, cross-compilation gates, or non-Ubuntu packaging. Existing experimental
Go CLI platform branches and `docker-compose.windows.yml` are legacy source,
not supported entry points. Retire them as part of installer consolidation;
their continued presence must not create a release requirement.

Container base images need not be Ubuntu: the host support policy does not
require replacing an otherwise suitable isolated service image.

## Clients: browsers and Linux first

The first deliverable includes the hosted browser client and a Linux x86_64
desktop application. Browser coverage remains Chrome, Firefox, Edge, desktop
Safari, iOS Safari, and Android Chrome. A member may use a Windows or Mac
browser against the Ubuntu server. Server restrictions do not restrict client
operating systems or justify removing standards-based HTTP/WebSocket APIs.

The Linux application uses the shared frontend through the Tauri shell. Use an
Ubuntu reference desktop for the first native build and smoke journey; do not
claim every Linux distribution is verified. Other Linux packaging can follow
measured demand. Windows x86_64 and macOS universal **clients remain planned**,
but are deferred and do not block this first release. Native mobile packages
remain deferred; mobile browser support is in scope.

Preserve the client/server protocol and the portable frontend so future native
clients can be added without redesigning the Ubuntu server. Linux-first does
not waive device authentication, OS credential storage, platform-trusted HTTPS,
token-safe media, or browser-engine coverage. In particular, test the Linux
WebKitGTK media path before declaring the Linux desktop complete.

## Binding release requirements

- `beta-contract.json` names the Ubuntu minimum, initial Linux desktop, planned
  Windows/macOS desktops, and required external gates.
- `ci/phase-gates.json` retains Linux operator builds and the Linux desktop gate.
  Windows/macOS operator builds and desktop certification are excluded from the
  initial gate set. They must not remain indirect certification prerequisites.
- Browser certification, clean Ubuntu installation, encrypted off-node recovery,
  upgrade/rollback, capacity, exposure, controlled egress, and supply-chain checks
  remain required. Missing evidence remains `not_run`, never success.
- A green product-truth check proves consistency with this scope. It does not
  prove a working installation, native package, or certified release.

## Delivery sequence

1. **Complete the Ubuntu browser node.** Add the planned static `telos-client`
   service and Traefik route split; generate and mount signing secrets; complete
   guided upstream setup. Prove owner bootstrap, invitation, chat, reading,
   streaming, file upload/download, and commentary on a fresh Ubuntu 24.04 host.
2. **Complete the Linux application.** Repair the registration middleware path,
   origin/preflight handling, native auth state, OS credential adapter, and
   headerless media authorization. Prove install, sign-in, restart, playback,
   token rotation, logout, and remote revocation in the actual Linux package.
3. **Prove operations.** Resolve real image digests, harden the running stack,
   encrypt off-node backups, restore onto a separate Ubuntu host, and rehearse
   upgrades and rollback. This work may overlap the Linux client after the
   server/API contract stabilizes.
4. **Certify one immutable candidate.** Run the full browser matrix and all
   remaining required gates against the exact artifacts to be delivered. The
   existing recovery and capacity targets remain acceptance goals until measured.
5. **Expand native clients.** Add Windows and Mac packages, native credential
   adapters and platform evidence after the initial delivery is proven. Reuse
   the Ubuntu server; do not revive Windows or Mac server hosting.

The August standalone-beta program remains useful implementation detail. Apply
this scope to every phase: Phase 2 is Ubuntu deployment, Phase 3 is Linux native
delivery, Phase 4 proves operations, and Phase 5 certifies browser plus Linux.
Historical platform spikes and Windows-server installer plans are superseded.
