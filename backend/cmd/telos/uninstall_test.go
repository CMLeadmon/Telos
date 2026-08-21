package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A tree that looks like an installed node: the application directory, the
// executable inside it, and a .env holding the node's credentials.
func stageInstall(t *testing.T) (appDir string, exe string) {
	t.Helper()
	appDir = filepath.Join(t.TempDir(), "telos")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe = filepath.Join(appDir, "telos")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return appDir, exe
}

func writeEnv(t *testing.T, appDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("POSTGRES_PASSWORD=live\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func alwaysConfirm(bool) func(string) bool {
	return func(string) bool { return true }
}

// The uninstaller removes what the installer added. A symlink someone else put
// on the PATH under a similar name is not ours to delete.
func TestRemovableEntriesTakesOnlyOurOwnLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privilege on Windows")
	}
	appDir, exe := stageInstall(t)
	binDir := t.TempDir()

	ours := filepath.Join(binDir, "telos")
	if err := os.Symlink(exe, ours); err != nil {
		t.Fatal(err)
	}
	someoneElses := filepath.Join(binDir, "telos-other")
	if err := os.Symlink(filepath.Join(binDir, "unrelated-binary"), someoneElses); err != nil {
		t.Fatal(err)
	}
	// A real file that merely shares the name. Never ours: install.sh links.
	impostor := filepath.Join(binDir, "telos-copy")
	if err := os.WriteFile(impostor, []byte("not ours"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := removableEntries([]string{ours, someoneElses, impostor, filepath.Join(binDir, "absent")}, "telos", appDir)

	if len(got) != 1 || got[0] != ours {
		t.Fatalf("removableEntries = %v, want only %q", got, ours)
	}
}

// Windows has no unprivileged symlinks, so the installed thing is the real
// executable sitting in the install directory.
func TestRemovableEntriesTakesTheRealExecutableInTheInstallDirectory(t *testing.T) {
	appDir, exe := stageInstall(t)

	got := removableEntries([]string{exe}, "telos", appDir)

	if len(got) != 1 || got[0] != exe {
		t.Fatalf("removableEntries = %v, want %q", got, exe)
	}
}

func TestUninstallRemovesTheLinkAndLeavesTheTreeWithoutPurge(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privilege on Windows")
	}
	appDir, exe := stageInstall(t)
	link := filepath.Join(t.TempDir(), "telos")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}

	err := runUninstallPlan(uninstallOptions{
		appDir:      appDir,
		candidates:  []string{link},
		assumeYes:   true,
		interactive: true,
		confirm:     alwaysConfirm(true),
	}, io.Discard)
	if err != nil {
		t.Fatalf("runUninstallPlan: %v", err)
	}

	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("the symlink survived")
	}
	if _, err := os.Stat(appDir); err != nil {
		t.Error("the application tree was removed without --purge")
	}
}

// Losing .env desynchronizes the node's credentials from the Postgres and
// MariaDB volumes that were encrypted against them, and there is no way back.
// --yes covers the ordinary questions; it deliberately does not cover this one.
func TestUninstallPurgeAsksAboutEnvEvenUnderAssumeYes(t *testing.T) {
	appDir, _ := stageInstall(t)
	writeEnv(t, appDir)

	var prompts []string
	err := runUninstallPlan(uninstallOptions{
		appDir:      appDir,
		purge:       true,
		assumeYes:   true,
		interactive: true,
		confirm: func(prompt string) bool {
			prompts = append(prompts, prompt)
			return false
		},
	}, io.Discard)
	if err != nil {
		t.Fatalf("declining should not be an error: %v", err)
	}

	if len(prompts) != 1 {
		t.Fatalf("prompts = %v, want exactly the .env question", prompts)
	}
	if !strings.Contains(strings.ToLower(prompts[0]), ".env") {
		t.Errorf("the prompt did not mention .env: %q", prompts[0])
	}
	if _, err := os.Stat(appDir); err != nil {
		t.Error("the tree was deleted after the member declined")
	}
}

// `read` on a closed stdin returns immediately, and the empty answer used to
// fall through to "Kept", exiting 0 — so a scripted --purge --yes reported
// success while doing nothing at all. Fail loudly instead.
func TestUninstallPurgeRefusesToDeleteEnvWithNoTerminal(t *testing.T) {
	appDir, _ := stageInstall(t)
	writeEnv(t, appDir)

	err := runUninstallPlan(uninstallOptions{
		appDir:      appDir,
		purge:       true,
		assumeYes:   true,
		interactive: false,
	}, io.Discard)

	if !errors.Is(err, errNoTerminal) {
		t.Fatalf("error = %v, want errNoTerminal", err)
	}
	if _, err := os.Stat(appDir); err != nil {
		t.Error("the tree was deleted despite the refusal")
	}
}

func TestUninstallPurgeRemovesTheTreeOnceConfirmed(t *testing.T) {
	appDir, _ := stageInstall(t)
	writeEnv(t, appDir)

	err := runUninstallPlan(uninstallOptions{
		appDir:      appDir,
		purge:       true,
		assumeYes:   true,
		interactive: true,
		confirm:     alwaysConfirm(true),
	}, io.Discard)
	if err != nil {
		t.Fatalf("runUninstallPlan: %v", err)
	}

	if _, err := os.Stat(appDir); !os.IsNotExist(err) {
		t.Fatal("the application tree survived a confirmed purge")
	}
}

// Nothing to lose, so nothing to ask about.
func TestUninstallPurgeWithoutEnvAsksNothingUnderAssumeYes(t *testing.T) {
	appDir, _ := stageInstall(t)

	asked := false
	err := runUninstallPlan(uninstallOptions{
		appDir:      appDir,
		purge:       true,
		assumeYes:   true,
		interactive: true,
		confirm:     func(string) bool { asked = true; return true },
	}, io.Discard)
	if err != nil {
		t.Fatalf("runUninstallPlan: %v", err)
	}

	if asked {
		t.Error("asked about a .env that does not exist")
	}
	if _, err := os.Stat(appDir); !os.IsNotExist(err) {
		t.Error("the tree survived")
	}
}

func TestUninstallAbortsWhenTheMemberDeclines(t *testing.T) {
	appDir, _ := stageInstall(t)

	err := runUninstallPlan(uninstallOptions{
		appDir:      appDir,
		purge:       true,
		interactive: true,
		confirm:     func(string) bool { return false },
	}, io.Discard)

	if !errors.Is(err, errUninstallAborted) {
		t.Fatalf("error = %v, want errUninstallAborted", err)
	}
	if _, err := os.Stat(appDir); err != nil {
		t.Error("the tree was removed after an abort")
	}
}

// Storage and container volumes are node data, not installed files. Destroying
// them is a separate, explicit act — `telos stop --volumes` and deleting
// STORAGE_PATH by hand.
func TestUninstallNeverTouchesNodeData(t *testing.T) {
	appDir, _ := stageInstall(t)
	storage := t.TempDir()
	if err := os.WriteFile(filepath.Join(storage, "book.epub"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := &strings.Builder{}
	err := runUninstallPlan(uninstallOptions{
		appDir:      appDir,
		purge:       true,
		assumeYes:   true,
		interactive: true,
		confirm:     alwaysConfirm(true),
	}, out)
	if err != nil {
		t.Fatalf("runUninstallPlan: %v", err)
	}

	if _, err := os.Stat(filepath.Join(storage, "book.epub")); err != nil {
		t.Fatal("storage was touched by an uninstall")
	}
	// And it has to say so, or the operator assumes the volumes went with it.
	if !strings.Contains(out.String(), "volume") {
		t.Errorf("the report never mentions what was left behind:\n%s", out.String())
	}
}
