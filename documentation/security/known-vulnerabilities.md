# Known vulnerabilities in the components Telos bundles

> **Scanned 2026-10-14** · applies to the **standalone beta line** (the `standalone-beta-impl` work, not yet released) ·
> scanner Trivy 0.75.0, vulnerability database retrieved 2026-10-14 16:47 UTC · severities High and Critical only.
> This is a point-in-time snapshot. New flaws are published every day; re-run the scan (below) rather than trusting an old copy.

## In one paragraph

Telos is its own server and web client **plus** well-known open-source programs it runs alongside: Jellyfin (video and music),
Grimmory (e-books), PostgreSQL and MariaDB (databases), Traefik (the HTTPS front door), and a few supporting services. When the
exact versions Telos currently ships are checked against public databases of known security flaws, **Telos's own server and web
client have no High or Critical findings**, and **all 15 Critical findings are in four bundled third-party components** (Grimmory,
Jellyfin, MariaDB, PostgreSQL). For Grimmory and Jellyfin the project already ships the newest published version, so the fix has to
come from those projects' next releases. Until then, Telos cannot be certified as a beta under its own release rules (see
*What the project is doing*).

## At a glance

| Component | What it does for Telos | Version pinned | Critical | High |
|---|---|---|---|---|
| telos-core | Telos's own server | built by Telos | 0 | 0 |
| telos-client | Telos's own web client | built by Telos | 0 | 0 |
| Traefik | the public front door (HTTPS) | traefik:v3.7.14 | 0 | 4 |
| PostgreSQL | Telos's main database | postgres:16.15-alpine | 1 | 24 |
| Redis | cache and live-chat relay | redis:7.4.11-alpine | 0 | 2 |
| MariaDB | Grimmory's database | mariadb:10.11.19 | 1 | 25 |
| Grimmory | e-book library service | grimmory:v3.5.0 | 7 | 71 |
| Jellyfin | video and music streaming service | jellyfin:10.11.11 | 6 | 143 |
| Prometheus | internal metrics store | prometheus:v3.15.0 | 0 | 8 |
| Egress proxy (Squid) | limits what the media services may reach on the Internet | squid:6.6-24.04_edge | 0 | 4 |
| ClamAV | virus scanner for uploads | clamav:1.5.4 | 0 | 0 |
| **Total** | | | **15** | **281** |

*Critical* and *High* are the scanner's severity ratings of the published advisories. They describe the flaw in general, not how
easy it is to exploit **in Telos**; see *How reachable is this?*.

## What these numbers do and do not mean

- **A finding means "this version of this software has a published advisory", not "Telos has been attacked".** We know of no
  exploitation of any of these in Telos. We have **not** carried out an exploitability analysis of each advisory against Telos.
- **Many findings come from a few small helper programs.** Of the 296 findings, 61 are in `kepubify` (an e-book conversion
  helper inside Grimmory, built with an old Go compiler), and 50 are in `gosu` (a tiny user-switching helper inside the PostgreSQL and
  MariaDB images; Telos starts those databases directly as their unprivileged user instead of through that helper). Counting
  advisories, not programs, overstates how many separate things need attention.
- **A fix usually exists somewhere, but not yet in an image we can pull.** For almost every advisory below a corrected version of the
  component has been published (listed in the tables). The container images that package them have not all been rebuilt yet. Telos
  moves its pins when they are.
- **Telos's own code is checked too.** The server and client are scanned like everything else; the Go libraries they use were raised
  to the fixed versions after the first scan, and the scan for accidentally committed secrets found only deliberate test values (which are allow-listed by exact value).

## The 15 Critical findings

These are 13 distinct advisories: CVE-2025-68121 (a Go standard-library flaw) appears in three components.

| Component | Advisory | Affected | What it is | Fixed in |
|---|---|---|---|---|
| Grimmory | CVE-2023-24538 | stdlib v1.17.8 | golang: html/template: backticks not treated as string delimiters | 1.19.8 / 1.20.3 |
| Grimmory | CVE-2023-24540 | stdlib v1.17.8 | golang: html/template: improper handling of JavaScript whitespace | 1.19.9 / 1.20.4 |
| Grimmory | CVE-2024-24790 | stdlib v1.17.8 | golang: net/netip: Unexpected behavior from Is methods for IPv4-mapped IPv6 addresses | 1.21.11 / 1.22.4 |
| Grimmory | CVE-2025-68121 | stdlib v1.17.8 | crypto/tls: crypto/tls: Incorrect certificate validation during TLS session resumption | 1.24.13 / 1.25.7 / 1.26.0-rc.3 |
| Grimmory | CVE-2026-65182 | org.apache.tomcat.embed:tomcat-embed-core 11.0.24 | tomcat: Apache Tomcat: Security constraint bypass due to improper access control | 11.0.25 / 10.1.58 / 9.0.121 |
| Grimmory | CVE-2026-65905 | org.apache.tomcat.embed:tomcat-embed-core 11.0.24 | tomcat: Apache Tomcat: Authentication bypass via limited replay attack in DIGEST authenticator | 11.0.25 / 10.1.58 / 9.0.121 |
| Grimmory | CVE-2026-68525 | org.apache.tomcat.embed:tomcat-embed-core 11.0.24 | tomcat: Apache Tomcat: Unauthorized resource access via FORM authentication bypass | 11.0.25 / 10.1.58 / 9.0.121 |
| Jellyfin | CVE-2026-13221 | perl-base 5.40.1-6 | perl: Perl: Incorrect regular expression processing via large regular expressions | 5.40.1-6+deb13u1 |
| Jellyfin | CVE-2026-42496 | perl-base 5.40.1-6 | perl-archive-tar: perl-archive-tar: Path traversal via crafted symlinks allows arbitrary file access | 5.40.1-6+deb13u1 |
| Jellyfin | CVE-2026-6653 | libxml2 2.12.7+dfsg+really2.9.14-2.1+deb13u2 | libxml2: mingw-libxml2: libxml2: Denial of Service via crafted XML input due to use-after-free | no fix published yet |
| Jellyfin | CVE-2026-70188 | Microsoft.AspNetCore.App.Runtime.linux-x64 9.0.16 | Microsoft Security Advisory CVE-2026-70188 - ASP.NET Core Security Feature Bypass Vulnerability | 11.0.0-rc.2 / 8.0.32 / 10.0.13 / 9.0.21 |
| Jellyfin | CVE-2026-8376 | perl-base 5.40.1-6 | perl: Perl: Heap buffer overflow when compiling regular expressions on 32-bit builds | 5.40.1-6+deb13u1 |
| Jellyfin | CVE-2026-88092 | Microsoft.AspNetCore.App.Runtime.linux-x64 9.0.16 | Microsoft Security Advisory CVE-2026-88092 - ASP.NET Core Security Feature Bypass Vulnerability | 8.0.32 / 10.0.13 / 11.0.0-rc.2 / 9.0.21 |
| MariaDB | CVE-2025-68121 | stdlib v1.24.6 | crypto/tls: crypto/tls: Incorrect certificate validation during TLS session resumption | 1.24.13 / 1.25.7 / 1.26.0-rc.3 |
| PostgreSQL | CVE-2025-68121 | stdlib v1.24.6 | crypto/tls: crypto/tls: Incorrect certificate validation during TLS session resumption | 1.24.13 / 1.25.7 / 1.26.0-rc.3 |

Plain-language reading:

- **Grimmory — Tomcat (3):** the web server inside Grimmory has authentication and access-control bypass advisories. They matter if
  an attacker can reach Grimmory's web interface. Telos does not publish Grimmory to the network; see below.
- **Grimmory — `kepubify` (4):** an e-book conversion helper built with Go 1.17; the advisories concern its use of Go's HTML and
  network libraries on untrusted input.
- **Jellyfin (6):** the Perl interpreter and XML library of its base operating system, and two "security feature bypass" advisories in the
  .NET web runtime it is built on.
- **MariaDB and PostgreSQL (1 each):** the `gosu` helper (certificate validation when resuming a TLS session). Not used by Telos's
  way of starting these databases.

## High-severity findings by component

The largest groups per component; every finding is in the data file.

**Traefik** — 4 High findings in 2 packages

| Package | Installed | High findings | A fixed version exists |
|---|---|---|---|
| stdlib | v1.26.8 | 3 | yes (e.g. 1.26.9) |
| golang.org/x/net | v0.59.0 | 1 | yes (e.g. 0.60.0) |

**PostgreSQL** — 24 High findings in 1 packages

| Package | Installed | High findings | A fixed version exists |
|---|---|---|---|
| stdlib | v1.24.6 | 24 | yes (e.g. 1.24.11) |

**Redis** — 2 High findings in 2 packages

| Package | Installed | High findings | A fixed version exists |
|---|---|---|---|
| libcrypto3 | 3.3.7-r1 | 1 | yes (e.g. 3.3.7-r2) |
| libssl3 | 3.3.7-r1 | 1 | yes (e.g. 3.3.7-r2) |

**MariaDB** — 25 High findings in 2 packages

| Package | Installed | High findings | A fixed version exists |
|---|---|---|---|
| stdlib | v1.24.6 | 24 | yes (e.g. 1.24.11) |
| libssl3 | 3.0.2-0ubuntu1.29 | 1 | yes (e.g. 3.0.2-0ubuntu1.30) |

**Grimmory** — 71 High findings in 7 packages

| Package | Installed | High findings | A fixed version exists |
|---|---|---|---|
| stdlib | v1.17.8 | 57 | yes (e.g. 1.17.11) |
| com.fasterxml.jackson.core:jackson-databind | 2.21.5 | 3 | yes (e.g. 2.18.10) |
| golang.org/x/text | v0.3.6 | 3 | yes (e.g. 0.3.7) |
| tools.jackson.core:jackson-databind | 3.1.5 | 3 | yes (e.g. 3.1.7) |
| com.fasterxml.jackson.core:jackson-core | 2.21.5 | 2 | yes (e.g. 2.18.11) |
| tools.jackson.core:jackson-core | 3.1.5 | 2 | yes (e.g. 3.1.7) |
| libexpat | 2.8.4-r0 | 1 | yes (e.g. 2.8.5-r0) |

**Jellyfin** — 143 High findings in 34 packages

| Package | Installed | High findings | A fixed version exists |
|---|---|---|---|
| Microsoft.NETCore.App.Runtime.linux-x64 | 9.0.16 | 11 | yes (e.g. 10.0.10) |
| libexpat1 | 2.7.1-2 | 8 | yes (e.g. 2.8.2-1~deb13u1) |
| bsdutils | 1:2.41-5 | 7 | yes (e.g. 2.41.5-0+deb13u1) |
| libblkid1 | 2.41-5 | 7 | yes (e.g. 2.41.5-0+deb13u1) |
| liblastlog2-2 | 2.41-5 | 7 | yes (e.g. 2.41.5-0+deb13u1) |
| libmount1 | 2.41-5 | 7 | yes (e.g. 2.41.5-0+deb13u1) |
| libsmartcols1 | 2.41-5 | 7 | yes (e.g. 2.41.5-0+deb13u1) |
| libuuid1 | 2.41-5 | 7 | yes (e.g. 2.41.5-0+deb13u1) |
| …26 more packages | | 82 | see the data file |

**Prometheus** — 8 High findings in 2 packages

| Package | Installed | High findings | A fixed version exists |
|---|---|---|---|
| stdlib | v1.27.1 | 6 | yes (e.g. 1.26.9) |
| golang.org/x/net | v0.58.0 | 2 | yes (e.g. 0.60.0) |

**Egress proxy (Squid)** — 4 High findings in 2 packages

| Package | Installed | High findings | A fixed version exists |
|---|---|---|---|
| libssl3t64 | 3.0.13-0ubuntu3.9 | 2 | yes (e.g. 3.0.13-0ubuntu3.11) |
| openssl | 3.0.13-0ubuntu3.9 | 2 | yes (e.g. 3.0.13-0ubuntu3.11) |

Of Jellyfin's 143 High findings, 64 have no fixed version published yet.

## How reachable is this?

What we verified on the reference test machines (a rehearsal, not a certification):

- **Only ports 80 and 443 are open to the network**, and only Traefik listens on them. A scan of all 65,535 TCP ports found nothing else.
- Traefik routes only two things: the Telos API (`/api/v1`) and the static web client. **Jellyfin, Grimmory, the databases, Redis,
  ClamAV and Prometheus are never routed directly**; they sit on internal container networks with no route to the Internet. The media
  services reach the Internet only through a deny-by-default proxy.
- The Telos API reaches Jellyfin and Grimmory on behalf of signed-in members, over those internal networks.

What this does **not** establish: whether any particular advisory above is reachable through the requests the Telos API forwards. A
flaw in how Jellyfin or Grimmory parse a file or a request could be reachable by a signed-in member even though their web interfaces
are not exposed. Treat the four affected components as the part of Telos that needs the most care until they are updated.

## What you can do now

1. **Keep the node's firewall to 80 and 443**, and never publish Jellyfin's or Grimmory's own ports. (Telos does not; the guided
   setup publishes them on `127.0.0.1` only while you run it.)
2. **Limit who has accounts.** Telos is invite-only; give the Owner and administrator roles to as few people as possible. The
   open advisories mostly need an attacker who can already send requests to the media services.
3. **Only add media and e-books you trust to the libraries.** Several advisories concern parsing crafted files.
4. **Consider a private network or VPN** in front of the node while these are open, particularly if the node holds sensitive content.
5. **Update when Telos releases new images.** Each release states which component versions it ships. Review `telos status` after
   upgrades and the notes in `docs/implementation/standalone-beta-progress.md`.

## What the project is doing

- **Already done:** after the first scan the project moved every component it can move (Alpine, nginx, Traefik, PostgreSQL, the Go build
  base) and raised five Go libraries. That removed every Critical finding in Telos's own images and in Traefik and took the total from
  465 findings (23 Critical) to 296 (15 Critical) (design alteration A42 in
  `docs/implementation/design-alterations.md`). The refreshed stack was proven by upgrading real data through the upgrade drill and a
  30-minute load test.
- **Release rule:** a candidate with any Critical finding, or any High finding without a named, signed and expiring exception, cannot pass
  the `candidate-supply-chain` gate, so cannot be certified. Nothing has been accepted: `ci/risk-acceptances.json` is empty.
- **Open decisions** for the maintainers: replace one of the four components, wait for upstream releases, or change the rule (for
  example to allow a written "not applicable" record for the unused `gosu` helper). None has been taken.

## Security-relevant exceptions inside Telos itself

These are deliberate and documented (`docs/implementation/design-alterations.md`):

| Where | What | Why | Compensating controls |
|---|---|---|---|
| PostgreSQL, MariaDB, Jellyfin containers | Do **not** use the `no-new-privileges` hardening option (A16, A33) | On Ubuntu 26.04 it makes AppArmor block signals those programs need; Jellyfin crashed under load, the databases would not start | Non-root user, all capabilities dropped, read-only root, internal-only network, AppArmor profile still enforced |
| `telos-client` container | Temporary-file areas are world-writable (mode 1777) (A4) | Podman 5.7 rejects the ownership option | The container is single-purpose, non-root and read-only |
| Backup service | Runs without `NoNewPrivileges`/`PrivateTmp` (A22) | Same Ubuntu 26.04 AppArmor interaction | Root-only staging, removed on every exit |
| HTTPS | The test setup uses a private certificate authority (A14) | No public DNS name in the test environment | Production uses public ACME or an operator-installed CA; Telos never disables certificate checks |
| Desktop app | Linux packages are unsigned beta builds | No signing identity yet | Invite-only distribution |

Independent security testing: **none has been done.** The checks described here are automated scans and the project's own rehearsal
tests, run on virtual machines.

## Verify it yourself

Any pinned image can be scanned directly (replace the reference with one from the table below):

```bash
trivy image --severity HIGH,CRITICAL docker.io/jellyfin/jellyfin:10.11.11@sha256:aefb67e6a7ff1debdd154a78a7bbb780fd0c873d8639210a7f6a2016ad2b35db
```

The images Telos pins (`release/images.lock`):

| Component | Reference |
|---|---|
| Grimmory | `docker.io/grimmory/grimmory:v3.5.0@sha256:bf6fe21c6e247597f7869664a440b5a34242e2d2940575cae39c17bfaa66dc0b` |
| Jellyfin | `docker.io/jellyfin/jellyfin:10.11.11@sha256:aefb67e6a7ff1debdd154a78a7bbb780fd0c873d8639210a7f6a2016ad2b35db` |
| MariaDB | `docker.io/library/mariadb:10.11.19@sha256:7db29378d4fdab73f8123bbc2b48905c90d1a4b00cf848b028f1e81e623257f2` |
| PostgreSQL | `docker.io/library/postgres:16.15-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea` |
| Traefik | `docker.io/library/traefik:v3.7.14@sha256:575fa15b135078fe5e50aa847987d96dbddd7b093c172429618404df73f3fa7c` |
| Prometheus | `docker.io/prom/prometheus:v3.15.0@sha256:efd719c99d83b060d9daefdcf00360461adf279f45ef5391f8d111892118753e` |
| Redis | `docker.io/library/redis:7.4.11-alpine@sha256:858f009f9709ce576febc734aa78b8f6d624b82571f9ddb6bda4377c833b3499` |
| ClamAV | `docker.io/clamav/clamav:1.5.4@sha256:638d90201ee7ee6a82d249a2fc23b132e578673b5f1f2766852b127676ca1ef6` |
| squid | `docker.io/ubuntu/squid:6.6-24.04_edge@sha256:8a3baed477e2c282ab8aa5edad442f69873246964f225c5c2ae8364b6610963c` |

The candidate-level scan the release gate uses is `scripts/scan-candidate.sh`; its method is described in
`documentation/operations/candidate-supply-chain.md`. The full list of findings behind this page is
[`known-vulnerabilities-2026-10-14.json`](known-vulnerabilities-2026-10-14.json).

To report a vulnerability, see [SECURITY.md](../../SECURITY.md).
