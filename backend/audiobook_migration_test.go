package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrationStageTransitions(t *testing.T) {
	valid := []struct {
		from, to MigrationStage
	}{
		{"", StageInventoried},
		{StageInventoried, StageCopied},
		{StageCopied, StageVerified},
		{StageVerified, StageSwitched},
		{StageSwitched, StageCleaned},
		{StageSwitched, StageRolledBack},
	}
	for _, v := range valid {
		if !CanTransition(v.from, v.to) {
			t.Errorf("expected valid transition from %s to %s", v.from, v.to)
		}
	}

	invalid := []struct {
		from, to MigrationStage
	}{
		{StageInventoried, StageSwitched},
		{StageCleaned, StageInventoried},
		{StageRolledBack, StageSwitched},
	}
	for _, v := range invalid {
		if CanTransition(v.from, v.to) {
			t.Errorf("expected invalid transition from %s to %s", v.from, v.to)
		}
	}
}

func TestAudiobookMigrationManifestValidationAndIO(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.json")

	m := AudiobookMigrationManifest{
		SchemaVersion:      1,
		CreatedAt:          time.Now(),
		JellyfinLibraryIDs: []string{"lib-audiobooks"},
		Stage:              StageInventoried,
		Items: []AudiobookMigrationItem{
			{
				CatalogID:      "cat-1",
				JellyfinID:     "jf-1",
				Stage:          StageInventoried,
				TotalSizeBytes: 1024,
				DurationMS:     60000,
				Files: []AudiobookMigrationFile{
					{
						RelativeSource: "audiobooks/book1.m4b",
						SizeBytes:      1024,
						SHA256:         "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
					},
				},
			},
		},
	}

	if err := WriteAudiobookManifest(manifestPath, m); err != nil {
		t.Fatalf("WriteAudiobookManifest error: %v", err)
	}

	readM, err := ReadAudiobookManifest(manifestPath)
	if err != nil {
		t.Fatalf("ReadAudiobookManifest error: %v", err)
	}

	if readM.SchemaVersion != 1 || len(readM.Items) != 1 || readM.Items[0].CatalogID != "cat-1" {
		t.Fatalf("read manifest mismatch: %+v", readM)
	}

	if readM.ManifestSHA256 == "" {
		t.Fatal("expected non-empty ManifestSHA256")
	}
}

func TestAudiobookMigrateCommandUnimplemented(t *testing.T) {
	subcmds := []string{"inventory", "copy", "verify", "switch", "rollback", "cleanup"}
	for _, subcmd := range subcmds {
		t.Run(subcmd, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestAudiobookMigrateHelperProcess")
			cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "TEST_AUDIOBOOK_SUBCMD="+subcmd)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("expected subcmd %s to fail with non-zero exit code, but it succeeded", subcmd)
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("expected exec.ExitError for subcmd %s, got %v", subcmd, err)
			}
			if strings.Contains(stdout.String(), "completed") {
				t.Fatalf("stdout contained success message for subcmd %s: %s", subcmd, stdout.String())
			}
			if !strings.Contains(stderr.String(), "not implemented") {
				t.Fatalf("stderr did not report 'not implemented' for subcmd %s: %s", subcmd, stderr.String())
			}
		})
	}
}

func TestAudiobookMigrateHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	subcmd := os.Getenv("TEST_AUDIOBOOK_SUBCMD")
	runAudiobookMigrateCommand([]string{subcmd})
}
