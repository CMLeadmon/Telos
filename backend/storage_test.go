//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func newStorage(t *testing.T) (*ConfinedStorage, string) {
	t.Helper()
	root := t.TempDir()
	stage := filepath.Join(root, "staging")
	shared := filepath.Join(root, "shared")
	os.MkdirAll(stage, 0o700)
	os.MkdirAll(shared, 0o700)
	s, err := OpenConfinedStorage(map[StorageArea]string{
		"staging": stage,
		"shared":  shared,
	})
	if err != nil {
		t.Skipf("confined storage unavailable (openat2): %v", err)
	}
	t.Cleanup(s.Close)
	return s, root
}

func writeStaged(t *testing.T, s *ConfinedStorage, key, content string) string {
	t.Helper()
	f, err := s.CreateExclusive("staging", key, 0o600)
	if err != nil {
		t.Fatalf("create %s: %v", key, err)
	}
	io.WriteString(f, content)
	f.Sync()
	f.Close()
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestConfinedStorageRejectsTraversalAndSymlinks(t *testing.T) {
	s, root := newStorage(t)

	// Traversal keys are rejected before any syscall.
	for _, bad := range []string{"../escape", "a/../../escape", "/abs", "a//b", "a/"} {
		if _, err := s.CreateExclusive("staging", bad, 0o600); err == nil {
			t.Errorf("traversal key %q was accepted", bad)
		}
	}

	// A symlink inside the root pointing outside must not be followed.
	outside := filepath.Join(root, "outside.txt")
	os.WriteFile(outside, []byte("secret"), 0o600)
	linkDir := filepath.Join(root, "staging", "linkdir")
	os.MkdirAll(linkDir, 0o700)
	os.Symlink(outside, filepath.Join(linkDir, "link"))
	if _, err := s.OpenRead("staging", "linkdir/link"); err == nil {
		t.Error("symlink to an outside file was followed")
	}
}

func TestPromoteNoReplace(t *testing.T) {
	s, _ := newStorage(t)
	sum := writeStaged(t, s, "up1", "hello world")

	// Successful promotion.
	res, err := s.PromoteNoReplace(context.Background(),
		StorageRef{"staging", "up1"}, StorageRef{"shared", "final/doc"}, sum)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if res.SHA256 != sum {
		t.Fatalf("promoted hash = %s, want %s", res.SHA256, sum)
	}
	// Source is gone; destination is readable.
	if _, err := s.OpenRead("staging", "up1"); err == nil {
		t.Error("source survived promotion")
	}
	if _, err := s.OpenRead("shared", "final/doc"); err != nil {
		t.Errorf("destination not readable: %v", err)
	}

	// No-replace: a second promotion to the same destination fails and
	// preserves the original.
	sum2 := writeStaged(t, s, "up2", "different content")
	if _, err := s.PromoteNoReplace(context.Background(),
		StorageRef{"staging", "up2"}, StorageRef{"shared", "final/doc"}, sum2); err == nil {
		t.Fatal("no-replace promotion overwrote an existing destination")
	}
	// Original destination content is intact.
	f, _ := s.OpenRead("shared", "final/doc")
	data, _ := io.ReadAll(f)
	f.Close()
	if string(data) != "hello world" {
		t.Fatalf("destination was modified: %q", data)
	}
}

func TestPromoteHashMismatch(t *testing.T) {
	s, _ := newStorage(t)
	writeStaged(t, s, "up3", "actual content")
	if _, err := s.PromoteNoReplace(context.Background(),
		StorageRef{"staging", "up3"}, StorageRef{"shared", "x"}, "0000deadbeef"); err == nil {
		t.Fatal("promotion accepted a hash mismatch")
	}
	// The bad source must not have been promoted.
	if _, err := s.OpenRead("shared", "x"); err == nil {
		t.Error("mismatched source was promoted")
	}
}

func TestCreateExclusiveCollision(t *testing.T) {
	s, _ := newStorage(t)
	writeStaged(t, s, "dup", "one")
	if _, err := s.CreateExclusive("staging", "dup", 0o600); err == nil {
		t.Fatal("CreateExclusive overwrote an existing file")
	}
}
