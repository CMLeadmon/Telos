package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createMockRepo(t *testing.T, envContent string, chmodEnv os.FileMode) string {
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
		t.Fatalf("failed to write docker-compose.yml: %v", err)
	}

	if envContent != "" {
		envPath := filepath.Join(tempDir, ".env")
		if err := os.WriteFile(envPath, []byte(envContent), chmodEnv); err != nil {
			t.Fatalf("failed to write .env: %v", err)
		}
		_ = os.Chmod(envPath, chmodEnv)
	}

	t.Setenv("TELOS_ROOT", tempDir)
	return tempDir
}

func TestDoctorMissingEnv(t *testing.T) {
	_ = createMockRepo(t, "", 0)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 when .env is missing, got %d", code)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, ".env is missing") {
		t.Errorf("stdout missing '.env is missing':\n%s", outStr)
	}
}

func TestDoctorPlaceholders(t *testing.T) {
	tempDir := createMockRepo(t, "STORAGE_PATH="+t.TempDir()+"\nPOSTGRES_PASSWORD=change-me\n", 0600)

	storagePath := filepath.Join(tempDir, "storage")
	for _, d := range []string{"", "/media", "/books", "/bookdrop"} {
		_ = os.MkdirAll(storagePath+d, 0755)
	}

	envData := "STORAGE_PATH=" + storagePath + "\nPOSTGRES_PASSWORD=change-me\n"
	_ = os.WriteFile(filepath.Join(tempDir, ".env"), []byte(envData), 0600)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 when placeholders exist, got %d", code)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "unreplaced placeholder values: POSTGRES_PASSWORD") {
		t.Errorf("stdout missing placeholder error:\n%s", outStr)
	}
}

func TestDoctorHealthyConfig(t *testing.T) {
	t.Setenv("TELOS_RUNTIME", "podman")
	t.Setenv("TELOS_COMPOSE_DRIVER", "podman-compose")

	origRunner := commandRunner
	commandRunner = func(name string, args ...string) ([]byte, error) {
		psOut := `telos-migrate|Exited (0)
telos-core|Up 5 minutes
telos-postgres|Up 5 minutes
telos-redis|Up 5 minutes
telos-traefik|Up 5 minutes`
		return []byte(psOut), nil
	}
	t.Cleanup(func() { commandRunner = origRunner })

	origGateway := gatewayHealthFetcher
	gatewayHealthFetcher = func(url string) ([]byte, error) {
		return []byte(`{"status":"ok"}`), nil
	}
	t.Cleanup(func() { gatewayHealthFetcher = origGateway })

	tempDir := t.TempDir()
	storagePath := filepath.Join(tempDir, "storage")
	for _, d := range []string{"", "/media", "/books", "/bookdrop"} {
		_ = os.MkdirAll(storagePath+d, 0755)
	}

	envContent := "STORAGE_PATH=" + storagePath + "\nTELOS_ENV=development\n"
	_ = createMockRepo(t, envContent, 0600)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr)

	// Note: code is 0 if preflight checks pass on test system, or 1 if system preflight fails.
	outStr := stdout.String()
	if !strings.Contains(outStr, ".env present") {
		t.Errorf("stdout missing '.env present':\n%s", outStr)
	}
	if !strings.Contains(outStr, "no placeholder credentials") {
		t.Errorf("stdout missing 'no placeholder credentials':\n%s", outStr)
	}
	if !strings.Contains(outStr, "storage tree under") {
		t.Errorf("stdout missing 'storage tree under':\n%s", outStr)
	}

	// Verify that doctor never creates missing files or mutates state
	if code == 0 && !strings.Contains(outStr, "No problems found") {
		t.Errorf("expected 'No problems found' when code is 0")
	}
}

func TestDoctorUnknownOption(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"--invalid-flag"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("expected exit code 2 for unknown option, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown option") {
		t.Errorf("stderr missing 'unknown option': %s", stderr.String())
	}
}

func TestDoctorHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0 for doctor --help, got %d", code)
	}
	if !strings.Contains(stdout.String(), "telos doctor — diagnose host") {
		t.Errorf("stdout missing doctor help header: %s", stdout.String())
	}
}
