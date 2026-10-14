# Telos <version> — release notes template

> `scripts/stage-release.sh` refuses notes that do not state each of the points below. Replace every bracket with the
> facts of the certified candidate; do not keep a sentence that is not true for it.

## What this is

Telos <version> is an invite-only **unsigned beta**. The reference server is Ubuntu 24.04 or newer on x86-64 with rootful
Podman 5; no other server platform is supported.

## Supported clients

- **Browsers** (the hosted client, same origin as the API): [list exactly the browsers and platforms in the certified matrix].
- **Linux desktop** application: an unsigned AppImage for x86-64 Linux. Windows and macOS applications are not part of this release.
- Only **platform-trusted HTTPS** is supported (a public certificate, or a private CA that the platform trusts). Self-signed
  certificates and certificate pinning are not supported.

## Verify before you install

The packages are **unsigned**. Check them against the signed candidate before running anything:

```
sha256sum -c telos-<version>-candidate.tar.zst.sha256
ssh-keygen -Y verify -f <allowed-signers> -I <principal> -n telos-beta-certification-v1 -s certification.json.sig < certification.json
```

The signature and checksum instructions in `documentation/operations/release.md` and `beta-certification.md` apply.

## Known limitations

[State the real ones: no voice, no push notifications, no high availability, and any gate or platform that stayed unverified.]

## Backup and recovery

Encrypted off-node snapshots every six hours; a **recovery point objective** of at most 24 hours and a **recovery time
objective** of at most 4 hours, as demonstrated by the separate-host restore evidence of this candidate.

## Upgrading

Install the next candidate with `scripts/install-candidate.sh` and run `scripts/upgrade.sh`; see
`documentation/operations/upgrade-and-rollback.md`. A failed upgrade that migrated the database is undone from the
encrypted snapshot recorded in the journal.
