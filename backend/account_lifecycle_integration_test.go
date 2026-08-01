package main

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type recordingRemover struct {
	mu   sync.Mutex
	seen []string
}

func (r *recordingRemover) Remove(_ context.Context, area, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, area+":"+key)
	return nil
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
