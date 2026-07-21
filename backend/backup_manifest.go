package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// BackupManifestVersion is the manifest schema version.
const BackupManifestVersion = 1

// ComponentDigest records the hash and size of one backup component.
type ComponentDigest struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// BackupManifest describes a complete backup generation. A backup is only
// successful when every required component is present and hashed.
type BackupManifest struct {
	FormatVersion     int               `json:"formatVersion"`
	Release           string            `json:"release"`
	SchemaVersion     int               `json:"schemaVersion"`
	MigrationChecksum string            `json:"migrationChecksumSet"`
	CreatedAt         time.Time         `json:"createdAt"`
	Components        []ComponentDigest `json:"components"`
	Tools             map[string]string `json:"tools"`
}

// requiredComponents are the components every backup must include.
var requiredComponents = []string{
	"postgres.sql",
	"grimmory-mariadb.sql",
	"service-config.tar",
	"env-material.enc",
	"acme.tar",
	"jellyfin-config.tar",
	"grimmory-config.tar",
	"shared-storage.tar",
	"release-capsule.tar",
}

var errIncompleteBackup = errors.New("backup manifest is missing a required component")

// Validate confirms the manifest carries every required component with a
// non-empty hash, a release/schema version, and a migration checksum set.
func (m *BackupManifest) Validate() error {
	if m.FormatVersion != BackupManifestVersion {
		return fmt.Errorf("unsupported manifest format version %d", m.FormatVersion)
	}
	if m.Release == "" || m.SchemaVersion <= 0 || m.MigrationChecksum == "" {
		return errors.New("manifest is missing release/schema/checksum identity")
	}
	present := map[string]ComponentDigest{}
	for _, c := range m.Components {
		if c.SHA256 == "" {
			return fmt.Errorf("component %q has no hash", c.Name)
		}
		present[c.Name] = c
	}
	for _, want := range requiredComponents {
		if _, ok := present[want]; !ok {
			return fmt.Errorf("%w: %s", errIncompleteBackup, want)
		}
	}
	return nil
}

// CompatibleWith reports whether a candidate backup can be restored onto a node
// running the given release and schema version. A backup from a newer schema
// than the node supports is rejected; the checksum set must match exactly.
func (m *BackupManifest) CompatibleWith(nodeSchemaVersion int, nodeChecksum string) error {
	if m.SchemaVersion > nodeSchemaVersion {
		return fmt.Errorf("backup schema %d is newer than this node (%d)", m.SchemaVersion, nodeSchemaVersion)
	}
	if m.SchemaVersion == nodeSchemaVersion && m.MigrationChecksum != nodeChecksum {
		return errors.New("backup migration checksum set does not match this node")
	}
	return nil
}

// hashFile returns the SHA-256 and size of a file.
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), info.Size(), nil
}

// WriteManifest serializes the manifest deterministically to path.
func (m *BackupManifest) WriteManifest(path string) error {
	sort.Slice(m.Components, func(i, j int) bool { return m.Components[i].Name < m.Components[j].Name })
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ReadManifest parses a manifest file.
func ReadManifest(path string) (*BackupManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m BackupManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
