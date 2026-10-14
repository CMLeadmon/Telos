# Telos Release Engineering and Verification Guide

Release building, staging, signing, and certification are not implemented or
claimable in the current repository. Their current commands return `not_run`
(exit 3) until Phase 5 supplies candidate-bound release workflows and evidence.
The following DAG is the intended release shape, not an available procedure.

## Non-Circular Hash Directed Acyclic Graph (DAG)

1. `artifacts.json`: SHA-256 checksums of payload files.
2. `release-manifest.json`: Manifest containing artifact index hash and commit metadata.
3. `SHA256SUMS`: Checksums of payload files + `artifacts.json` + `release-manifest.json`.
4. `candidate-lock.json`: Immutable lock file referencing all release files and detached Cosign signatures.

## Generations on a node

An installed release is an immutable *generation* under `/srv/telos/generations/<candidate digest hex>/`, selected by
the single mutable symlink `/srv/telos/current`; mutable state (`node.env`, signing keys, journals, the installed
receipt) lives in `/var/lib/telos`, never in a generation. `generation.json` is candidate-independent: it carries no
candidate digest, install path or time, and it does not hash itself, so it can be built before the candidate lock exists.
The installed receipt (`state/installed-generation.json`) binds the candidate-lock digest to the manifest digest after
verification. Upgrading and rolling back between generations is described in
[`upgrade-and-rollback.md`](./upgrade-and-rollback.md); `release/predecessor-policy.json` names the exact earlier
release (tag, commit, candidate-lock digest, generation-manifest digest) an upgrade is certified from — or `null` while
none exists, in which case `scripts/build-predecessor.sh` reports `not_run`.
