package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func reconFixture(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	f := withFixture(t)
	old := dbPool
	dbPool = f.DB
	t.Cleanup(func() { dbPool = old })
	var uid string
	if err := f.DB.QueryRow(context.Background(), `INSERT INTO users (username, password_hash) VALUES ('reconowner','x') RETURNING id::text`).Scan(&uid); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return f.DB, uid
}

func insertFileFull(t *testing.T, db *pgxpool.Pool, ownerID, key, hash string, size int64, state string) string {
	t.Helper()
	var id string
	err := db.QueryRow(context.Background(), `
		INSERT INTO files (filename, sha256, storage_key, size_bytes, mime_type, uploader_id, scan_status, state, purpose)
		VALUES ('f.bin', $1, $2, $3, 'application/octet-stream', $4::uuid, 'clean', $5, 'shared')
		RETURNING id::text`, hash, key, size, ownerID, state).Scan(&id)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	return id
}

func addExpiredLease(t *testing.T, db *pgxpool.Pool, fileID string) {
	t.Helper()
	_, err := db.Exec(context.Background(), `
		INSERT INTO file_ingestion_leases (file_id, lease_owner, expires_at)
		VALUES ($1, 'w', NOW() - interval '2 hours')`, fileID)
	if err != nil {
		t.Fatalf("insert lease: %v", err)
	}
}

func TestFileReconcilerFinalizesDeletions(t *testing.T) {
	db, uid := reconFixture(t)
	ctx := context.Background()
	phys := newFakePhysical()
	phys.put("shared/del", "h", 10)
	id := insertFileFull(t, db, uid, "shared/del", "h", 10, "deleting")

	rec := newFileReconciler(db, phys, nil)
	rep, err := rec.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Deleted != 1 {
		t.Fatalf("Deleted = %d, want 1", rep.Deleted)
	}
	if st := stateOf(t, db, id); st != "deleted" {
		t.Fatalf("state = %q, want deleted", st)
	}
	if present, _ := phys.Exists("shared", "shared/del"); present {
		t.Fatal("physical asset not removed")
	}
	// Second identical pass converges: nothing left in 'deleting'.
	rep2, _ := rec.RunOnce(ctx)
	if rep2.Deleted != 0 {
		t.Fatalf("second pass Deleted = %d, want 0", rep2.Deleted)
	}
}

func TestFileReconcilerRepairsAndMisses(t *testing.T) {
	db, uid := reconFixture(t)
	ctx := context.Background()
	phys := newFakePhysical()
	// present + identity match -> repaired; absent -> missing.
	phys.put("shared/ok", "GOOD", 10)
	repaired := insertFileFull(t, db, uid, "shared/ok", "GOOD", 10, "promoting")
	missing := insertFileFull(t, db, uid, "shared/lost", "LOST", 10, "promoting")
	addExpiredLease(t, db, repaired)
	addExpiredLease(t, db, missing)

	rec := newFileReconciler(db, phys, nil)
	rep, err := rec.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Repaired != 1 || rep.Missing != 1 {
		t.Fatalf("Repaired=%d Missing=%d, want 1/1", rep.Repaired, rep.Missing)
	}
	if st := stateOf(t, db, repaired); st != "available" {
		t.Fatalf("repaired state = %q, want available", st)
	}
	if st := stateOf(t, db, missing); st != "missing" {
		t.Fatalf("missing state = %q, want missing", st)
	}
	// Leases are consumed either way.
	var leases int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM file_ingestion_leases`).Scan(&leases)
	if leases != 0 {
		t.Fatalf("leases remaining = %d, want 0", leases)
	}
}

func TestFileReconcilerAcknowledgesHandoffs(t *testing.T) {
	db, uid := reconFixture(t)
	ctx := context.Background()
	catalog := &fakeCatalog{imported: map[string]string{"IMPORTED": "book-42"}}
	consumed := insertFileFull(t, db, uid, "bookdrop/a", "IMPORTED", 10, "handed_off")
	pending := insertFileFull(t, db, uid, "bookdrop/b", "NOTYET", 10, "handed_off")

	rec := newFileReconciler(db, nil, catalog)
	rep, err := rec.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Consumed != 1 {
		t.Fatalf("Consumed = %d, want 1", rep.Consumed)
	}
	if st := stateOf(t, db, consumed); st != "consumed" {
		t.Fatalf("consumed state = %q, want consumed", st)
	}
	// Unobserved import is never assumed lost; it stays handed_off.
	if st := stateOf(t, db, pending); st != "handed_off" {
		t.Fatalf("pending state = %q, want handed_off", st)
	}
}

func TestFileReconcilerSingleWalker(t *testing.T) {
	db, uid := reconFixture(t)
	ctx := context.Background()
	insertFileFull(t, db, uid, "shared/x", "h", 10, "deleting")

	// Hold the walker lock on a dedicated connection; RunOnce must yield without
	// touching any row.
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", reconcileWalkerLock); err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", reconcileWalkerLock)

	rec := newFileReconciler(db, newFakePhysical(), nil)
	rep, err := rec.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Deferred == 0 || rep.Deleted != 0 {
		t.Fatalf("expected deferral with no work done, got %+v", rep)
	}
}
