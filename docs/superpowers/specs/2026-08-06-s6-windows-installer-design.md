# Windows Backend Installer — Design

The Windows Backend Installer sub-project enables node operators to deploy, run, and manage the containerized Telos backend on Windows hosts via Docker Desktop or WSL2. Rather than maintaining dual Bash and PowerShell script trees, this sub-project rewrites the operator CLI (`telos`) into a cross-platform Go binary compiled for Linux, macOS, and Windows (`GOOS=windows`). It adapts Docker Compose volume flags and default storage paths for non-SELinux Windows environments while preserving full containerization of the gateway and backend services. This sub-project is fully parallelizable and has zero dependencies on other sub-projects.

## 1. Purpose

### 1.1 Decisions

| Decision | Specification |
|---|---|
| Windows Backend Strategy | The backend stays containerized on Windows via Docker Desktop / WSL2 running the same Linux images. This is an installer port, not a native-binary port. |
| CLI Architecture | Rewrite the operator CLI as a Go CLI, not PowerShell. One cross-platform binary instead of two divergent script trees, and it can share config and validation code with the gateway. |
| Code Impact | Because the gateway stays containerized, the hardcoded POSIX storage-path literals and the base64'd `filepath.Join` file IDs stop being problems. Leave code comments noting they would bite hard on a native-binary path — do not fix them here. |
| Compose Adjustments | Compose adjustments in scope: strip `:z` on non-SELinux hosts, branch the UID/GID settings, supply a platform-appropriate storage-path default, and verify the fixed IPAM subnet that feeds the trusted-proxy CIDR list survives Podman-on-Windows. |
| Parallelization | Fully parallelizable — depends on no other sub-project. |

### 1.2 Success criteria

1. Operators can install and manage a Telos backend node on Windows 10/11 using a single compiled `telos.exe` CLI binary.
2. The Go operator CLI executes `init`, `start`, `stop`, `status`, and `doctor` commands equivalently across Windows, Linux, and macOS.
3. `docker-compose.yml` mounts container volumes without SELinux `:z` flag errors on Windows and macOS hosts.
4. Default `STORAGE_PATH` resolves to a valid Windows path (e.g. `C:\ProgramData\Telos\storage` or WSL2 mount) when running on Windows.
5. Fixed IPAM network subnet (`10.201.0.0/24`) and trusted proxy headers function properly under Docker Desktop / WSL2 / Podman Desktop.

## 2. Verified current state

- **Existing Shell Script Inventory:** The operator script surface currently comprises 1,437 lines of Bash across 10 files:
  1. `telos` (dispatcher): 115 lines
  2. `install.sh` (installer): 224 lines
  3. `uninstall.sh` (uninstaller): 95 lines
  4. `cli/lib/common.sh`: 90 lines
  5. `cli/lib/preflight.sh`: 212 lines
  6. `cli/commands/doctor.sh`: 183 lines
  7. `cli/commands/init.sh`: 237 lines
  8. `cli/commands/start.sh`: 67 lines
  9. `cli/commands/status.sh`: 152 lines
  10. `cli/commands/stop.sh`: 62 lines
- **Linux-Only Preflight Constructs:** `cli/lib/preflight.sh` contains multiple Linux-specific checks that break on Windows:
  - `/etc/os-release` distro parsing in `detect_distro()` (`L15-23`) and `distro_family()` (`L25-49`).
  - `/dev/net/tun` check for rootless networking in `check_tun_module()` (`L122-133`).
  - `fuse-overlayfs` driver check in `check_overlay_driver()` (`L100-119`).
  - Container registry config search at `~/.config/containers/registries.conf` and `/etc/containers/registries.conf` in `check_registries()` (`L136-151`).
  - Sysctl check for `net.ipv4.ip_unprivileged_port_start` in `check_privileged_ports()` (`L154-166`).
- **SELinux `:z` Volume Mounts:** `docker-compose.yml` hardcodes SELinux `:z` relabeling flags at 4 bind mount locations:
  - Line 101: `${STORAGE_PATH:-/data/shared}:/data/shared:z`
  - Line 150: `${STORAGE_PATH:-/mnt/storage/shared}:/data/shared:z`
  - Line 325: `${STORAGE_PATH:-/mnt/storage/shared}/books:/books:z`
  - Line 326: `${STORAGE_PATH:-/mnt/storage/shared}/bookdrop:/bookdrop:z`
- **UID/GID and Storage Configuration:** `docker-compose.yml:262` specifies `user: "${APP_UID}:${APP_GID}"` and lines 308-309 pass `USER_ID=${APP_UID}` / `GROUP_ID=${APP_GID}`. Default storage path is hardcoded as `STORAGE_PATH=/mnt/storage/shared` in `.env.example:46`.
- **Fixed IPAM Subnet:** `docker-compose.yml:9` defines network subnet `10.201.0.0/24` (`telos-ingress`), feeding `TELOS_TRUSTED_PROXY_CIDRS` at line 136 and `.env.example:37`.
- **POSIX Path Literals & Base64 ID References in Container Backend:** Hardcoded POSIX `/data/shared` literals exist in `backend/main.go:372-387`, `3304`, `3558`, `3636-3639`, `3779`, `3822`, `backend/settings.go:887`, and `backend/health.go:309`. Base64-encoded `filepath.Join` relative path IDs exist at `backend/main.go:3330-3333` and `3435-3436`. Because backend execution remains containerized in Linux containers on Windows, these Linux paths remain valid inside the container environment and do not need modification.

## 3. Architecture

### 3.1 Boundaries

- Replaces shell scripts (`telos`, `cli/*`, `install.sh`, `uninstall.sh`) with a Go CLI codebase (`cmd/telos/`).
- The backend Go gateway (`backend/`) and Docker container images remain Linux-native; no Windows native binary compilation of `telos-core` is required.
- Configuration `.env` files remain compatible across OS platforms.

### 3.2 Go Operator CLI Architecture

The new operator CLI is written in Go under `cmd/telos/` and compiled to cross-platform binaries:

```
                               ┌─────────────────────────┐
                               │       cmd/telos/        │
                               │  Cross-Platform Go CLI  │
                               └────────────┬────────────┘
                                            │
                ┌───────────────────────────┼───────────────────────────┐
                ▼                           ▼                           ▼
       ┌─────────────────┐         ┌─────────────────┐         ┌─────────────────┐
       │ Linux OS        │         │ macOS Host      │         │ Windows Host    │
       │ Podman / Docker │         │ Docker Desktop  │         │ WSL2 / Docker   │
       └─────────────────┘         └─────────────────┘         └─────────────────┘
```

The Go CLI implements command handlers (`init`, `start`, `stop`, `status`, `doctor`) natively using cross-platform Go standard libraries (`os`, `exec`, `path/filepath`), replacing external shell tool dependencies (`sed`, `awk`, `grep`).

### 3.3 Windows Container Runtime & WSL2 / Docker Desktop Layer

On Windows, the `telos` CLI detects available container engines in order:
1. Docker Desktop with WSL2 backend (`docker info`).
2. Podman Desktop / WSL2 machine (`podman info`).
3. Direct WSL2 distro container runtime.

Preflight checks under Windows evaluate container engine responsiveness and memory allocations (>4 GiB recommended) rather than Linux-specific kernel modules (`/dev/net/tun` or `fuse-overlayfs`).

### 3.4 Docker Compose & Storage Adjustments

- **SELinux Mount Flags:** `:z` suffix on volume mounts is required on RHEL/Fedora SELinux systems but can cause volume errors or warnings on Windows/macOS. The Go CLI strips `:z` or uses dynamic Compose file overlays (`docker-compose.override.yml`) when running on non-SELinux hosts.
- **UID/GID Defaults:** Windows environments do not use POSIX numerical UIDs. When `APP_UID` / `APP_GID` are unset on Windows, `telos init` defaults them to `1000:1000` inside the Linux container.
- **Windows Storage Path:** Default `STORAGE_PATH` on Windows resolves to `%PROGRAMDATA%\Telos\storage` (e.g. `C:\ProgramData\Telos\storage`), mounted into WSL2 container space.

## 4. Contract changes

- **CLI Artifact Output:**
  - `telos` (Linux/macOS binary)
  - `telos.exe` (Windows binary)
- **New CLI Environment Overrides:**
  - `TELOS_COMPOSE_DRIVER`: Explicitly forces `docker compose` or `podman-compose`.

## 5. Implementation surface

| Path | Change | Rationale |
|---|---|---|
| `cmd/telos/main.go` | Create Go CLI entrypoint | Main command dispatcher replacing top-level `telos` script. |
| `cmd/telos/commands/` | Implement CLI command modules | Go handlers for `init`, `start`, `stop`, `status`, and `doctor`. |
| `cmd/telos/preflight/` | Implement cross-platform preflight checks | Replaces `cli/lib/preflight.sh` with OS-aware runtime detection. |
| `docker-compose.yml` | Update volume mounts with SELinux conditional handling | Prevents `:z` volume mount failures on Windows/macOS. |
| `.env.example` | Document Windows storage path defaults | Clarifies `STORAGE_PATH` formatting for Windows operators. |
| `scripts/build-cli.sh` | Create cross-compilation build script | Compiles `telos` CLI for `GOOS=linux`, `GOOS=darwin`, and `GOOS=windows`. |

## 6. Failure handling and observability

- **WSL2 Not Enabled:** Preflight detects missing WSL2 runtime on Windows and outputs step-by-step `wsl --install` instructions.
- **Docker/Podman Not Running:** CLI reports clear status error when daemon socket is unreachable, offering OS-specific start commands.
- **Port Conflict:** Detects if `:80` or `:443` are occupied on Windows (e.g. by IIS or World Wide Web Publishing Service) and suggests alternative `TRAEFIK_HTTP_PORT` configuration.

## 7. Security and privacy

- Go CLI binary execution does not require elevated Administrator privileges on Windows unless binding privileged low ports directly on host interfaces.
- Container secrets generated by `telos init` are saved with restricted file ACLs on Windows.

## 8. Verification

Verification requires running unit tests and cross-compilation checks:

```bash
# Go unit tests and cross-compilation check (via podman container per AGENTS.md §5)
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v .:/app:z -w /app docker.io/library/golang:1.26.5 env GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/telos
```

New verification tests:
- `cmd/telos/preflight/preflight_test.go`: Tests preflight reporting on Windows vs Linux mock environments.
- `cmd/telos/commands/init_test.go`: Verifies `.env` generation and path resolution across OS platforms.

## 9. Documentation changes required with implementation

- Update `documentation/operations/install.md` with Windows installation instructions using `telos.exe`.
- Update `CLAUDE.md` to document the Go CLI codebase and build commands.

## 10. Deferred / out of scope

- Native Windows service (`NT Service` / `sc.exe`) registration (containers managed via Docker Desktop / Podman service).
- Native Windows binary port of `telos-core` gateway without containerization.
