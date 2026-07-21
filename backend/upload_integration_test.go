//go:build linux

package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func uploadPipelineFixture(t *testing.T, verdict string) (*UploadPipeline, string) {
	t.Helper()
	f := withFixture(t)
	old := dbPool
	dbPool = f.DB
	t.Cleanup(func() { dbPool = old })

	s, root := newStorage(t)
	_ = root
	scanner := fakeClamd(t, verdict, "", false)
	guard := newQuotaGuard(QuotaPolicy{PerUserPhysicalBytes: 1 << 30, NodeReserveBytes: 1, ReservationTTL: time.Minute}, t.TempDir())
	p := &UploadPipeline{storage: s, quota: guard, scanner: scanner, staging: "staging", shared: "shared"}

	var uid string
	f.DB.QueryRow(context.Background(), `INSERT INTO users (username, password_hash) VALUES ('uploader','x') RETURNING id`).Scan(&uid)
	return p, uid
}

func TestUploadPipelineCleanFileBecomesAvailable(t *testing.T) {
	p, uid := uploadPipelineFixture(t, "stream: OK\x00")
	ctx := context.Background()

	res, err := p.Process(ctx, UploadRequest{
		UploaderID: uid, Filename: "notes.txt", Purpose: PurposeShared,
		DeclaredSize: 11, MaxBytes: 100 << 20,
	}, strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}

	// The catalog row is available/clean and the physical file is present.
	var state, scan string
	dbPool.QueryRow(ctx, `SELECT state, scan_status FROM files WHERE id=$1`, res.FileID).Scan(&state, &scan)
	if state != "available" || scan != "clean" {
		t.Fatalf("catalog row state=%q scan=%q", state, scan)
	}
	if _, err := p.storage.OpenRead("shared", res.StorageKey); err != nil {
		t.Fatalf("promoted file not readable: %v", err)
	}
	// The reservation was released (no lingering rows).
	var reservations int
	dbPool.QueryRow(ctx, `SELECT COUNT(*) FROM upload_reservations WHERE user_id=$1`, uid).Scan(&reservations)
	if reservations != 0 {
		t.Fatalf("%d reservations left after commit", reservations)
	}
	// An audit row was written.
	var audits int
	dbPool.QueryRow(ctx, `SELECT COUNT(*) FROM file_audit WHERE file_id=$1 AND action='create'`, res.FileID).Scan(&audits)
	if audits != 1 {
		t.Fatalf("audit rows = %d, want 1", audits)
	}
}

func TestUploadPipelineInfectedFailsClosed(t *testing.T) {
	p, uid := uploadPipelineFixture(t, "stream: Eicar FOUND\x00")
	ctx := context.Background()

	_, err := p.Process(ctx, UploadRequest{
		UploaderID: uid, Filename: "bad.txt", Purpose: PurposeShared,
		DeclaredSize: 3, MaxBytes: 100 << 20,
	}, strings.NewReader("bad"))
	if err == nil {
		t.Fatal("infected upload succeeded")
	}
	// No available catalog row, no lingering reservation, no promoted file.
	var files, reservations int
	dbPool.QueryRow(ctx, `SELECT COUNT(*) FROM files WHERE uploader_id=$1`, uid).Scan(&files)
	dbPool.QueryRow(ctx, `SELECT COUNT(*) FROM upload_reservations WHERE user_id=$1`, uid).Scan(&reservations)
	if files != 0 {
		t.Fatalf("infected upload left %d catalog rows", files)
	}
	if reservations != 0 {
		t.Fatalf("infected upload left %d reservations", reservations)
	}
}

func TestUploadPipelineOversizeFailsClosed(t *testing.T) {
	p, uid := uploadPipelineFixture(t, "stream: OK\x00")
	ctx := context.Background()
	_, err := p.Process(ctx, UploadRequest{
		UploaderID: uid, Filename: "big.txt", Purpose: PurposeShared,
		DeclaredSize: 5, MaxBytes: 5,
	}, strings.NewReader("way more than five bytes"))
	if err == nil {
		t.Fatal("oversize upload succeeded")
	}
	var files int
	dbPool.QueryRow(ctx, `SELECT COUNT(*) FROM files WHERE uploader_id=$1`, uid).Scan(&files)
	if files != 0 {
		t.Fatal("oversize upload created a catalog row")
	}
}
