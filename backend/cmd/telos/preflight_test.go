package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestInstallHint(t *testing.T) {
	tests := []struct {
		pkg      string
		expected string
	}{
		{"podman", "podman"},
		{"fuse-overlayfs", "fuse-overlayfs"},
	}

	for _, tt := range tests {
		hint := installHint(tt.pkg)
		if !strings.Contains(hint, tt.pkg) {
			t.Errorf("installHint(%q) = %q, expected substring %q", tt.pkg, hint, tt.pkg)
		}
	}
}

func TestCheckToolRequiredVsOptional(t *testing.T) {
	// Non-existent command test
	fakeTool := "non_existent_tool_xyz_12345"

	reqRes := checkTool(fakeTool, "fake-pkg", true)
	if reqRes.Status != StatusFail {
		t.Errorf("checkTool required missing tool status = %v, expected StatusFail (%v)", reqRes.Status, StatusFail)
	}
	if !strings.Contains(reqRes.Message, fakeTool) {
		t.Errorf("checkTool required missing tool message = %q, expected containing %q", reqRes.Message, fakeTool)
	}
	if reqRes.Hint == "" {
		t.Errorf("checkTool required missing tool hint should not be empty")
	}

	optRes := checkTool(fakeTool, "fake-pkg", false)
	if optRes.Status != StatusWarn {
		t.Errorf("checkTool optional missing tool status = %v, expected StatusWarn (%v)", optRes.Status, StatusWarn)
	}
	if !strings.Contains(optRes.Message, fakeTool) {
		t.Errorf("checkTool optional missing tool message = %q, expected containing %q", optRes.Message, fakeTool)
	}
}

func TestRunPreflightExecution(t *testing.T) {
	var buf bytes.Buffer
	res := runPreflight(&buf)

	if res == nil {
		t.Fatal("runPreflight returned nil result")
	}

	if res.Distro == "" {
		t.Errorf("runPreflight returned empty distro string")
	}

	if len(res.Results) == 0 {
		t.Errorf("runPreflight returned no check results")
	}

	outStr := buf.String()
	if !strings.Contains(outStr, "Host prerequisites") {
		t.Errorf("report output missing header: %s", outStr)
	}

	// Verify count accounting logic matches actual checks
	failuresCount := 0
	warningsCount := 0
	for _, c := range res.Results {
		if c.Status == StatusFail {
			failuresCount++
		} else if c.Status == StatusWarn {
			warningsCount++
		}
	}

	if res.Failures != failuresCount {
		t.Errorf("res.Failures = %d, counted %d", res.Failures, failuresCount)
	}
	if res.Warnings != warningsCount {
		t.Errorf("res.Warnings = %d, counted %d", res.Warnings, warningsCount)
	}
}

// The distro table decides which command an operator is told to run when a
// prerequisite is missing. It must stay identical to distro_family() and
// install_hint() in cli/lib/preflight.sh — the two CLIs giving different
// remediation for the same host would be worse than either giving none.
func TestFamilyFromIDs(t *testing.T) {
	cases := []struct{ id, idLike, want string }{
		{"arch", "", "arch"},
		{"manjaro", "", "arch"},
		{"endeavouros", "", "arch"},
		{"cachyos", "", "arch"},
		{"debian", "", "debian"},
		{"ubuntu", "", "debian"},
		{"linuxmint", "", "debian"},
		{"pop", "", "debian"},
		{"raspbian", "", "debian"},
		{"fedora", "", "fedora"},
		{"rhel", "", "fedora"},
		{"centos", "", "fedora"},
		{"rocky", "", "fedora"},
		{"almalinux", "", "fedora"},
		{"opensuse-tumbleweed", "", "suse"},
		{"opensuse-leap", "", "suse"},
		{"sles", "", "suse"},
		// Unknown derivatives fall back to ID_LIKE.
		{"garuda", "arch", "arch"},
		{"elementary", "ubuntu debian", "debian"},
		{"nobara", "fedora", "fedora"},
		{"tumbleweed-slowroll", "suse opensuse", "suse"},
		// Nothing recognizable at all.
		{"plan9", "", "unknown"},
		{"", "", "unknown"},
	}
	for _, c := range cases {
		if got := familyFromIDs(c.id, c.idLike); got != c.want {
			t.Errorf("familyFromIDs(%q, %q) = %q, want %q", c.id, c.idLike, got, c.want)
		}
	}
}

func TestInstallHintForMatchesShellParity(t *testing.T) {
	cases := []struct{ family, want string }{
		{"arch", "sudo pacman -S --needed podman"},
		{"debian", "sudo apt install podman"},
		{"fedora", "sudo dnf install podman"},
		{"suse", "sudo zypper install podman"},
		{"unknown", "install 'podman' with your distribution's package manager"},
	}
	for _, c := range cases {
		if got := installHintFor(c.family, "podman"); got != c.want {
			t.Errorf("installHintFor(%q) = %q, want %q", c.family, got, c.want)
		}
	}
}
