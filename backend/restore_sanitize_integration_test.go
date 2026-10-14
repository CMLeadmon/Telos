package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"telos-core/testutil"
)

// restoredDatabase is a migrated database holding what a backup would have carried:
// live credentials of every kind, content, and an audit record.
func restoredDatabase(t *testing.T) (*pgxpool.Pool, map[string]string) {
	t.Helper()
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()
	if _, err := RunMigrations(ctx, pool, migrationsFS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ids := map[string]string{}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", strings.Fields(sql)[0], err)
		}
	}
	scan := func(key, sql string, args ...any) {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
		ids[key] = id
	}
	scan("alice", `INSERT INTO users (username, password_hash) VALUES ('alice','hash-a') RETURNING id`)
	scan("bob", `INSERT INTO users (username, password_hash) VALUES ('bob','hash-b') RETURNING id`)
	// Browser sessions (no device) and a device with an access session and refresh tokens.
	exec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (repeat('a',64), $1, NOW() + interval '7 days'), (repeat('b',64), $2, NOW() + interval '7 days')`, ids["alice"], ids["bob"])
	exec(`INSERT INTO sessions (token_hash, user_id, expires_at, revoked_at) VALUES (repeat('c',64), $1, NOW() + interval '7 days', NOW() - interval '1 day')`, ids["alice"])
	scan("device", `INSERT INTO devices (user_id, device_name, platform, client_version) VALUES ($1,'Laptop','linux','1.0') RETURNING id`, ids["alice"])
	exec(`INSERT INTO sessions (token_hash, user_id, expires_at, device_id) VALUES (repeat('d',64), $1, NOW() + interval '12 hours', $2)`, ids["alice"], ids["device"])
	exec(`INSERT INTO device_refresh_tokens (device_id, token_hash, expires_at) VALUES ($1, repeat('e',64), NOW() + interval '30 days')`, ids["device"])
	// Invites: unused (live), unused (already expired), used.
	exec(`INSERT INTO invites (token_hash, creator_id, created_at, expires_at) VALUES (repeat('1',64), $1, NOW(), NOW() + interval '3 days'), (repeat('2',64), $1, NOW() - interval '4 days', NOW() - interval '1 day')`, ids["alice"])
	exec(`INSERT INTO invites (token_hash, creator_id, created_at, expires_at, used_at, used_by) VALUES (repeat('3',64), $1, NOW() - interval '2 hours', NOW() + interval '3 days', NOW() - interval '1 hour', $2)`, ids["alice"], ids["bob"])
	return pool, ids
}

func counts(t *testing.T, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	ctx := context.Background()
	out := map[string]int{}
	for key, sql := range map[string]string{
		"liveSessions":  `SELECT count(*) FROM sessions WHERE revoked_at IS NULL`,
		"liveDevices":   `SELECT count(*) FROM devices WHERE revoked_at IS NULL`,
		"liveRefresh":   `SELECT count(*) FROM device_refresh_tokens WHERE consumed_at IS NULL`,
		"liveInvites":   `SELECT count(*) FROM invites WHERE used_at IS NULL AND expires_at > NOW()`,
		"users":         `SELECT count(*) FROM users`,
		"usedInvites":   `SELECT count(*) FROM invites WHERE used_at IS NOT NULL`,
		"restoreEvents": `SELECT count(*) FROM restore_events`,
	} {
		var n int
		if err := pool.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		out[key] = n
	}
	return out
}

func TestSanitizeRestoreRevokesEveryCredentialAndKeepsAccounts(t *testing.T) {
	pool, _ := restoredDatabase(t)
	ctx := context.Background()
	before := counts(t, pool)
	if before["liveSessions"] != 3 || before["liveDevices"] != 1 || before["liveRefresh"] != 1 || before["liveInvites"] != 1 {
		t.Fatalf("seed is wrong: %v", before)
	}

	result, err := runSanitizeRestore(ctx, pool, "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionsRevoked != 3 || result.DevicesRevoked != 1 || result.RefreshTokensRevoked != 1 || result.InvitesInvalidated != 1 {
		t.Fatalf("result = %+v", result)
	}
	after := counts(t, pool)
	if after["liveSessions"] != 0 || after["liveDevices"] != 0 || after["liveRefresh"] != 0 || after["liveInvites"] != 0 {
		t.Fatalf("live credentials remain: %v", after)
	}
	// The previously used invite is untouched; the live one is now marked used; the expired
	// one stays as it was.
	if after["users"] != 2 || after["usedInvites"] != 2 {
		t.Fatalf("accounts or invites are wrong: %v", after)
	}
	var snapshot string
	var sessions, devices, tokens, invites int
	if err := pool.QueryRow(ctx, `SELECT source_snapshot, sessions_revoked, devices_revoked, refresh_tokens_revoked, invites_invalidated FROM restore_events`).
		Scan(&snapshot, &sessions, &devices, &tokens, &invites); err != nil {
		t.Fatalf("no restore audit event: %v", err)
	}
	if snapshot != "0123456789abcdef" || sessions != 3 || devices != 1 || tokens != 1 || invites != 1 {
		t.Fatalf("audit event = %s %d %d %d %d", snapshot, sessions, devices, tokens, invites)
	}

	// A second run is safe: nothing further changes, and it records itself.
	second, err := runSanitizeRestore(ctx, pool, "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if second != (SanitizeResult{}) {
		t.Errorf("second run changed %+v, want nothing", second)
	}
	if got := counts(t, pool); got["restoreEvents"] != 2 || got["users"] != 2 {
		t.Errorf("after a second run: %v", got)
	}
}

func TestSanitizeRestoreFailureChangesNothing(t *testing.T) {
	pool, _ := restoredDatabase(t)
	ctx := context.Background()
	// The audit insert fails after every revocation has run; the whole transaction must go.
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION refuse_restore_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$;
		CREATE TRIGGER refuse_restore_event BEFORE INSERT ON restore_events FOR EACH ROW EXECUTE FUNCTION refuse_restore_event();
	`); err != nil {
		t.Fatal(err)
	}
	before := counts(t, pool)
	if _, err := runSanitizeRestore(ctx, pool, "0123456789abcdef"); err == nil {
		t.Fatal("an injected failure was swallowed")
	} else if !strings.Contains(err.Error(), "recording the restore event") {
		t.Fatalf("unexpected error: %v", err)
	}
	after := counts(t, pool)
	for key, value := range before {
		if after[key] != value {
			t.Errorf("%s changed from %d to %d although the transaction failed", key, value, after[key])
		}
	}
}

func TestSanitizeRestoreRefusesAnUnmigratedOrForeignDatabase(t *testing.T) {
	ctx := context.Background()
	empty := testutil.FreshDatabase(t)
	if _, err := runSanitizeRestore(ctx, empty, "0123456789abcdef"); err == nil || !strings.Contains(err.Error(), "refusing to run") {
		t.Fatalf("an empty database: err = %v, want a refusal", err)
	}

	pool, _ := restoredDatabase(t)
	// A newer schema than this release knows (a backup from a later version).
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES (999, 'from_the_future', repeat('f',64))`); err != nil {
		t.Fatal(err)
	}
	if _, err := runSanitizeRestore(ctx, pool, "0123456789abcdef"); err == nil || !strings.Contains(err.Error(), "refusing to run") {
		t.Fatalf("a newer schema: err = %v, want a refusal", err)
	}
	if counts(t, pool)["liveSessions"] != 3 {
		t.Fatal("a refused sanitize still changed data")
	}
	// A drifted checksum (the migration file was edited after it was applied).
	pool2, _ := restoredDatabase(t)
	if _, err := pool2.Exec(ctx, `UPDATE schema_migrations SET checksum = repeat('0',64) WHERE version = 3`); err != nil {
		t.Fatal(err)
	}
	if _, err := runSanitizeRestore(ctx, pool2, "0123456789abcdef"); err == nil || !strings.Contains(err.Error(), "refusing to run") {
		t.Fatalf("checksum drift: err = %v, want a refusal", err)
	}
}

func TestSanitizeRestoreRejectsABadSnapshotIdentifier(t *testing.T) {
	pool, _ := restoredDatabase(t)
	for _, bad := range []string{"SNAPSHOT", "short", "0123456789abcdef'; DROP TABLE users;--", strings.Repeat("a", 65)} {
		if _, err := runSanitizeRestore(context.Background(), pool, bad); err == nil {
			t.Errorf("snapshot %q was accepted", bad)
		}
	}
	if counts(t, pool)["restoreEvents"] != 0 {
		t.Error("an invalid snapshot id still recorded an event")
	}
	_ = time.Second
}
