package main

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type PreflightStatus int

const (
	StatusOK PreflightStatus = iota
	StatusWarn
	StatusFail
)

type PreflightCheckResult struct {
	Status  PreflightStatus
	Message string
	Hint    string
}

type PreflightResult struct {
	Distro   string
	Failures int
	Warnings int
	Hints    []string
	Results  []PreflightCheckResult
}

func checkContainerRuntime() PreflightCheckResult {
	if podmanPath, err := exec.LookPath("podman"); err == nil && podmanPath != "" {
		out, err := exec.Command("podman", "--version").Output()
		version := "unknown"
		if err == nil {
			fields := strings.Fields(string(out))
			if len(fields) >= 3 {
				version = strings.TrimSuffix(fields[2], ",")
			}
		}
		return PreflightCheckResult{
			Status:  StatusOK,
			Message: fmt.Sprintf("container runtime: podman %s", version),
		}
	}

	if dockerPath, err := exec.LookPath("docker"); err == nil && dockerPath != "" {
		out, err := exec.Command("docker", "--version").Output()
		version := "unknown"
		if err == nil {
			fields := strings.Fields(string(out))
			if len(fields) >= 3 {
				version = strings.TrimSuffix(fields[2], ",")
			}
		}
		return PreflightCheckResult{
			Status:  StatusOK,
			Message: fmt.Sprintf("container runtime: docker %s", version),
		}
	}

	return PreflightCheckResult{
		Status:  StatusFail,
		Message: "no container runtime (need podman or docker)",
		Hint:    installHint("podman"),
	}
}

func checkCompose() PreflightCheckResult {
	if composePath, err := exec.LookPath("podman-compose"); err == nil && composePath != "" {
		return PreflightCheckResult{
			Status:  StatusOK,
			Message: "compose driver: podman-compose",
		}
	}

	if dockerPath, err := exec.LookPath("docker"); err == nil && dockerPath != "" {
		if err := exec.Command("docker", "compose", "version").Run(); err == nil {
			return PreflightCheckResult{
				Status:  StatusOK,
				Message: "compose driver: docker compose",
			}
		}
	}

	if composePath, err := exec.LookPath("docker-compose"); err == nil && composePath != "" {
		return PreflightCheckResult{
			Status:  StatusOK,
			Message: "compose driver: docker-compose",
		}
	}

	return PreflightCheckResult{
		Status:  StatusFail,
		Message: "no compose driver",
		Hint:    installHint("podman-compose"),
	}
}

func checkTool(tool, pkg string, required bool) PreflightCheckResult {
	if _, err := exec.LookPath(tool); err == nil {
		return PreflightCheckResult{
			Status:  StatusOK,
			Message: fmt.Sprintf("tool: %s", tool),
		}
	}

	if required {
		return PreflightCheckResult{
			Status:  StatusFail,
			Message: fmt.Sprintf("missing required tool: %s", tool),
			Hint:    installHint(pkg),
		}
	}

	return PreflightCheckResult{
		Status:  StatusWarn,
		Message: fmt.Sprintf("missing optional tool: %s", tool),
	}
}

// familyFromIDs maps an os-release ID (and its ID_LIKE fallback) onto a package
// family. Split out from the Linux probe and kept platform-independent so the
// distro table — the most error-prone part of preflight, and the part that
// decides which command an operator is told to run — is testable everywhere
// rather than only on the distro the test happens to run on.
func familyFromIDs(id, idLike string) string {
	switch id {
	case "arch", "manjaro", "endeavouros", "cachyos":
		return "arch"
	case "debian", "ubuntu", "linuxmint", "pop", "raspbian":
		return "debian"
	case "fedora", "rhel", "centos", "rocky", "almalinux":
		return "fedora"
	}
	if strings.HasPrefix(id, "opensuse") || id == "sles" {
		return "suse"
	}
	switch {
	case strings.Contains(idLike, "arch"):
		return "arch"
	case strings.Contains(idLike, "debian"), strings.Contains(idLike, "ubuntu"):
		return "debian"
	case strings.Contains(idLike, "fedora"), strings.Contains(idLike, "rhel"):
		return "fedora"
	case strings.Contains(idLike, "suse"):
		return "suse"
	}
	return "unknown"
}

// installHintFor is the pure family-to-command mapping. It must stay identical
// to install_hint() in cli/lib/preflight.sh: the two CLIs are expected to give
// an operator the same remediation for the same host.
func installHintFor(family, pkg string) string {
	switch family {
	case "arch":
		return fmt.Sprintf("sudo pacman -S --needed %s", pkg)
	case "debian":
		return fmt.Sprintf("sudo apt install %s", pkg)
	case "fedora":
		return fmt.Sprintf("sudo dnf install %s", pkg)
	case "suse":
		return fmt.Sprintf("sudo zypper install %s", pkg)
	default:
		return fmt.Sprintf("install '%s' with your distribution's package manager", pkg)
	}
}

func installHint(pkg string) string {
	return installHintFor(distroFamily(), pkg)
}

func runPreflight(out io.Writer) *PreflightResult {
	res := &PreflightResult{
		Distro:  detectDistro(),
		Results: make([]PreflightCheckResult, 0),
	}

	addCheck := func(c PreflightCheckResult) {
		res.Results = append(res.Results, c)
		switch c.Status {
		case StatusOK:
			// ok
		case StatusWarn:
			res.Warnings++
			if c.Hint != "" {
				res.Hints = append(res.Hints, c.Hint)
			}
		case StatusFail:
			res.Failures++
			if c.Hint != "" {
				res.Hints = append(res.Hints, c.Hint)
			}
		}
	}

	addCheck(checkContainerRuntime())
	addCheck(checkCompose())

	for _, c := range runPlatformPreflightChecks() {
		addCheck(c)
	}

	addCheck(checkTool("openssl", "openssl", true))
	addCheck(checkTool("python3", "python", true))
	addCheck(checkTool("curl", "curl", false))

	if out != nil {
		printPreflightReport(out, res)
	}

	return res
}

func printPreflightReport(out io.Writer, res *PreflightResult) {
	fmt.Fprintf(out, "Host prerequisites  (%s)\n\n", res.Distro)

	for _, c := range res.Results {
		var icon string
		switch c.Status {
		case StatusOK:
			icon = "✔"
		case StatusWarn:
			icon = "!"
		case StatusFail:
			icon = "✘"
		}
		fmt.Fprintf(out, "  %s  %s\n", icon, c.Message)
	}

	fmt.Fprintln(out, "")
	if res.Failures > 0 {
		fmt.Fprintf(out, "%d prerequisite(s) missing. Run these, then try again:\n\n", res.Failures)
		for _, h := range res.Hints {
			fmt.Fprintf(out, "    %s\n", h)
		}
		fmt.Fprintln(out, "")
	} else if res.Warnings > 0 && len(res.Hints) > 0 {
		fmt.Fprintln(out, "Notes:")
		for _, h := range res.Hints {
			fmt.Fprintf(out, "    %s\n", h)
		}
		fmt.Fprintln(out, "")
	}
}
