package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type MigrationStage string

const (
	StageInventoried MigrationStage = "inventoried"
	StageCopied      MigrationStage = "copied"
	StageImported    MigrationStage = "imported"
	StageVerified    MigrationStage = "verified"
	StageSwitched    MigrationStage = "switched"
	StageCleaned     MigrationStage = "cleaned"
	StageRolledBack  MigrationStage = "rolled_back"
)

type AudiobookMigrationFile struct {
	RelativeSource      string `json:"relativeSource"`
	RelativeDestination string `json:"relativeDestination,omitempty"`
	SizeBytes           int64  `json:"sizeBytes"`
	SHA256              string `json:"sha256"`
}

type AudiobookMigrationItem struct {
	CatalogID      string                   `json:"catalogId"`
	JellyfinID     string                   `json:"jellyfinId"`
	GrimmoryID     string                   `json:"grimmoryId,omitempty"`
	Files          []AudiobookMigrationFile `json:"files"`
	TotalSizeBytes int64                    `json:"totalSizeBytes"`
	FileSetSHA256  string                   `json:"fileSetSha256"`
	DurationMS     int64                    `json:"durationMs"`
	Stage          MigrationStage           `json:"stage"`
	Verification   []string                 `json:"verification"`
}

type AudiobookMigrationManifest struct {
	SchemaVersion      int                      `json:"schemaVersion"`
	CreatedAt          time.Time                `json:"createdAt"`
	JellyfinLibraryIDs []string                 `json:"jellyfinLibraryIds"`
	Items              []AudiobookMigrationItem `json:"items"`
	Stage              MigrationStage           `json:"stage"`
	ManifestSHA256     string                   `json:"manifestSha256"`
	BackupProof        string                   `json:"backupProof,omitempty"`
}

func CanTransition(from, to MigrationStage) bool {
	switch from {
	case "":
		return to == StageInventoried
	case StageInventoried:
		return to == StageCopied
	case StageCopied:
		return to == StageImported || to == StageVerified
	case StageImported:
		return to == StageVerified
	case StageVerified:
		return to == StageSwitched
	case StageSwitched:
		return to == StageCleaned || to == StageRolledBack
	case StageCleaned, StageRolledBack:
		return false
	default:
		return false
	}
}

func ComputeFileSetChecksum(files []AudiobookMigrationFile) string {
	sorted := make([]AudiobookMigrationFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].RelativeSource < sorted[j].RelativeSource
	})
	h := sha256.New()
	for _, f := range sorted {
		_, _ = io.WriteString(h, fmt.Sprintf("%s:%d:%s\n", f.RelativeSource, f.SizeBytes, f.SHA256))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (m *AudiobookMigrationManifest) ComputeManifestChecksum() string {
	cp := *m
	cp.ManifestSHA256 = ""
	data, _ := json.Marshal(cp)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (m *AudiobookMigrationManifest) Seal() error {
	m.ManifestSHA256 = m.ComputeManifestChecksum()
	return nil
}

func (m *AudiobookMigrationManifest) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported manifest schema version: %d", m.SchemaVersion)
	}
	if len(m.JellyfinLibraryIDs) == 0 {
		return errors.New("jellyfinLibraryIds cannot be empty")
	}
	seenCatalog := map[string]bool{}
	seenJellyfin := map[string]bool{}
	for _, item := range m.Items {
		if item.CatalogID == "" || item.JellyfinID == "" {
			return errors.New("catalogId and jellyfinId are required for all items")
		}
		if seenCatalog[item.CatalogID] {
			return fmt.Errorf("duplicate catalogId: %s", item.CatalogID)
		}
		seenCatalog[item.CatalogID] = true
		if seenJellyfin[item.JellyfinID] {
			return fmt.Errorf("duplicate jellyfinId: %s", item.JellyfinID)
		}
		seenJellyfin[item.JellyfinID] = true

		for _, f := range item.Files {
			if strings.HasPrefix(f.RelativeSource, "/") || strings.Contains(f.RelativeSource, "..") {
				return fmt.Errorf("invalid relativeSource path: %s", f.RelativeSource)
			}
		}
	}
	computed := m.ComputeManifestChecksum()
	if m.ManifestSHA256 != "" && m.ManifestSHA256 != computed {
		return errors.New("manifest sha256 mismatch")
	}
	return nil
}

func ReadAudiobookManifest(path string) (AudiobookMigrationManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AudiobookMigrationManifest{}, err
	}
	var m AudiobookMigrationManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return AudiobookMigrationManifest{}, err
	}
	if err := m.Validate(); err != nil {
		return AudiobookMigrationManifest{}, err
	}
	return m, nil
}

func WriteAudiobookManifest(path string, m AudiobookMigrationManifest) error {
	if err := m.Seal(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(dir, "manifest-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		_ = tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	_ = tmpFile.Close()

	return os.Rename(tmpPath, path)
}

func runAudiobookMigrateCommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: telos-core audiobook-migrate <subcommand> [flags]")
		os.Exit(2)
	}
	subcmd := args[0]
	switch subcmd {
	case "inventory", "copy", "verify", "switch", "rollback", "cleanup":
		fmt.Fprintf(os.Stderr, "Audiobook migration mode %s is not implemented.\n", subcmd)
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "Unknown audiobook-migrate subcommand: %s\n", subcmd)
		os.Exit(2)
	}
}
