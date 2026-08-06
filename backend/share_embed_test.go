package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Files browser IDs entries as base64url(relative path) — it walks the
// filesystem — while the files table is keyed by UUID. handleDownloadFile
// accepts both namespaces; embeds must too, or every file the browser lists is
// unshareable.

func fileEmbedSnapshot(t *testing.T, ref string) map[string]string {
	t.Helper()
	canonical, raw, err := buildEmbedSnapshot(context.Background(), "file", ref)
	if err != nil {
		t.Fatalf("buildEmbedSnapshot(file, %q): %v", ref, err)
	}
	// Files are not catalog items, so the ref is echoed rather than
	// canonicalized the way a book or media ref is.
	if canonical != ref {
		t.Errorf("canonical ref = %q, want the ref as passed (%q)", canonical, ref)
	}
	var snap map[string]string
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	return snap
}

func TestBuildEmbedSnapshotResolvesFilesystemFile(t *testing.T) {
	old := mediaRoot
	mediaRoot = t.TempDir()
	defer func() { mediaRoot = old }()

	if err := os.Mkdir(filepath.Join(mediaRoot, "Pictures"), 0755); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("Pictures", "Joe_2.jpg")
	if err := os.WriteFile(filepath.Join(mediaRoot, rel), make([]byte, 2048), 0644); err != nil {
		t.Fatal(err)
	}

	snap := fileEmbedSnapshot(t, base64.RawURLEncoding.EncodeToString([]byte(rel)))

	if snap["title"] != "Joe_2.jpg" {
		t.Errorf("title = %q, want Joe_2.jpg", snap["title"])
	}
	if snap["kicker"] != "Shared File" {
		t.Errorf("kicker = %q, want Shared File", snap["kicker"])
	}
	// Subtitle carries size and MIME, as the DB path already does.
	if !strings.Contains(snap["subtitle"], "2.0 KB") {
		t.Errorf("subtitle = %q, want it to carry the size", snap["subtitle"])
	}
	if !strings.Contains(snap["subtitle"], "image/jpeg") {
		t.Errorf("subtitle = %q, want it to carry the MIME type", snap["subtitle"])
	}
}

// Anything the filesystem namespace declines falls through to the UUID lookup,
// which is where a bad ref becomes "file not found". These assert the decline
// itself, so the containment rules are covered without a database.
func TestSharedFileMetaFromPathDeclines(t *testing.T) {
	old := mediaRoot
	mediaRoot = t.TempDir()
	defer func() { mediaRoot = old }()

	if err := os.Mkdir(filepath.Join(mediaRoot, "Pictures"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mediaRoot, "Pictures", "Joe.jpg"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	encoded := func(s string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(s))
	}

	cases := []struct {
		name string
		id   string
	}{
		// The ref arrives on the send-message body; resolveMediaPath contains it.
		{"traversal", encoded("../../etc/passwd")},
		{"traversal below a real folder", encoded("Pictures/../../../etc/passwd")},
		// Folders are navigable rows in the browser, never pickable targets.
		{"directory", encoded("Pictures")},
		{"media root", encoded("")},
		{"missing file", encoded("Pictures/nope.jpg")},
		// A UUID decodes as valid base64url; it must reach the DB namespace.
		{"uuid", "a2c8837f-5e09-4f7d-b56a-bd089ebbcecf"},
		{"not base64", "!!!not base64!!!"},
	}
	for _, tc := range cases {
		if _, _, _, ok := sharedFileMetaFromPath(tc.id); ok {
			t.Errorf("%s: expected the filesystem namespace to decline %q", tc.name, tc.id)
		}
	}

	// Sanity: the same helper does accept a real file, so the cases above are
	// declining for the right reason and not because the helper never matches.
	if _, _, _, ok := sharedFileMetaFromPath(encoded("Pictures/Joe.jpg")); !ok {
		t.Error("expected a real file to resolve")
	}
}

func TestBuildEmbedSnapshotRejectsUnknownKind(t *testing.T) {
	if _, _, err := buildEmbedSnapshot(context.Background(), "voice_room", "1"); err == nil {
		t.Fatal("expected an unsupported kind to be rejected")
	}
}

// The SQL half of the file search only sees uploads recorded in the files
// table; the Files browser lists the whole shared volume. Search has to cover
// what the browser shows or the search box lies about what is shareable.
func TestSearchSharedFilesystem(t *testing.T) {
	old := mediaRoot
	mediaRoot = t.TempDir()
	defer func() { mediaRoot = old }()

	mkdir := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(mediaRoot, p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(mediaRoot, p), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	mkdir("Audiobooks/Beyond Good and Evil")
	mkdir(".hidden")
	write("Audiobooks/Beyond Good and Evil/19634-01.mp3")
	write("Audiobooks/Beyond Good and Evil/19634-02.mp3")
	write("Audiobooks/notes.txt")
	write(".hidden/secret.mp3")

	// Nested matches are found; the browser reaches them, so search must too.
	got := searchSharedFilesystem("19634", 15)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(got), got)
	}
	for _, f := range got {
		rel, err := base64.RawURLEncoding.DecodeString(f.ID)
		if err != nil {
			t.Errorf("ID %q is not the browser's base64url namespace", f.ID)
			continue
		}
		if !strings.HasPrefix(string(rel), "Audiobooks/") {
			t.Errorf("decoded ID = %q, want a path under Audiobooks/", rel)
		}
		// The ID must round-trip through the resolver embeds use.
		if _, _, _, ok := sharedFileMetaFromPath(f.ID); !ok {
			t.Errorf("ID %q does not resolve back to a shareable file", f.ID)
		}
	}

	if hits := searchSharedFilesystem("secret", 15); len(hits) != 0 {
		t.Errorf("dotfile subtree should be skipped, got %+v", hits)
	}
	if hits := searchSharedFilesystem("19634", 1); len(hits) != 1 {
		t.Errorf("limit not honoured: got %d, want 1", len(hits))
	}
	if hits := searchSharedFilesystem("Audiobooks", 15); len(hits) != 0 {
		t.Errorf("directories are not shareable results, got %+v", hits)
	}
	if hits := searchSharedFilesystem("", 15); len(hits) != 0 {
		t.Errorf("empty term should match nothing, got %+v", hits)
	}
}
