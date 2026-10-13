# Telos Clean-Node Installation Guide

The server targets **Ubuntu 24.04+ only**. Windows, macOS, WSL/Docker Desktop,
and other Linux distributions are out of scope. See the binding
[platform policy](../product/platform-scope.md).

## Prerequisites and current boundary

- Ubuntu 24.04 or newer; initial reference architecture x86_64.
- Podman 5.0.0 or newer and the tested Compose provider. A reproducible runtime
  installation source must be established by the clean-node deployment phase.
- Linux kernel support for `openat2` and `renameat2`.
- Mode `0600` `.env` credentials and separately provisioned signing files.
- Platform-trusted HTTPS, with operator-controlled DNS and persistent storage.

The authoritative operator entry point is the Bash `./telos` dispatcher.
`backend/cmd/telos/` is experimental. Do not use the legacy Windows Compose
overlay as an installation path.

The clean production installation remains incomplete: hosted-browser delivery
requires the planned `telos-client` service and route split, and signing-secret
provisioning and upstream setup must be completed. Follow the standalone-beta
Phase 2 plan under the platform policy above; do not interpret this prerequisite
list as evidence that the current checkout installs a working production node.
Never run `telos init --force` against an existing node.

Certify a clean installation on Ubuntu 24.04, then run the same installation,
storage, upgrade, and recovery checks on newer Ubuntu releases. Record the exact
OS, runtime, and candidate versions tested.
