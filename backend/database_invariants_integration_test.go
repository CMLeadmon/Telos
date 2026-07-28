package main

import (
	"context"
	"testing"

	"telos-core/testutil"
)

func TestDatabaseConstraintsRejectInvalidRows(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()
	if _, err := RunMigrations(ctx, pool, migrationsFS); err != nil {
		t.Fatal(err)
	}

	// Seed a valid user + channel to hang FK-bound rows off of.
	var uid, cid string
	pool.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('alice','x') RETURNING id`).Scan(&uid)
	pool.QueryRow(ctx, `INSERT INTO channels (name) VALUES ('general') RETURNING id`).Scan(&cid)

	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{"non-canonical username", `INSERT INTO users (username, password_hash) VALUES ($1,'x')`, []any{"Bad Name!"}},
		{"negative mask", `INSERT INTO channel_role_overrides (channel_id, role_id, allow_mask, deny_mask) VALUES ($1,'Member',-1,0)`, []any{cid}},
		{"overlong message", `INSERT INTO messages (channel_id, user_id, content) VALUES ($1,$2,repeat('a',4001))`, []any{cid, uid}},
		{"session expiry before creation", `INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES ('h',$1, NOW() - INTERVAL '1 hour', NOW())`, []any{uid}},
		{"negative file size", `INSERT INTO files (filename, storage_path, size_bytes, mime_type) VALUES ('f','p',-1,'text/plain')`, nil},
		{"bad scan status", `INSERT INTO files (filename, storage_path, size_bytes, mime_type, scan_status) VALUES ('f','p',1,'text/plain','bogus')`, nil},
		{"out-of-range progress", `INSERT INTO book_progress (user_id, book_id, percent) VALUES ($1,'b',150)`, []any{uid}},
		{"non-object prefs", `INSERT INTO user_preferences (user_id, prefs) VALUES ($1,'[1,2]')`, []any{uid}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tc.sql, tc.args...); err == nil {
				t.Fatalf("constraint did not reject: %s", tc.name)
			}
		})
	}
}

func TestForeignKeyIndexAuditIsEmpty(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()
	if _, err := RunMigrations(ctx, pool, migrationsFS); err != nil {
		t.Fatal(err)
	}

	// Every foreign key must have an index whose leading column is the FK
	// column (covering joins and cascade deletes).
	rows, err := pool.Query(ctx, `
		SELECT conrelid::regclass::text, att.attname
		FROM pg_constraint c
		JOIN pg_attribute att
		  ON att.attrelid = c.conrelid AND att.attnum = c.conkey[1]
		WHERE c.contype = 'f'
		  AND NOT EXISTS (
		    SELECT 1 FROM pg_index i
		    WHERE i.indrelid = c.conrelid
		      AND i.indkey[0] = c.conkey[1]
		  )
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var tbl, col string
		rows.Scan(&tbl, &col)
		missing = append(missing, tbl+"."+col)
	}
	if len(missing) > 0 {
		t.Fatalf("foreign keys without a leading index: %v", missing)
	}
}

func TestMessageManagementGrant(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()
	if _, err := RunMigrations(ctx, pool, migrationsFS); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"Owner", "Administrator", "Moderator"} {
		var has bool
		pool.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM role_permissions WHERE role_id=$1 AND permission_id='manage_messages')
		`, role).Scan(&has)
		if !has {
			t.Errorf("%s lacks manage_messages", role)
		}
	}
	// Member must NOT have it.
	var memberHas bool
	pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM role_permissions WHERE role_id='Member' AND permission_id='manage_messages')`).Scan(&memberHas)
	if memberHas {
		t.Error("Member unexpectedly has manage_messages")
	}
}
