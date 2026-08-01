package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type recordingRemover struct {
	mu   sync.Mutex
	seen []string
}

type gatedRemover struct {
	calls    chan string
	releases chan struct{}
}

func newGatedRemover() *gatedRemover {
	return &gatedRemover{
		calls:    make(chan string, 3),
		releases: make(chan struct{}),
	}
}

func (r *gatedRemover) Remove(_ context.Context, area, key string) error {
	r.calls <- area + ":" + key
	<-r.releases
	return nil
}

func awaitDeletionCall(t *testing.T, expected, redirected <-chan string) string {
	t.Helper()
	select {
	case got := <-expected:
		return got
	case got := <-redirected:
		t.Fatalf("asset removal was redirected after dispatch: %s", got)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for asset deletion worker")
	}
	return ""
}

func (r *recordingRemover) Remove(_ context.Context, area, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, area+":"+key)
	return nil
}

func (r *recordingRemover) removedAssets() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

func TestAccountDeletionMatrix(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	oldSink, oldRemover := securityEvents, assetRemover
	securityEvents = OutboxSecurityEventSink{}
	rem := &recordingRemover{}
	assetRemover = rem
	t.Cleanup(func() { securityEvents, assetRemover = oldSink, oldRemover })

	// Seed a member with private + public state.
	var uid, cid, mid string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('doomed','x') RETURNING id`).Scan(&uid)
	f.DB.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1,'Member')`, uid)
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ('doomed-sess',$1, NOW()+INTERVAL '1 hour')`, uid)
	f.DB.Exec(ctx, `INSERT INTO user_preferences (user_id, prefs) VALUES ($1,'{"theme":"ink"}')`, uid)
	f.DB.Exec(ctx, `INSERT INTO book_progress (user_id, book_id, percent) VALUES ($1,'b1',42)`, uid)
	catalogItem, err := NewCatalogRepository(f.DB).Observe(ctx, CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "lifecycle-book", LibraryID: "library-a",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatalf("observe lifecycle catalog item: %v", err)
	}
	if _, err := f.DB.Exec(ctx, `
		INSERT INTO member_progress (user_id, catalog_item_id, locator, percent)
		VALUES ($1::uuid, $2::uuid, '{"cfi":"x","fraction":0.4}'::jsonb, 0.4)`, uid, catalogItem.ID); err != nil {
		t.Fatalf("seed member progress: %v", err)
	}
	f.DB.QueryRow(ctx, `INSERT INTO channels (name) VALUES ('c') RETURNING id`).Scan(&cid)
	f.DB.QueryRow(ctx, `INSERT INTO messages (channel_id, user_id, content) VALUES ($1,$2,'public words') RETURNING id`, cid, uid).Scan(&mid)
	// A private avatar file and a shared file.
	f.DB.Exec(ctx, `INSERT INTO files (filename, sha256, uploader_id, scan_status, storage_key, size_bytes, mime_type, purpose) VALUES ('a.png','h',$1,'clean','avatars/a.png',1,'image/png','avatar')`, uid)
	f.DB.Exec(ctx, `INSERT INTO files (filename, sha256, uploader_id, scan_status, storage_key, size_bytes, mime_type, purpose) VALUES ('doc.pdf','h',$1,'clean','shared/doc.pdf',1,'application/pdf','shared')`, uid)

	receipt, err := DeleteAccount(ctx, uid)
	if err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	if receipt.RequestID == "" {
		t.Fatal("no receipt id")
	}

	// Private state is gone.
	assertCount(t, f.DB, `SELECT COUNT(*) FROM user_preferences WHERE user_id=$1`, uid, 0)
	assertCount(t, f.DB, `SELECT COUNT(*) FROM book_progress WHERE user_id=$1`, uid, 0)
	assertCount(t, f.DB, `SELECT COUNT(*) FROM member_progress WHERE user_id=$1`, uid, 0)
	assertCount(t, f.DB, `SELECT COUNT(*) FROM user_roles WHERE user_id=$1`, uid, 0)
	assertCount(t, f.DB, `SELECT COUNT(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL`, uid, 0)
	assertCount(t, f.DB, `SELECT COUNT(*) FROM files WHERE uploader_id=$1 AND purpose='avatar'`, uid, 0)
	// Shared file is retained.
	assertCount(t, f.DB, `SELECT COUNT(*) FROM files WHERE uploader_id=$1 AND purpose='shared'`, uid, 1)
	assertCount(t, f.DB, `SELECT COUNT(*) FROM catalog_items WHERE id=$1::uuid`, catalogItem.ID, 1)
	assertCount(t, f.DB, `SELECT COUNT(*) FROM catalog_sources WHERE catalog_item_id=$1::uuid`, catalogItem.ID, 1)

	// Public message survives, anonymized.
	assertCount(t, f.DB, `SELECT COUNT(*) FROM messages WHERE id=$1`, mid, 1)
	var username, display string
	f.DB.QueryRow(ctx, `SELECT username, display_name FROM users WHERE id=$1`, uid).Scan(&username, &display)
	if display != "Deleted User" || username[:8] != "deleted-" {
		t.Fatalf("user not anonymized: %q / %q", username, display)
	}

	// A durable account-deletion event was enqueued.
	assertCount(t, f.DB, `SELECT COUNT(*) FROM outbox_events WHERE event_type='account_deleted' AND aggregate_id=$1`, uid, 1)

	// Repeat deletion returns the SAME receipt (idempotent).
	receipt2, err := DeleteAccount(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if receipt2.RequestID != receipt.RequestID {
		t.Fatalf("repeat deletion changed the receipt: %s vs %s", receipt2.RequestID, receipt.RequestID)
	}
}

func TestAccountDeletionWorkerKeepsDispatchDependencies(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	oldSink, oldRemover := securityEvents, assetRemover
	securityEvents = OutboxSecurityEventSink{}
	capturedRemover := newGatedRemover()
	redirectedRemover := &gatedRemover{
		calls:    make(chan string, 3),
		releases: make(chan struct{}, 3),
	}
	assetRemover = capturedRemover
	t.Cleanup(func() {
		dbPool = f.DB
		securityEvents, assetRemover = oldSink, oldRemover
	})

	closedPool, err := pgxpool.NewWithConfig(ctx, f.DB.Config().Copy())
	if err != nil {
		t.Fatalf("create replacement pool: %v", err)
	}
	closedPool.Close()

	var userID string
	if err := f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('worker-race','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := f.DB.Exec(ctx, `
		INSERT INTO files (filename, sha256, uploader_id, scan_status, storage_key, size_bytes, mime_type, purpose)
		VALUES
			('one.bin', 'worker-race-1', $1, 'clean', 'private/one.bin', 1, 'application/octet-stream', 'book_ingest'),
			('two.bin', 'worker-race-2', $1, 'clean', 'private/two.bin', 1, 'application/octet-stream', 'book_ingest'),
			('three.bin', 'worker-race-3', $1, 'clean', 'private/three.bin', 1, 'application/octet-stream', 'book_ingest')
	`, userID); err != nil {
		t.Fatalf("seed private assets: %v", err)
	}

	if _, err := DeleteAccount(ctx, userID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}

	first := awaitDeletionCall(t, capturedRemover.calls, redirectedRemover.calls)
	dbPool = closedPool
	assetRemover = redirectedRemover
	capturedRemover.releases <- struct{}{}

	second := awaitDeletionCall(t, capturedRemover.calls, redirectedRemover.calls)
	assertAssetDeletionStatus(t, f.DB, first, "done")
	capturedRemover.releases <- struct{}{}

	awaitDeletionCall(t, capturedRemover.calls, redirectedRemover.calls)
	assertAssetDeletionStatus(t, f.DB, second, "done")
	capturedRemover.releases <- struct{}{}
}

func TestAssetDeletionWorkerUsesOnlyExplicitDependencies(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	if err := f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('explicit-worker','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := f.DB.Exec(ctx, `
		INSERT INTO asset_deletion_jobs (user_id, area, storage_key)
		VALUES ($1, 'upload', 'explicit/job.bin')
	`, userID); err != nil {
		t.Fatalf("seed deletion job: %v", err)
	}

	invalidGlobalPool, err := pgxpool.NewWithConfig(ctx, f.DB.Config().Copy())
	if err != nil {
		t.Fatalf("create invalid global pool: %v", err)
	}
	invalidGlobalPool.Close()
	explicitRemover := &recordingRemover{}
	globalRemover := &recordingRemover{}
	oldPool, oldRemover := dbPool, assetRemover
	dbPool, assetRemover = invalidGlobalPool, globalRemover
	t.Cleanup(func() { dbPool, assetRemover = oldPool, oldRemover })

	runAssetDeletionJobs(ctx, f.DB, explicitRemover, userID)

	assertAssetDeletionStatus(t, f.DB, "upload:explicit/job.bin", "done")
	if got := explicitRemover.removedAssets(); len(got) != 1 || got[0] != "upload:explicit/job.bin" {
		t.Fatalf("explicit remover calls = %v, want [upload:explicit/job.bin]", got)
	}
	if got := globalRemover.removedAssets(); len(got) != 0 {
		t.Fatalf("global remover received calls: %v", got)
	}
}

func TestAssetDeletionWorkerClosedPoolLeavesJobPending(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var userID string
	if err := f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('closed-worker','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := f.DB.Exec(ctx, `
		INSERT INTO asset_deletion_jobs (user_id, area, storage_key)
		VALUES ($1, 'upload', 'closed/job.bin')
	`, userID); err != nil {
		t.Fatalf("seed deletion job: %v", err)
	}

	closedWorkerPool, err := pgxpool.NewWithConfig(ctx, f.DB.Config().Copy())
	if err != nil {
		t.Fatalf("create worker pool: %v", err)
	}
	closedWorkerPool.Close()
	remover := &recordingRemover{}
	done := make(chan struct{})
	go func() {
		runAssetDeletionJobs(ctx, closedWorkerPool, remover, userID)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not return after its captured pool closed")
	}

	assertAssetDeletionStatus(t, f.DB, "upload:closed/job.bin", "pending")
	if got := remover.removedAssets(); len(got) != 0 {
		t.Fatalf("remover called after worker pool closed: %v", got)
	}
}

func assertAssetDeletionStatus(t *testing.T, pool *pgxpool.Pool, asset, want string) {
	t.Helper()
	storageKey := asset[len("upload:"):]
	var got string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM asset_deletion_jobs WHERE storage_key=$1`, storageKey).Scan(&got); err != nil {
		t.Fatalf("query asset deletion status for %q: %v", storageKey, err)
	}
	if got != want {
		t.Fatalf("asset deletion status for %q = %q, want %q", storageKey, got, want)
	}
}

func assertCount(t *testing.T, pool *pgxpool.Pool, sql, arg string, want int) {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, arg).Scan(&n); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if n != want {
		t.Fatalf("count = %d, want %d (%s)", n, want, sql)
	}
}
