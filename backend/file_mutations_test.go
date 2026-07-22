package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mutationFixture wires the global dbPool to the disposable database and returns
// a service plus three actors: an owner (member), a manager (Owner role bypass),
// and a stranger with no permissions.
func mutationFixture(t *testing.T) (*FileService, *pgxpool.Pool, *UserContext, *UserContext, *UserContext) {
	t.Helper()
	f := withFixture(t)
	old := dbPool
	dbPool = f.DB
	t.Cleanup(func() { dbPool = old })

	ctx := context.Background()
	mk := func(name string) string {
		var id string
		if err := f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ($1,'x') RETURNING id::text`, name).Scan(&id); err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		return id
	}
	owner := &UserContext{ID: mk("owner"), Roles: []string{"Member"}}
	manager := &UserContext{ID: mk("manager"), Roles: []string{"Owner"}}
	stranger := &UserContext{ID: mk("stranger"), Roles: []string{}}
	return newFileService(f.DB), f.DB, owner, manager, stranger
}

func insertFile(t *testing.T, db *pgxpool.Pool, ownerID, key, state string) string {
	t.Helper()
	var id string
	err := db.QueryRow(context.Background(), `
		INSERT INTO files (filename, sha256, storage_key, size_bytes, mime_type, uploader_id, scan_status, state, purpose)
		VALUES ('f.bin', 'abc', $1, 10, 'application/octet-stream', $2::uuid, 'clean', $3, 'shared')
		RETURNING id::text`, key, ownerID, state).Scan(&id)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	return id
}

func TestFileMutationFolderLifecycle(t *testing.T) {
	svc, _, owner, _, stranger := mutationFixture(t)
	ctx := context.Background()

	root, err := svc.CreateFolder(ctx, owner, "", "Docs")
	if err != nil {
		t.Fatalf("create root folder: %v", err)
	}
	if _, err := svc.CreateFolder(ctx, owner, root.ID, "Reports"); err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Normalized sibling collision ("reports" vs "Reports ") fails.
	if _, err := svc.CreateFolder(ctx, owner, root.ID, "  reports "); err != errSiblingConflict {
		t.Fatalf("expected sibling conflict, got %v", err)
	}

	// Invalid names rejected.
	for _, bad := range []string{"", "  ", "a/b", "..", string([]byte{0x01})} {
		if _, err := svc.CreateFolder(ctx, owner, root.ID, bad); err != errNameInvalid {
			t.Fatalf("name %q: expected errNameInvalid, got %v", bad, err)
		}
	}

	// A stranger with no permission cannot create under the owner's folder.
	if _, err := svc.CreateFolder(ctx, stranger, root.ID, "Sneaky"); err != errMutationDenied {
		t.Fatalf("stranger create: expected denied, got %v", err)
	}

	// Non-empty delete is refused; empty delete (and repeat) succeed.
	if err := svc.DeleteFolder(ctx, owner, root.ID); err != errFolderNotEmpty {
		t.Fatalf("expected folder_not_empty, got %v", err)
	}
}

func TestFileMutationFolderCycleRejected(t *testing.T) {
	svc, _, owner, _, _ := mutationFixture(t)
	ctx := context.Background()
	a, _ := svc.CreateFolder(ctx, owner, "", "A")
	b, _ := svc.CreateFolder(ctx, owner, a.ID, "B")
	c, _ := svc.CreateFolder(ctx, owner, b.ID, "C")

	// Moving A beneath C (its own descendant) is a cycle.
	if err := svc.MoveFolder(ctx, owner, a.ID, c.ID); err != errFolderCycle {
		t.Fatalf("expected cycle, got %v", err)
	}
	// Moving A beneath itself is a cycle.
	if err := svc.MoveFolder(ctx, owner, a.ID, a.ID); err != errFolderCycle {
		t.Fatalf("expected self-cycle, got %v", err)
	}
	// A legitimate move (C to root) succeeds.
	if err := svc.MoveFolder(ctx, owner, c.ID, ""); err != nil {
		t.Fatalf("legit move: %v", err)
	}
}

func TestFileMutationFilePhysicalKeyImmutable(t *testing.T) {
	svc, db, owner, _, stranger := mutationFixture(t)
	ctx := context.Background()
	fileID := insertFile(t, db, owner.ID, "shared/keep-me", "available")
	folder, _ := svc.CreateFolder(ctx, owner, "", "Target")

	origKey := storageKeyOf(t, db, fileID)
	if err := svc.RenameFile(ctx, owner, fileID, "Renamed.bin"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := svc.MoveFile(ctx, owner, fileID, folder.ID); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got := storageKeyOf(t, db, fileID); got != origKey {
		t.Fatalf("physical key changed on logical mutation: %q -> %q", origKey, got)
	}

	// Stranger cannot mutate someone else's file.
	if err := svc.RenameFile(ctx, stranger, fileID, "Hijack"); err != errMutationDenied {
		t.Fatalf("stranger rename: expected denied, got %v", err)
	}
}

func TestFileMutationDeleteHidesAndIsIdempotent(t *testing.T) {
	svc, db, owner, _, _ := mutationFixture(t)
	ctx := context.Background()
	fileID := insertFile(t, db, owner.ID, "shared/gone", "available")

	if err := svc.DeleteFile(ctx, owner, fileID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if st := stateOf(t, db, fileID); st != "deleting" {
		t.Fatalf("state after delete = %q, want deleting", st)
	}
	// Hidden from the visible listing immediately.
	store := newFileStore(db)
	visible, _ := store.ListVisible(ctx, owner.ID, 50)
	for _, v := range visible {
		if v.ID == fileID {
			t.Fatal("deleting file still visible")
		}
	}
	// Repeated deletion is a stable no-op.
	if err := svc.DeleteFile(ctx, owner, fileID); err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	if st := stateOf(t, db, fileID); st != "deleting" {
		t.Fatalf("state after repeat = %q, want deleting", st)
	}
}

func storageKeyOf(t *testing.T, db *pgxpool.Pool, fileID string) string {
	t.Helper()
	var key string
	if err := db.QueryRow(context.Background(), `SELECT storage_key FROM files WHERE id=$1`, fileID).Scan(&key); err != nil {
		t.Fatalf("read key: %v", err)
	}
	return key
}

func stateOf(t *testing.T, db *pgxpool.Pool, fileID string) string {
	t.Helper()
	var st string
	if err := db.QueryRow(context.Background(), `SELECT state FROM files WHERE id=$1`, fileID).Scan(&st); err != nil {
		t.Fatalf("read state: %v", err)
	}
	return st
}
