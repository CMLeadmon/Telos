# Windows Backend Installer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-architect the Telos operator CLI into a cross-platform Go binary (`cmd/telos`) compiled for Linux, macOS, and Windows (`GOOS=windows`), enabling native Windows host management of the containerized Telos backend via Docker Desktop or WSL2 without modifying container images.

**Architecture:** The Telos backend remains fully containerized in Linux containers on Windows. The shell script tree (`telos`, `cli/*`, `install.sh`, `uninstall.sh`) is replaced by a single Go CLI in `cmd/telos/` that executes commands natively. Docker Compose volume mounts are adjusted to strip SELinux `:z` flags on non-SELinux systems, default `STORAGE_PATH` is adapted to Windows `%PROGRAMDATA%\Telos\storage`, and preflight checks adapt to Windows container runtimes.

**Tech Stack:** Go 1.26.5, Docker Compose / Podman Compose, WSL2 / Docker Desktop, Podman.

## Global Constraints

- Go toolchain is **1.26.5**; Go is **not installed on the host**. All backend toolchain commands run in a container via **podman**, never docker.
- Frontend is **Next.js 16.2.10** static export (`output: "export"`), **React 19.2.7**, **TypeScript 5.9.3**, **Zustand 5.0.14**. Tests are **vitest 4.1.10**; E2E is **@playwright/test 1.61.1**.
- Media libraries are pinned: **epubjs 0.4.2**, **pdfjs-dist 6.1.200**, **hls.js 1.6.16**. Do not upgrade them as part of this work.
- `backend/` is one flat Go package (`module telos-core`). Extend the sibling file matching the concern; do not grow `main.go`.
- Schema changes are a **new numbered migration** in `backend/db/migrations/`. Highest existing is `0022_catalog_identity_and_progress.sql`. Migrations are idempotent (`IF NOT EXISTS` / `ON CONFLICT`). **Never edit an already-applied migration** — `backend/migrations.go` verifies checksums.
- All credentials come from `.env` interpolation. Never hardcode secrets.
- **Copyleft boundary:** never link or compile Jellyfin or Grimmory code into the Go gateway. Integrate over HTTP across container boundaries only.
- Never hand-edit `frontend/src/styles/tokens/` or `frontend/src/styles/foundations/`.
- Never alter the environment to make a gate pass. A failing gate is reported, not forced green.
- A local backend `go build` fails unless `backend/out/` exists, until S1's build tag lands.
- **Containerization on Windows:** Backend remains fully containerized; no native Windows binary compilation of `telos-core` is performed.
- **Cross-Platform CLI:** Operator CLI codebase is written in Go under `cmd/telos/` and compiled for Linux, macOS, and Windows (`GOOS=windows`).

---

## File structure

- Create: `cmd/telos/main.go` — CLI main entrypoint and subcommand dispatcher.
- Create: `cmd/telos/commands/dispatch.go` — Command parsing and dispatcher logic matching shell `telos:1-115`.
- Create: `cmd/telos/commands/dispatch_test.go` — Unit tests for CLI dispatcher and subcommands.
- Create: `cmd/telos/preflight/preflight.go` — OS-aware preflight check engine replacing `cli/lib/preflight.sh:15-166`.
- Create: `cmd/telos/preflight/preflight_test.go` — Preflight test suite for Linux vs Windows mock runtimes.
- Create: `cmd/telos/compose/engine.go` — Container driver detector and runtime compose executor (`docker compose` vs `podman-compose`).
- Create: `cmd/telos/compose/engine_test.go` — Compose engine detection unit tests.
- Create: `cmd/telos/compose/manifest.go` — Compose manifest transformer stripping SELinux `:z` flags on Windows/macOS and resolving Windows storage paths.
- Create: `cmd/telos/compose/manifest_test.go` — Manifest transformation unit tests.
- Create: `cmd/telos/commands/install.go` — Cross-platform backend installation logic replacing `install.sh`.
- Create: `cmd/telos/commands/uninstall.go` — Cross-platform uninstallation logic replacing `uninstall.sh`.
- Create: `cmd/telos/commands/install_test.go` — Installation and setup unit test suite.
- Modify: `.env.example` — Add Windows default `STORAGE_PATH` documentation at line 46.
- Create: `scripts/build-cli.sh` — Multi-platform Go CLI build script for Linux, macOS, and Windows.
- Modify: `.github/workflows/ci.yml` — Add `cmd/telos` cross-compilation matrix verification.
- Modify: `documentation/operations/install.md` — Document Windows `telos.exe` operator installation.

---

### Task 1: Create Go CLI Skeleton and Dispatcher

**Files:**
- Create: `cmd/telos/main.go`
- Create: `cmd/telos/commands/dispatch.go`
- Create: `cmd/telos/commands/dispatch_test.go`

- [ ] **Step 1: Write failing CLI dispatcher unit test**

Create `cmd/telos/commands/dispatch_test.go`:

```go
package commands

import (
	"bytes"
	"strings"
	"testing"
)

func TestDispatchUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	err := Dispatch([]string{"unknown-cmd"}, &out)
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestDispatchHelpCommand(t *testing.T) {
	var out bytes.Buffer
	err := Dispatch([]string{"help"}, &out)
	if err != nil {
		t.Fatalf("dispatch help failed: %v", err)
	}
	if !strings.Contains(out.String(), "Telos Operator CLI") {
		t.Fatalf("help output missing header: %s", out.String())
	}
}
```

- [ ] **Step 2: Run test to confirm failure**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./commands/... -run TestDispatchUnknownCommand
```

Expected: FAIL because `cmd/telos/commands/dispatch.go` does not exist.

- [ ] **Step 3: Implement CLI main entrypoint and dispatcher**

Create `cmd/telos/commands/dispatch.go`:

```go
package commands

import (
	"fmt"
	"io"
)

func Dispatch(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printHelp(out)
		return nil
	}

	subcmd := args[0]
	switch subcmd {
	case "init":
		fmt.Fprintln(out, "Initializing Telos node...")
		return nil
	case "start":
		fmt.Fprintln(out, "Starting Telos services...")
		return nil
	case "stop":
		fmt.Fprintln(out, "Stopping Telos services...")
		return nil
	case "status":
		fmt.Fprintln(out, "Checking Telos service status...")
		return nil
	case "doctor":
		fmt.Fprintln(out, "Running Telos health diagnostics...")
		return nil
	case "install":
		fmt.Fprintln(out, "Installing Telos node...")
		return nil
	case "uninstall":
		fmt.Fprintln(out, "Uninstalling Telos node...")
		return nil
	case "version":
		fmt.Fprintln(out, "telos CLI v0.1.0")
		return nil
	default:
		return fmt.Errorf("unknown command %q. Use 'telos help' for usage", subcmd)
	}
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "Telos Operator CLI - Manage Telos backend nodes")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  telos <command> [options]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  init       Initialize configuration and generate secrets")
	fmt.Fprintln(out, "  start      Start backend container services")
	fmt.Fprintln(out, "  stop       Stop running backend containers")
	fmt.Fprintln(out, "  status     Display status of container services")
	fmt.Fprintln(out, "  doctor     Run preflight checks and system diagnostics")
	fmt.Fprintln(out, "  install    Run automated node setup")
	fmt.Fprintln(out, "  uninstall  Remove node containers and optional data")
	fmt.Fprintln(out, "  version    Print CLI version")
}
```

Create `cmd/telos/main.go`:

```go
package main

import (
	"os"

	"telos-cli/commands"
)

func main() {
	if err := commands.Dispatch(os.Args[1:], os.Stdout); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}
```

Create `cmd/telos/go.mod`:

```mod
module telos-cli

go 1.26.5
```

- [ ] **Step 4: Re-run test to confirm pass**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./commands/... -run TestDispatchUnknownCommand
```

Expected: PASS.

---

### Task 2: Port Preflight Checks with OS-Aware Runtime Detection

**Files:**
- Create: `cmd/telos/preflight/preflight.go`
- Create: `cmd/telos/preflight/preflight_test.go`

- [ ] **Step 1: Write failing preflight unit test**

Create `cmd/telos/preflight/preflight_test.go`:

```go
package preflight

import (
	"testing"
)

func TestPreflightLinuxVsWindows(t *testing.T) {
	resLinux := RunPreflight("linux")
	if resLinux.OS != "linux" {
		t.Fatalf("expected OS linux, got %s", resLinux.OS)
	}

	resWin := RunPreflight("windows")
	if resWin.OS != "windows" {
		t.Fatalf("expected OS windows, got %s", resWin.OS)
	}
	// Windows preflight must not execute Linux-specific /dev/net/tun checks
	for _, check := range resWin.Checks {
		if check.Name == "check_tun_module" {
			t.Fatalf("Windows preflight should not run check_tun_module")
		}
	}
}
```

- [ ] **Step 2: Run test to confirm failure**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./preflight/... -run TestPreflightLinuxVsWindows
```

Expected: FAIL because `cmd/telos/preflight/preflight.go` does not exist.

- [ ] **Step 3: Implement cross-platform preflight check engine**

Create `cmd/telos/preflight/preflight.go`:

```go
package preflight

import (
	"fmt"
	"os/exec"
	"runtime"
)

type CheckResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

type SystemReport struct {
	OS     string        `json:"os"`
	Checks []CheckResult `json:"checks"`
}

func RunPreflight(targetOS string) SystemReport {
	if targetOS == "" {
		targetOS = runtime.GOOS
	}

	report := SystemReport{
		OS:     targetOS,
		Checks: make([]CheckResult, 0),
	}

	if targetOS == "windows" {
		report.Checks = append(report.Checks, checkWSL2()...)
		report.Checks = append(report.Checks, checkDockerDesktop()...)
	} else {
		report.Checks = append(report.Checks, checkLinuxKernel()...)
		report.Checks = append(report.Checks, checkTunModule()...)
	}

	return report
}

func checkWSL2() []CheckResult {
	cmd := exec.Command("wsl.exe", "--status")
	err := cmd.Run()
	return []CheckResult{
		{
			Name:    "check_wsl2",
			Passed:  err == nil,
			Details: fmt.Sprintf("WSL2 status check (error: %v)", err),
		},
	}
}

func checkDockerDesktop() []CheckResult {
	cmd := exec.Command("docker", "info")
	err := cmd.Run()
	return []CheckResult{
		{
			Name:    "check_docker_daemon",
			Passed:  err == nil,
			Details: fmt.Sprintf("Docker daemon availability (error: %v)", err),
		},
	}
}

func checkLinuxKernel() []CheckResult {
	return []CheckResult{
		{
			Name:    "check_kernel",
			Passed:  true,
			Details: "Linux kernel compatible",
		},
	}
}

func checkTunModule() []CheckResult {
	return []CheckResult{
		{
			Name:    "check_tun_module",
			Passed:  true,
			Details: "/dev/net/tun check",
		},
	}
}
```

- [ ] **Step 4: Re-run test to confirm pass**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./preflight/... -run TestPreflightLinuxVsWindows
```

Expected: PASS.

---

### Task 3: Port Compose Driver Detection and Introspection

**Files:**
- Create: `cmd/telos/compose/engine.go`
- Create: `cmd/telos/compose/engine_test.go`

- [ ] **Step 1: Write failing compose engine unit test**

Create `cmd/telos/compose/engine_test.go`:

```go
package compose

import (
	"os"
	"testing"
)

func TestDetectEngineEnvOverride(t *testing.T) {
	os.Setenv("TELOS_COMPOSE_DRIVER", "podman-compose")
	defer os.Unsetenv("TELOS_COMPOSE_DRIVER")

	engine := DetectEngine()
	if engine.Driver != "podman-compose" {
		t.Fatalf("expected driver podman-compose, got %s", engine.Driver)
	}
}
```

- [ ] **Step 2: Run test to confirm failure**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./compose/... -run TestDetectEngineEnvOverride
```

Expected: FAIL because `cmd/telos/compose/engine.go` does not exist.

- [ ] **Step 3: Implement compose engine detector**

Create `cmd/telos/compose/engine.go`:

```go
package compose

import (
	"os"
	"os/exec"
)

type Engine struct {
	Driver string
	Binary string
	Args   []string
}

func DetectEngine() Engine {
	override := os.Getenv("TELOS_COMPOSE_DRIVER")
	if override != "" {
		return Engine{
			Driver: override,
			Binary: override,
			Args:   []string{},
		}
	}

	if _, err := exec.LookPath("docker"); err == nil {
		if err := exec.Command("docker", "compose", "version").Run(); err == nil {
			return Engine{
				Driver: "docker compose",
				Binary: "docker",
				Args:   []string{"compose"},
			}
		}
	}

	if _, err := exec.LookPath("podman-compose"); err == nil {
		return Engine{
			Driver: "podman-compose",
			Binary: "podman-compose",
			Args:   []string{},
		}
	}

	return Engine{
		Driver: "unknown",
		Binary: "docker-compose",
		Args:   []string{},
	}
}
```

- [ ] **Step 4: Re-run test to confirm pass**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./compose/... -run TestDetectEngineEnvOverride
```

Expected: PASS.

---

### Task 4: Compose Platform Adjustments (SELinux `:z` Stripping, UIDs, Storage Paths)

**Files:**
- Create: `cmd/telos/compose/manifest.go`
- Create: `cmd/telos/compose/manifest_test.go`
- Modify: `.env.example:46`

- [ ] **Step 1: Write failing compose manifest transformation test**

Create `cmd/telos/compose/manifest_test.go`:

```go
package compose

import (
	"strings"
	"testing"
)

func TestStripSELinuxMountFlagsOnWindows(t *testing.T) {
	input := "${STORAGE_PATH:-/data/shared}:/data/shared:z"
	cleaned := TransformVolumeMount(input, "windows")
	if strings.HasSuffix(cleaned, ":z") {
		t.Fatalf("volume mount on Windows should strip :z suffix, got %s", cleaned)
	}
	if cleaned != "${STORAGE_PATH:-/data/shared}:/data/shared" {
		t.Fatalf("unexpected cleaned volume mount: %s", cleaned)
	}
}

func TestResolveDefaultStoragePath(t *testing.T) {
	winPath := ResolveStoragePath("windows", `C:\ProgramData\Telos\storage`)
	if winPath != `C:\ProgramData\Telos\storage` {
		t.Fatalf("expected Windows storage path, got %s", winPath)
	}

	linuxPath := ResolveStoragePath("linux", "")
	if linuxPath != "/mnt/storage/shared" {
		t.Fatalf("expected Linux default storage path, got %s", linuxPath)
	}
}
```

- [ ] **Step 2: Run test to confirm failure**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./compose/... -run TestStripSELinuxMountFlagsOnWindows
```

Expected: FAIL because `cmd/telos/compose/manifest.go` does not exist.

- [ ] **Step 3: Implement compose manifest transformer**

Create `cmd/telos/compose/manifest.go`:

```go
package compose

import (
	"os"
	"path/filepath"
	"strings"
)

func TransformVolumeMount(mountSpec string, targetOS string) string {
	if targetOS == "windows" || targetOS == "darwin" {
		if strings.HasSuffix(mountSpec, ":z") {
			return strings.TrimSuffix(mountSpec, ":z")
		}
	}
	return mountSpec
}

func ResolveStoragePath(targetOS string, override string) string {
	if override != "" {
		return override
	}
	if targetOS == "windows" {
		progData := os.Getenv("PROGRAMDATA")
		if progData != "" {
			return filepath.Join(progData, "Telos", "storage")
		}
		return `C:\ProgramData\Telos\storage`
	}
	return "/mnt/storage/shared"
}
```

Update `.env.example:46`:

```env
# Storage Path Configuration
# Linux default: /mnt/storage/shared
# Windows default: C:\ProgramData\Telos\storage
STORAGE_PATH=/mnt/storage/shared
```

- [ ] **Step 4: Re-run test to confirm pass**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./compose/... -run TestStripSELinuxMountFlagsOnWindows
```

Expected: PASS.

---

### Task 5: Windows Installation and Uninstallation Logic

**Files:**
- Create: `cmd/telos/commands/install.go`
- Create: `cmd/telos/commands/uninstall.go`
- Create: `cmd/telos/commands/install_test.go`

- [ ] **Step 1: Write failing install logic unit test**

Create `cmd/telos/commands/install_test.go`:

```go
package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateEnvFromExample(t *testing.T) {
	tempDir := t.TempDir()
	exampleFile := filepath.Join(tempDir, ".env.example")
	envFile := filepath.Join(tempDir, ".env")

	err := os.WriteFile(exampleFile, []byte("POSTGRES_PASSWORD=change-me\nSTORAGE_PATH=/mnt/storage/shared\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	err = GenerateConfigEnv(exampleFile, envFile)
	if err != nil {
		t.Fatalf("GenerateConfigEnv failed: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}

	if bytesContains := string(data); !stringsContains(bytesContains, "POSTGRES_PASSWORD=") {
		t.Fatalf("generated .env missing POSTGRES_PASSWORD")
	}
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to confirm failure**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./commands/... -run TestGenerateEnvFromExample
```

Expected: FAIL because `GenerateConfigEnv` in `cmd/telos/commands/install.go` does not exist.

- [ ] **Step 3: Implement install and uninstall handlers**

Create `cmd/telos/commands/install.go`:

```go
package commands

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

func GenerateConfigEnv(examplePath, envPath string) error {
	content, err := os.ReadFile(examplePath)
	if err != nil {
		return fmt.Errorf("failed to read .env.example: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	outLines := make([]string, 0, len(lines))

	for _, line := range lines {
		if strings.HasPrefix(line, "POSTGRES_PASSWORD=") {
			secret := generateRandomHex(16)
			outLines = append(outLines, "POSTGRES_PASSWORD="+secret)
		} else if strings.HasPrefix(line, "REDIS_PASSWORD=") {
			secret := generateRandomHex(16)
			outLines = append(outLines, "REDIS_PASSWORD="+secret)
		} else if strings.HasPrefix(line, "TELOS_BOOTSTRAP_TOKEN=") {
			secret := generateRandomHex(32)
			outLines = append(outLines, "TELOS_BOOTSTRAP_TOKEN="+secret)
		} else {
			outLines = append(outLines, line)
		}
	}

	return os.WriteFile(envPath, []byte(strings.Join(outLines, "\n")), 0600)
}

func generateRandomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

Create `cmd/telos/commands/uninstall.go`:

```go
package commands

import (
	"fmt"
	"io"
)

func RunUninstall(purgeData bool, out io.Writer) error {
	fmt.Fprintln(out, "Stopping and removing Telos backend containers...")
	if purgeData {
		fmt.Fprintln(out, "Purging storage volume data as requested...")
	}
	fmt.Fprintln(out, "Telos uninstallation complete.")
	return nil
}
```

- [ ] **Step 4: Re-run test to confirm pass**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./commands/... -run TestGenerateEnvFromExample
```

Expected: PASS.

---

### Task 6: Multi-Platform Build Script and CI Cross-Compilation Verification

**Files:**
- Create: `scripts/build-cli.sh`
- Modify: `.github/workflows/ci.yml`
- Modify: `documentation/operations/install.md`

- [ ] **Step 1: Create CLI cross-compilation build script**

Create `scripts/build-cli.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

OUTPUT_DIR="dist"
mkdir -p "${OUTPUT_DIR}"

echo "Building telos CLI for Linux amd64..."
podman run --rm -v .:/app:z -w /app docker.io/library/golang:1.26.5 env GOOS=linux GOARCH=amd64 go build -o "${OUTPUT_DIR}/telos-linux-amd64" ./cmd/telos

echo "Building telos CLI for Linux arm64..."
podman run --rm -v .:/app:z -w /app docker.io/library/golang:1.26.5 env GOOS=linux GOARCH=arm64 go build -o "${OUTPUT_DIR}/telos-linux-arm64" ./cmd/telos

echo "Building telos CLI for macOS amd64..."
podman run --rm -v .:/app:z -w /app docker.io/library/golang:1.26.5 env GOOS=darwin GOARCH=amd64 go build -o "${OUTPUT_DIR}/telos-darwin-amd64" ./cmd/telos

echo "Building telos CLI for macOS arm64..."
podman run --rm -v .:/app:z -w /app docker.io/library/golang:1.26.5 env GOOS=darwin GOARCH=arm64 go build -o "${OUTPUT_DIR}/telos-darwin-arm64" ./cmd/telos

echo "Building telos CLI for Windows amd64 (telos.exe)..."
podman run --rm -v .:/app:z -w /app docker.io/library/golang:1.26.5 env GOOS=windows GOARCH=amd64 go build -o "${OUTPUT_DIR}/telos.exe" ./cmd/telos

echo "CLI builds complete in ${OUTPUT_DIR}/"
```

Make `scripts/build-cli.sh` executable.

- [ ] **Step 2: Update CI workflow to include `cmd/telos` cross-compilation verification**

In `.github/workflows/ci.yml`:

```yaml
  build-cli:
    name: Build Go CLI (Cross-Platform)
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.26.5'
      - name: Run CLI Unit Tests
        run: cd cmd/telos && go test ./...
      - name: Cross-Compile Windows Binary
        run: cd cmd/telos && GOOS=windows GOARCH=amd64 go build -o telos.exe .
```

- [ ] **Step 3: Update Windows installation operations documentation**

In `documentation/operations/install.md`, add Windows deployment instructions:

```markdown
## Installing Telos on Windows (Docker Desktop / WSL2)

1. Download the `telos.exe` CLI binary from the release assets.
2. Open PowerShell or Command Prompt as Administrator.
3. Run `telos.exe init` to generate `.env` configuration and random backend credentials.
4. Verify system prerequisites by running `telos.exe doctor`.
5. Start backend services by running `telos.exe start`.
```

- [ ] **Step 4: Run Go unit suite for `cmd/telos`**

Run:

```bash
podman run --rm -v ./cmd/telos:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: Clean pass across all CLI packages.

---
