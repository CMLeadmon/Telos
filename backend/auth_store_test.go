package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLoadAuthenticatedUserAggregates(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()

	var uid string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash, display_name) VALUES ('alice','x','Alice A') RETURNING id`).Scan(&uid)
	f.DB.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1,'Member'),($1,'Moderator')`, uid)
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ('h1',$1, NOW() + INTERVAL '1 hour')`, uid)

	uc, err := LoadAuthenticatedUser(ctx, f.DB, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if uc.Username != "alice" || uc.DisplayName != "Alice A" {
		t.Fatalf("identity wrong: %+v", uc)
	}
	if len(uc.Roles) != 2 {
		t.Fatalf("roles = %v", uc.Roles)
	}
	// Permissions are sorted and include both roles' grants (e.g. moderate_chat
	// from Moderator, view_channel from Member).
	if !contains(uc.Permissions, "moderate_chat") || !contains(uc.Permissions, "view_channel") {
		t.Fatalf("permissions missing expected grants: %v", uc.Permissions)
	}
	for i := 1; i < len(uc.Permissions); i++ {
		if uc.Permissions[i-1] > uc.Permissions[i] {
			t.Fatalf("permissions not sorted: %v", uc.Permissions)
		}
	}

	// Revoked / expired / disabled cases fail.
	f.DB.Exec(ctx, `UPDATE sessions SET revoked_at = NOW() WHERE token_hash='h1'`)
	if _, err := LoadAuthenticatedUser(ctx, f.DB, "h1"); err == nil {
		t.Fatal("revoked session loaded")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestDatabaseTimeoutsFire(t *testing.T) {
	// A pool built with a short statement timeout must abort a slow query.
	if testing.Short() {
		t.Skip("short")
	}
	f := withFixture(t)
	_ = f
	cfg := defaultDatabaseConfig(testDatabaseURL(t))
	cfg.StatementTimeout = 300 * time.Millisecond
	pool, err := NewDatabasePool(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = pool.Exec(context.Background(), "SELECT pg_sleep(3)")
	if err == nil {
		t.Fatal("statement timeout did not fire on a 3s query")
	}
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TELOS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("no TELOS_TEST_DATABASE_URL")
	}
	return url
}

func TestSessionTouchWorkerCoalesces(t *testing.T) {
	f := withFixture(t)
	ctx := context.Background()
	var uid string
	f.DB.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('bob','x') RETURNING id`).Scan(&uid)
	f.DB.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at, last_seen_at) VALUES ('t1',$1, NOW()+INTERVAL '1 hour', NULL)`, uid)

	w := newSessionTouchWorker(f.DB)
	wctx, cancel := context.WithCancel(ctx)
	w.Run(wctx)
	for i := 0; i < 50; i++ {
		w.Touch("t1")
	}
	// Cancel drains and flushes.
	cancel()
	w.Wait()

	var lastSeen *time.Time
	f.DB.QueryRow(ctx, `SELECT last_seen_at FROM sessions WHERE token_hash='t1'`).Scan(&lastSeen)
	if lastSeen == nil {
		t.Fatal("coalesced touch did not stamp last_seen_at")
	}
}
