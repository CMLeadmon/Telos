package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func findRepoRoot() (string, bool) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

func TestDetectContainerRuntimeEnvOverride(t *testing.T) {
	t.Setenv("TELOS_RUNTIME", "podman-test")
	runtime, err := detectContainerRuntime()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if runtime != "podman-test" {
		t.Fatalf("expected podman-test, got %s", runtime)
	}
}

func TestDetectComposeDriverEnvOverride(t *testing.T) {
	t.Setenv("TELOS_COMPOSE_DRIVER", "docker-compose-test")
	driver, err := detectComposeDriver()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if driver != "docker-compose-test" {
		t.Fatalf("expected docker-compose-test, got %s", driver)
	}
}

func TestBuildComposeArgs(t *testing.T) {
	engine := &ComposeEngine{
		Runtime:       "docker",
		ComposeDriver: "docker compose",
		Binary:        "docker",
		Args:          []string{"compose"},
	}

	bin, args := buildComposeArgs(engine, "/tmp/repo", false, "up", "-d")
	if bin != "docker" {
		t.Fatalf("expected binary docker, got %s", bin)
	}
	expectedArgs := []string{"compose", "-f", filepath.Join("/tmp/repo", "docker-compose.yml"), "up", "-d"}
	if !slices.Equal(args, expectedArgs) {
		t.Fatalf("args mismatch.\nGot:  %v\nWant: %v", args, expectedArgs)
	}

	// With dev mode
	_, devArgs := buildComposeArgs(engine, "/tmp/repo", true, "up", "-d")
	expectedDevArgs := []string{
		"compose",
		"-f", filepath.Join("/tmp/repo", "docker-compose.yml"),
		"-f", filepath.Join("/tmp/repo", "docker-compose.dev.yml"),
		"up", "-d",
	}
	if !slices.Equal(devArgs, expectedDevArgs) {
		t.Fatalf("dev args mismatch.\nGot:  %v\nWant: %v", devArgs, expectedDevArgs)
	}
}

// TestExpectedContainers_ProfileFiltering is the REQUIRED regression test.
// It verifies that profile-gated services like telos-audiobook-migrate (which specifies
// profiles: ["migration"]) are excluded on a default boot, but included when the profile is active.
func TestExpectedContainers_ProfileFiltering(t *testing.T) {
	sampleYAML := `
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

	// 1. Default Boot on YAML content (no active profiles)
	defaultContainers, err := expectedContainersFromYAML([]byte(sampleYAML), nil)
	if err != nil {
		t.Fatalf("expectedContainersFromYAML failed for default boot: %v", err)
	}

	if slices.Contains(defaultContainers, "telos-audiobook-migrate") {
		t.Fatalf("BUG REGRESSION: telos-audiobook-migrate should NOT be in expected containers for default boot, but was found in %v", defaultContainers)
	}

	requiredDefault := []string{"telos-migrate", "telos-core", "telos-postgres", "telos-redis", "telos-traefik"}
	for _, req := range requiredDefault {
		if !slices.Contains(defaultContainers, req) {
			t.Fatalf("expected container %s missing from default boot: %v", req, defaultContainers)
		}
	}

	// 2. Migration Profile Active on YAML content
	migrationContainers, err := expectedContainersFromYAML([]byte(sampleYAML), []string{"migration"})
	if err != nil {
		t.Fatalf("expectedContainersFromYAML failed for migration profile: %v", err)
	}

	if !slices.Contains(migrationContainers, "telos-audiobook-migrate") {
		t.Fatalf("telos-audiobook-migrate MUST be included when 'migration' profile is active, but was missing from %v", migrationContainers)
	}

	// 3. If repo root docker-compose.yml is available on disk, test against actual repo file too
	if root, ok := findRepoRoot(); ok {
		composePath := filepath.Join(root, "docker-compose.yml")
		actualDefault, err := expectedContainersFromFile(composePath, nil)
		if err != nil {
			t.Fatalf("expectedContainersFromFile failed for repo compose file: %v", err)
		}
		if slices.Contains(actualDefault, "telos-audiobook-migrate") {
			t.Fatalf("BUG REGRESSION: telos-audiobook-migrate in repo docker-compose.yml should NOT be in expected containers for default boot")
		}

		actualMigration, err := expectedContainersFromFile(composePath, []string{"migration"})
		if err != nil {
			t.Fatalf("expectedContainersFromFile failed for repo compose file with profile: %v", err)
		}
		if !slices.Contains(actualMigration, "telos-audiobook-migrate") {
			t.Fatalf("telos-audiobook-migrate in repo docker-compose.yml MUST be included when 'migration' profile is active")
		}
	}
}

func TestIsOneshot(t *testing.T) {
	if !isOneshot("telos-migrate") {
		t.Fatalf("expected telos-migrate to be oneshot")
	}
	if isOneshot("telos-core") {
		t.Fatalf("telos-core should not be oneshot")
	}
	if isOneshot("telos-postgres") {
		t.Fatalf("telos-postgres should not be oneshot")
	}
}
