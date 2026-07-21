package main

import (
	"context"
	"strings"
	"testing"

	"telos-core/testutil"
)

func TestSearchUsesTrigramIndexes(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()
	if _, err := RunMigrations(ctx, pool, migrationsFS); err != nil {
		t.Fatal(err)
	}
	// Seed enough users that the trigram index is a viable plan.
	for i := 0; i < 60; i++ {
		pool.Exec(ctx, `INSERT INTO users (username, password_hash) VALUES ($1,'x')`, "person"+itoa(i))
	}
	pool.Exec(ctx, `ANALYZE users`)

	// Force the planner away from a seq scan and confirm the trigram index is
	// the chosen access path for the substring search.
	var plan string
	rows, err := pool.Query(ctx, `
		SET LOCAL enable_seqscan = off;
		EXPLAIN (FORMAT TEXT)
		SELECT id FROM users WHERE lower(username) LIKE '%person1%'
	`)
	if err != nil {
		// Multi-statement EXPLAIN may need separate execs; fall back.
		pool.Exec(ctx, `SET enable_seqscan = off`)
		rows, err = pool.Query(ctx, `EXPLAIN (FORMAT TEXT) SELECT id FROM users WHERE lower(username) LIKE '%person1%'`)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		rows.Scan(&line)
		plan += line + "\n"
	}
	if !strings.Contains(plan, "idx_users_username_trgm") {
		t.Fatalf("user search did not use the trigram index; plan:\n%s", plan)
	}
}

func TestSearchScopeBounds(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()
	if _, err := RunMigrations(ctx, pool, migrationsFS); err != nil {
		t.Fatal(err)
	}
	// 30 matching users; the search cap is 15.
	for i := 0; i < 30; i++ {
		pool.Exec(ctx, `INSERT INTO users (username, password_hash) VALUES ($1,'x')`, "matchuser"+itoa(i))
	}
	pattern, ok := normalizedSearchTerm("matchuser")
	if !ok {
		t.Fatal("term rejected")
	}
	var n int
	pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
			SELECT id FROM users
			WHERE active = TRUE AND lower(username) LIKE $1
			LIMIT $2
		) s
	`, pattern, searchScopeLimit).Scan(&n)
	if n != searchScopeLimit {
		t.Fatalf("scope returned %d, want cap %d", n, searchScopeLimit)
	}
}
