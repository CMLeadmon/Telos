package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubRuntimeAndCommands(t *testing.T, psOutput string, gatewayBytes []byte, gatewayErr error) {
	t.Setenv("TELOS_RUNTIME", "podman")
	t.Setenv("TELOS_COMPOSE_DRIVER", "podman-compose")

	tempDir := t.TempDir()
	composeContent := `
services:
  traefik:
    container_name: telos-traefik
  telos-migrate:
    container_name: telos-migrate
  telos-audiobook-migrate:
    profiles: ["migration"]
    container_name: telos-audiobook-migrate
  telos-core:
    container_name: telos-core
  postgres:
    container_name: telos-postgres
  redis:
    container_name: telos-redis
`
	if err := os.WriteFile(filepath.Join(tempDir, "docker-compose.yml"), []byte(composeContent), 0644); err != nil {
		t.Fatalf("failed to write mock docker-compose.yml: %v", err)
	}
	t.Setenv("TELOS_ROOT", tempDir)

	origRunner := commandRunner
	commandRunner = func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "ps" {
			return []byte(psOutput), nil
		}
		return nil, nil
	}
	t.Cleanup(func() { commandRunner = origRunner })

	origGateway := gatewayHealthFetcher
	gatewayHealthFetcher = func(url string) ([]byte, error) {
		if gatewayErr != nil {
			return nil, gatewayErr
		}
		return gatewayBytes, nil
	}
	t.Cleanup(func() { gatewayHealthFetcher = origGateway })
}

// TestStatusHealthyDefaultBootExit0 verifies that a healthy default boot returns exit 0,
// correctly classifies oneshot containers (telos-migrate exited 0 -> completed), and
// excludes profile-gated services (telos-audiobook-migrate).
func TestStatusHealthyDefaultBootExit0(t *testing.T) {
	psOut := `telos-migrate|Exited (0) 10 minutes ago
telos-core|Up 10 minutes
telos-postgres|Up 10 minutes (healthy)
telos-redis|Up 10 minutes (healthy)
telos-traefik|Up 10 minutes`

	stubRuntimeAndCommands(t, psOut, []byte(`{"status":"ok","checks":[{"name":"postgres","status":"ok","metric":"connected"}]}`), nil)

	var stdout, stderr bytes.Buffer
	code := runStatus(nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected status code 0 on healthy default boot, got %d. stderr: %s, stdout: %s", code, stderr.String(), stdout.String())
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "5/5 services up") {
		t.Errorf("expected '5/5 services up', got output:\n%s", outStr)
	}

	if strings.Contains(outStr, "telos-audiobook-migrate") {
		t.Errorf("BUG REGRESSION: telos-audiobook-migrate should NOT be listed on default boot status")
	}

	if !strings.Contains(outStr, "completed") {
		t.Errorf("expected oneshot container telos-migrate to be labeled 'completed', got output:\n%s", outStr)
	}
}

func TestStatusUnhealthyContainerExit1(t *testing.T) {
	psOut := `telos-migrate|Exited (0) 10 minutes ago
telos-core|Exited (1) 2 minutes ago
telos-postgres|Up 10 minutes (healthy)
telos-redis|Up 10 minutes (healthy)
telos-traefik|Up 10 minutes`

	stubRuntimeAndCommands(t, psOut, nil, os.ErrNotExist)

	var stdout, stderr bytes.Buffer
	code := runStatus(nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected status code 1 when a service has problems, got %d. stderr: %s, stdout: %s", code, stderr.String(), stdout.String())
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "4/5 services up") || !strings.Contains(outStr, "1 with problems") {
		t.Errorf("expected 4/5 services up, 1 with problems; got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "exited (code 1)") {
		t.Errorf("expected 'exited (code 1)' in output, got:\n%s", outStr)
	}
}

func TestStatusJSONOutput(t *testing.T) {
	psOut := `telos-migrate|Exited (0)
telos-core|Up 5 minutes
telos-postgres|Up 5 minutes (healthy)
telos-redis|Up 5 minutes (healthy)
telos-traefik|Up 5 minutes`

	gwJSON := `{"status":"ok","checks":[]}`
	stubRuntimeAndCommands(t, psOut, []byte(gwJSON), nil)

	var stdout, stderr bytes.Buffer
	code := runStatus([]string{"--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0 for status --json on healthy stack, got %d. stderr: %s", code, stderr.String())
	}

	var resp StatusJSONResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v, raw output: %s", err, stdout.String())
	}

	if resp.Runtime != "podman" {
		t.Errorf("expected runtime 'podman', got %s", resp.Runtime)
	}

	if len(resp.Containers) != 5 {
		t.Errorf("expected 5 containers in JSON output, got %d", len(resp.Containers))
	}

	for _, c := range resp.Containers {
		if c.Service == "telos-audiobook-migrate" {
			t.Errorf("telos-audiobook-migrate should be excluded from JSON output")
		}
	}
}

func TestStatusUnknownOption(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runStatus([]string{"--invalid-flag"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("expected exit code 2 for unknown status flag, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown option") {
		t.Errorf("stderr missing 'unknown option': %s", stderr.String())
	}
}

func TestStatusHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runStatus([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0 for --help, got %d", code)
	}
	if !strings.Contains(stdout.String(), "telos status — report boot state") {
		t.Errorf("stdout missing status help header: %s", stdout.String())
	}
}
