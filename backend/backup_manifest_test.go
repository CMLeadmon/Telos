package main

import (
	"path/filepath"
	"testing"
	"time"
)

func completeManifest() *BackupManifest {
	m := &BackupManifest{
		FormatVersion:     BackupManifestVersion,
		Release:           "v1.0.0",
		SchemaVersion:     11,
		MigrationChecksum: "abc123",
		CreatedAt:         time.Now().UTC(),
		Tools:             map[string]string{"restic": "0.17.0"},
	}
	for _, c := range requiredComponents {
		m.Components = append(m.Components, ComponentDigest{Name: c, SHA256: "deadbeef", Size: 1})
	}
	return m
}

func TestBackupManifestValidate(t *testing.T) {
	if err := completeManifest().Validate(); err != nil {
		t.Fatalf("complete manifest invalid: %v", err)
	}

	// Missing a required component fails.
	m := completeManifest()
	m.Components = m.Components[:len(m.Components)-1]
	if err := m.Validate(); err == nil {
		t.Fatal("incomplete manifest passed validation")
	}

	// A component without a hash fails.
	m = completeManifest()
	m.Components[0].SHA256 = ""
	if err := m.Validate(); err == nil {
		t.Fatal("unhashed component passed validation")
	}

	// Missing identity fails.
	m = completeManifest()
	m.MigrationChecksum = ""
	if err := m.Validate(); err == nil {
		t.Fatal("manifest without checksum set passed validation")
	}
}

func TestBackupManifestCompatibility(t *testing.T) {
	m := completeManifest() // schema 11, checksum abc123

	// Same schema + matching checksum: OK.
	if err := m.CompatibleWith(11, "abc123"); err != nil {
		t.Fatalf("matching node rejected: %v", err)
	}
	// Same schema + different checksum: rejected.
	if err := m.CompatibleWith(11, "different"); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	// Backup newer than node: rejected.
	if err := m.CompatibleWith(10, "abc123"); err == nil {
		t.Fatal("future-schema backup accepted")
	}
	// Backup older than node (node has newer migrations): allowed (migrations
	// re-run forward on restore).
	if err := m.CompatibleWith(12, "nodechecksum"); err != nil {
		t.Fatalf("older backup rejected: %v", err)
	}
}

func TestBackupManifestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m := completeManifest()
	if err := m.WriteManifest(path); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("round-tripped manifest invalid: %v", err)
	}
	if got.Release != m.Release || got.SchemaVersion != m.SchemaVersion {
		t.Fatalf("round-trip lost identity: %+v", got)
	}
}
