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
