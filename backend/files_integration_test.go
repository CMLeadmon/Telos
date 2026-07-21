package main

import (
	"context"
	"testing"
)

func seedFile(t *testing.T, uploader, name, purpose, visibility, state, scan, key string) string {
	t.Helper()
	var id string
	err := dbPool.QueryRow(context.Background(), `
		INSERT INTO files (filename, sha256, uploader_id, scan_status, storage_key, size_bytes, mime_type, purpose, visibility, state)
		VALUES ($1,'h',$2,$3,$4,1,'application/octet-stream',$5,$6,$7) RETURNING id::text
	`, name, nullableUUID(uploader), scan, key, purpose, visibility, state).Scan(&id)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	return id
}

func nullableUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestFileStorePurposeFiltering(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	store := newFileStore(f.DB)

	var alice, bob string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('alice','x') RETURNING id`).Scan(&alice)
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('bob','x') RETURNING id`).Scan(&bob)

	clean := seedFile(t, alice, "doc.pdf", "shared", "community", "available", "clean", "k/clean")
	infected := seedFile(t, alice, "virus.exe", "shared", "community", "quarantined", "infected", "k/virus")
	avatar := seedFile(t, alice, "a.png", "avatar", "private", "available", "clean", "k/avatar")
	staged := seedFile(t, alice, "wip.pdf", "shared", "community", "staged", "pending", "k/wip")
	bobPriv := seedFile(t, bob, "secret.pdf", "shared", "private", "available", "clean", "k/secret")

	// FindAvailable returns only the clean community shared file.
	if _, err := store.FindAvailable(ctx, clean, PurposeShared, alice); err != nil {
		t.Fatalf("clean shared file not found: %v", err)
	}
	for _, id := range []string{infected, staged} {
		if _, err := store.FindAvailable(ctx, id, PurposeShared, alice); err == nil {
			t.Fatalf("leaked non-available file %s", id)
		}
	}
	// Avatar is not returned under the shared purpose.
	if _, err := store.FindAvailable(ctx, avatar, PurposeShared, alice); err == nil {
		t.Fatal("avatar leaked as a shared file")
	}
	// Another user's private shared file is not visible to alice.
	if _, err := store.FindAvailable(ctx, bobPriv, PurposeShared, alice); err == nil {
		t.Fatal("bob's private file leaked to alice")
	}
	// But it is visible to bob.
	if _, err := store.FindAvailable(ctx, bobPriv, PurposeShared, bob); err != nil {
		t.Fatalf("bob cannot see his own private file: %v", err)
	}

	// ListVisible for alice: only the clean community file (not infected,
	// avatar, staged, or bob's private).
	list, err := store.ListVisible(ctx, alice, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != clean {
		t.Fatalf("ListVisible returned %d files, want just the clean one", len(list))
	}
}

func TestFileAuditImmutableWrite(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	store := newFileStore(f.DB)

	var uid string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('auditor','x') RETURNING id`).Scan(&uid)
	fid := seedFile(t, uid, "f.pdf", "shared", "community", "available", "clean", "k/audit")

	if err := store.RecordAudit(ctx, uid, fid, "", "download", map[string]any{"bytes": 10}); err != nil {
		t.Fatalf("record audit: %v", err)
	}
	var n int
	f.DB.QueryRow(ctx, `SELECT COUNT(*) FROM file_audit WHERE file_id=$1 AND action='download'`, fid).Scan(&n)
	if n != 1 {
		t.Fatalf("audit rows = %d, want 1", n)
	}
	// An invalid action is rejected by the constraint.
	if err := store.RecordAudit(ctx, uid, fid, "", "hack", nil); err == nil {
		t.Fatal("invalid audit action accepted")
	}
}

func TestLogicalFolderSiblingCollision(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	var uid string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('owner','x') RETURNING id`).Scan(&uid)

	_, err := f.DB.Exec(ctx, `INSERT INTO logical_folders (owner_id, normalized_name, display_name) VALUES ($1,'docs','Docs')`, uid)
	if err != nil {
		t.Fatal(err)
	}
	// Same normalized name for the same owner+parent collides.
	_, err = f.DB.Exec(ctx, `INSERT INTO logical_folders (owner_id, normalized_name, display_name) VALUES ($1,'docs','DOCS')`, uid)
	if err == nil {
		t.Fatal("duplicate sibling folder name accepted")
	}
}
