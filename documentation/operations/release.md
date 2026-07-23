# Telos Release Engineering and Verification Guide

This document describes the reproducible build system, OCI image pinning, artifact hashing DAG, detached Cosign bundle signing, and candidate staging workflows for Telos release engineering.

## Non-Circular Hash Directed Acyclic Graph (DAG)

1. `artifacts.json`: SHA-256 checksums of payload files.
2. `release-manifest.json`: Manifest containing artifact index hash and commit metadata.
3. `SHA256SUMS`: Checksums of payload files + `artifacts.json` + `release-manifest.json`.
4. `candidate-lock.json`: Immutable lock file referencing all release files and detached Cosign signatures.
