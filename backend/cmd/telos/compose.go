package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ComposeEngine holds resolved container runtime and compose driver settings.
type ComposeEngine struct {
	Runtime       string   // "podman" or "docker"
	ComposeDriver string   // "podman-compose", "docker compose", "docker-compose"
	Binary        string   // primary executable name
	Args          []string // argument prefix, e.g. ["compose"]
}

// detectContainerRuntime resolves the container runtime binary name ("podman" or "docker").
func detectContainerRuntime() (string, error) {
	if envRuntime := os.Getenv("TELOS_RUNTIME"); envRuntime != "" {
		return envRuntime, nil
	}
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman", nil
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker", nil
	}
	return "", fmt.Errorf("neither podman nor docker found on PATH")
}

// detectComposeDriver resolves the compose command string ("podman-compose", "docker compose", or "docker-compose").
func detectComposeDriver() (string, error) {
	if envDriver := os.Getenv("TELOS_COMPOSE_DRIVER"); envDriver != "" {
		return envDriver, nil
	}
	if _, err := exec.LookPath("podman-compose"); err == nil {
		return "podman-compose", nil
	}
	if _, err := exec.LookPath("docker"); err == nil {
		if err := exec.Command("docker", "compose", "version").Run(); err == nil {
			return "docker compose", nil
		}
	}
	if _, err := exec.LookPath("docker-compose"); err == nil {
		return "docker-compose", nil
	}
	return "", fmt.Errorf("no compose driver found (tried podman-compose, docker compose, docker-compose)")
}

// detectComposeEngine returns a ComposeEngine struct with runtime and compose driver details.
func detectComposeEngine() (*ComposeEngine, error) {
	runtime, err := detectContainerRuntime()
	if err != nil {
		return nil, err
	}
	driver, err := detectComposeDriver()
	if err != nil {
		return nil, err
	}

	engine := &ComposeEngine{
		Runtime:       runtime,
		ComposeDriver: driver,
	}

	switch driver {
	case "docker compose":
		engine.Binary = "docker"
		engine.Args = []string{"compose"}
	default:
		engine.Binary = driver
		engine.Args = []string{}
	}

	return engine, nil
}

// buildComposeArgs returns the binary and argument slice to run compose with appropriate file flags.
func buildComposeArgs(engine *ComposeEngine, repoRoot string, devMode bool, extraArgs ...string) (string, []string) {
	args := append([]string{}, engine.Args...)

	mainCompose := filepath.Join(repoRoot, "docker-compose.yml")
	args = append(args, "-f", mainCompose)

	if devMode || os.Getenv("TELOS_DEV") == "1" {
		devCompose := filepath.Join(repoRoot, "docker-compose.dev.yml")
		args = append(args, "-f", devCompose)
	}

	args = append(args, extraArgs...)
	return engine.Binary, args
}

// YAML structure for compose file parsing.
type rawComposeConfig struct {
	Services map[string]rawComposeService `yaml:"services"`
}

type rawComposeService struct {
	ContainerName string   `yaml:"container_name"`
	Profiles      []string `yaml:"profiles"`
}

// expectedContainersFromYAML parses compose YAML content and returns container names for active profiles.
func expectedContainersFromYAML(data []byte, activeProfiles []string) ([]string, error) {
	var cfg rawComposeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse compose YAML: %w", err)
	}

	if len(activeProfiles) == 0 {
		if envProfiles := os.Getenv("COMPOSE_PROFILES"); envProfiles != "" {
			for _, p := range strings.Split(envProfiles, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					activeProfiles = append(activeProfiles, p)
				}
			}
		}
	}

	profileSet := make(map[string]bool)
	for _, p := range activeProfiles {
		profileSet[p] = true
	}

	var expected []string
	for svcName, svc := range cfg.Services {
		if len(svc.Profiles) > 0 {
			match := false
			for _, p := range svc.Profiles {
				if profileSet[p] {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}

		cName := svc.ContainerName
		if cName == "" {
			cName = svcName
		}
		expected = append(expected, cName)
	}

	return expected, nil
}

// expectedContainersFromFile reads a compose file and returns expected container names.
func expectedContainersFromFile(filePath string, activeProfiles []string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("cannot find %s: %w", filePath, err)
	}
	return expectedContainersFromYAML(data, activeProfiles)
}

// expectedContainers returns the expected container names for a Telos node.
// It attempts to use the compose CLI engine if available; if not or if it fails,
// it falls back to parsing docker-compose.yml directly with profile awareness.
func expectedContainers(repoRoot string, activeProfiles []string) ([]string, error) {
	engine, err := detectComposeEngine()
	if err == nil && engine != nil {
		mainCompose := filepath.Join(repoRoot, "docker-compose.yml")
		cmdArgs := append([]string{}, engine.Args...)
		for _, p := range activeProfiles {
			cmdArgs = append(cmdArgs, "--profile", p)
		}
		cmdArgs = append(cmdArgs, "-f", mainCompose, "config", "--services")

		cmd := exec.Command(engine.Binary, cmdArgs...)
		out, execErr := cmd.Output()
		if execErr == nil {
			services := strings.Fields(string(out))
			if len(services) > 0 {
				return mapServicesToContainers(repoRoot, services)
			}
		}
	}

	composeFile := filepath.Join(repoRoot, "docker-compose.yml")
	return expectedContainersFromFile(composeFile, activeProfiles)
}

func mapServicesToContainers(repoRoot string, services []string) ([]string, error) {
	composeFile := filepath.Join(repoRoot, "docker-compose.yml")
	data, err := os.ReadFile(composeFile)
	if err != nil {
		return services, nil
	}
	var cfg rawComposeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return services, nil
	}

	svcSet := make(map[string]bool)
	for _, s := range services {
		svcSet[s] = true
	}

	var containers []string
	for svcName, svc := range cfg.Services {
		if svcSet[svcName] {
			cName := svc.ContainerName
			if cName == "" {
				cName = svcName
			}
			containers = append(containers, cName)
		}
	}
	return containers, nil
}

// oneshotContainers lists services that run once and exit cleanly (exit 0 is success).
var oneshotContainers = map[string]bool{
	"telos-migrate": true,
}

// isOneshot returns true if the container/service name is a known one-shot service.
func isOneshot(name string) bool {
	return oneshotContainers[name]
}
