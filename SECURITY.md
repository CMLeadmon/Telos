# Security Policy

## Status

Telos has no stable release. The **standalone beta** (the `standalone-beta-impl` line) is under test and **has not been certified**;
it should not be exposed to untrusted users or hold data you cannot afford to lose. Only that line has been assessed the way this
document describes. The `main` branch predates that work and has not been scanned or hardened in the same way.

## Reporting a vulnerability

**Please do not open a public issue for a flaw in Telos itself.**

Use GitHub's private reporting: on the repository page choose **Security → Report a vulnerability**. Include:

- what you found and which part of Telos it affects (gateway API, web client, command-line tool, container configuration, backup or restore);
- the version or commit, and how you installed it;
- steps to reproduce, or a proof of concept if you have one;
- what an attacker would gain.

We will acknowledge your report, tell you what we decide, and credit you in the fix if you wish.

Flaws in the open-source programs Telos bundles (Jellyfin, Grimmory, PostgreSQL, MariaDB, Traefik and others) should also be
reported to those projects. Tell us as well if Telos's configuration makes one of them easier to reach or exploit.

## Known vulnerabilities in bundled components

The components Telos ships are scanned against public advisory databases. At the last scan (2026-10-14) Telos's own server and web
client had no High or Critical findings, and the **15 Critical findings are all in four bundled third-party components**
(Grimmory, Jellyfin, MariaDB, PostgreSQL). The full list, what it means, how reachable it is, and what you can do now are in
[documentation/security/known-vulnerabilities.md](documentation/security/known-vulnerabilities.md).

## How Telos limits exposure

- Only ports **80 and 443** are published, by Traefik alone; everything else stays on internal container networks.
- Accounts are invite-only. Passwords are hashed; sessions and device credentials can be revoked, and a restore revokes all of them.
- Containers run as non-root users with capabilities dropped and read-only root filesystems, with documented exceptions
  (see *Security-relevant exceptions* in the document above).
- The media services reach the Internet only through a deny-by-default proxy.
- Backups are encrypted and kept off the node.

These properties were checked on reference test machines. Independent security testing has **not** been done.

## Supported versions

| Line | Security fixes |
|---|---|
| Standalone beta (`standalone-beta-impl`, until released) | Yes, best effort while it is in beta |
| `main` (pre-standalone) | Not assessed |
