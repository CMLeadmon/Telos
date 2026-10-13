# Telos Clean-Node Installation Guide

The server runs on **Ubuntu 24.04 or newer, x86-64, with rootful Podman 5 or newer**.
Windows, macOS, WSL/Docker Desktop, other Linux distributions (including Ubuntu
derivatives), ARM and Docker-only hosts are not supported and have no best-effort
path. See the binding [platform policy](../product/platform-scope.md). The
authoritative operator entry point is the Bash `telos` command; `backend/cmd/telos/`
is experimental.

> **Status:** this procedure is implemented and covered by hermetic tests, and its
> prerequisites were verified on an Ubuntu 26.04.1 VM. The full install has
> **not yet been executed on a clean reference host** with trusted public HTTPS. That live run is the `clean-install` gate; follow
> [clean-install-evidence.md](clean-install-evidence.md) to produce its evidence.
> Until it passes on a frozen candidate, treat this guide as a procedure under test.

## What must exist before you start

| Requirement | Enforced by | Status of the source |
|---|---|---|
| Ubuntu 24.04+ (`ID=ubuntu`, `VERSION_ID` ≥ 24.04), x86-64, kernel ≥ 5.6 | `telos doctor`, `install.sh` | **26.04 is the verified reference release.** Any other release that meets the policy (including the 24.04 floor) is reported as **not yet a verified combination** until its own install, storage, upgrade and recovery checks run. |
| Podman **5.0.0+**, rootful (`sudo telos …`) | `telos doctor`, every compose call | Verified on 26.04 (archive 5.7.0); none for 24.04. See below. |
| `podman-compose` **1.6.0** at an absolute path in `PODMAN_COMPOSE_PROVIDER` | `telos doctor`, every compose call | Pinned artifact recorded below. |
| `openssl`, `python3`, `flock` (util-linux), `ss` (iproute2) | `telos doctor` | Ubuntu archive. `curl` is optional. |
| Free public ports **80 and 443** (or held by this node's own running Traefik) | `telos doctor`, `install.sh --check` | Production never falls back to alternate ports; only a development node (`TELOS_DEV=1`, rootless) may. |
| A DNS name pointing at the node and **inbound 80/443 from the internet** | the live drill | HTTP-01 certificate issuance needs the public port 80; loopback tests cannot prove it. |
| Persistent storage (`STORAGE_PATH`, default `/mnt/storage/shared`) | `telos doctor` | Operator-provided. |

The checks above are one shared implementation (`cli/lib/preflight.sh`) used by both
`install.sh` and `telos doctor`, so the two cannot disagree. Run it before anything
else; it changes nothing:

```bash
./install.sh --check
```

### Podman 5

| Release | Podman from the Ubuntu archive | Status |
|---|---|---|
| **26.04 (verified reference)** | 5.7.0 (`apt-cache policy podman`) | Verified: installed with `apt` on a 26.04.1 host, `sudo podman run` works (rootful, netavark, overlay). |
| 24.04 (policy floor) | below 5 | **No verified Podman 5 source.** `apt install podman` alone does not satisfy the floor; the preflight refuses it. |

On 26.04:

```bash
sudo apt-get install -y podman skopeo buildah uidmap
sudo podman run --rm docker.io/library/alpine:3.20 echo ok      # rootful smoke
```

For 24.04 this repository does not ship a recipe because none has been verified, and
inventing one would be worse than saying so. Do not mix distribution repositories and do
not lower the floor. When a source is verified, record the exact package or source URL,
version, signing key or checksum and install commands here, with the VM evidence (see
*Prerequisite validation* in [clean-install-evidence.md](clean-install-evidence.md)). Compare
against the [Ubuntu package index](https://packages.ubuntu.com/search?keywords=podman) and
[Podman's instructions](https://podman.io/docs/installation).

### The Compose provider

Telos drives Podman through `podman-compose` **1.6.0** (the provider the compose
contract was checked against), always by absolute path so a missing or substituted
provider can never fall through to Docker. Ubuntu's archive version (1.0.6 on 24.04) is
not accepted in production.

Pinned artifact (PyPI, upload 2026-06-03):

```text
podman_compose-1.6.0-py3-none-any.whl
sha256 98e48af25d44c10ecef948163bfcca002e32aa8bbb7e66593fbd4faff7270c30
```

Installation into a root-owned virtual environment, **verified on an Ubuntu 26.04.1 host**
(Podman 5.7.0): the wheel installs by hash and `podman-compose --version` prints
`podman version 5.7.0` then `podman-compose version 1.6.0` (the preflight reads the second
line). Its two dependencies, `pyyaml` and `python-dotenv`, come from the Ubuntu archive here;
a fully hash-pinned closure belongs to the Phase 4 tool lock:

```bash
sudo apt-get install -y python3-venv python3-yaml python3-dotenv
sudo python3 -m venv --system-site-packages /opt/telos-tools/podman-compose
cat > /tmp/podman-compose.req <<'EOF'
podman-compose==1.6.0 --hash=sha256:98e48af25d44c10ecef948163bfcca002e32aa8bbb7e66593fbd4faff7270c30
EOF
sudo /opt/telos-tools/podman-compose/bin/python -m pip install --require-hashes --no-deps -r /tmp/podman-compose.req
export PODMAN_COMPOSE_PROVIDER=/opt/telos-tools/podman-compose/bin/podman-compose
"$PODMAN_COMPOSE_PROVIDER" --version     # must print 1.6.0
```

Persist `PODMAN_COMPOSE_PROVIDER` for `sudo` (for example in `/etc/environment` or a
`sudoers` `env_keep` rule); `telos doctor` refuses a relative path, a non-executable
file and any version other than 1.6.0.

## Installation

Everything below runs on the node. Production is **rootful**: use `sudo telos …`.

1. **Get the tree and put the CLI on the PATH.**

   ```bash
   sudo ./install.sh --yes            # copies the tree to /opt/telos and links /usr/local/bin/telos
   ```

   `install.sh` installs the CLI only. It never installs system packages, never
   creates secrets and never touches data.

2. **Initialize the node (once).**

   ```bash
   sudo telos init --domain node.example.net --acme-email ops@example.net
   ```

   This creates `/var/lib/telos` (the node path, outside any release tree):
   `node.env` (root, `0600`), `config/` (`10001:10001`, `0700`) holding exactly the
   cursor and HLS signing files (`0600`), and `state/` (root, `0700`). Secrets are
   generated locally and never printed. `init` **never overwrites** a node, a signing
   file or storage, and there is no `--force`: if it finds a partial or foreign
   directory it refuses and says what to repair. To adopt an existing checkout `.env`
   (keeping the credentials its volumes already hold) use `--import-env PATH`.

3. **Configure the media services (guided).**

   ```bash
   sudo telos configure integrations --setup
   ```

   Follow [guided-integrations.md](guided-integrations.md). Jellyfin and Grimmory are
   published on `127.0.0.1` only during setup; from a remote workstation use the SSH
   tunnel the command prints. The command validates the credentials, records stable
   library IDs in `node.env` and removes the setup listeners.

4. **Check, then start.**

   ```bash
   sudo telos doctor                  # read-only diagnosis; prescribes targeted repairs
   sudo telos start
   sudo telos status
   ```

   `telos start` refuses, before Compose runs, while `node.env` holds placeholders,
   Jellyfin or Grimmory library IDs are missing, or a signing file is absent or has
   the wrong owner or mode; each defect is listed and `telos doctor` names one repair
   per defect. A taken public port is reported by `telos doctor`, not worked around.

5. **Prove it from outside the node.** Run the member-journey smoke and the install
   drill from a *different* machine, as described in
   [clean-install-evidence.md](clean-install-evidence.md). The smoke needs a media
   fixture manifest (a real video, audio, ebook and chat channel) because a mocked
   suite or an empty library cannot stand in for live reading and playback.

## Operating notes

- Re-run `sudo telos doctor` after any change; it never repairs destructively.
- Never run `telos init` against a node you want to keep unless it is the explicit,
  refusing, non-overwriting form above. Secrets cannot be regenerated without
  desynchronizing the existing Postgres and MariaDB volumes.
- The browser client is served by the separate `telos-client` container; the gateway
  serves only `/api/v1`. Traefik is the only public listener.
- Windows and macOS applications are future clients, never server targets. The first
  native client is a Linux desktop application that connects to a node.

Certify a clean installation on Ubuntu 26.04, then repeat the installation, storage,
upgrade and recovery checks on any other release (including the 24.04 floor) before calling it verified.
Record the exact OS, runtime and candidate versions tested.
