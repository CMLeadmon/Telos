package main

import (
	"context"
	"sync"
	"testing"

	"telos-core/testutil"
)

func TestMigration0020RemovesRealtimeExtras(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()
	if _, err := RunMigrations(ctx, pool, migrationsFS); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	for _, table := range []string{
		"watch_parties", "watch_party_members", "watch_party_invitations",
		"watch_party_host_offers", "notifications", "media_lists", "media_list_entries",
	} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.tables
			   WHERE table_schema='public' AND table_name=$1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if exists {
			t.Errorf("table %s should have been dropped", table)
		}
	}

	var typeExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.columns
		   WHERE table_name='channels' AND column_name='type')`).Scan(&typeExists); err != nil {
		t.Fatalf("check channels.type: %v", err)
	}
	if typeExists {
		t.Error("channels.type should have been dropped")
	}

	var permCount int
	pool.QueryRow(ctx, `SELECT count(*) FROM permissions WHERE id='join_voice'`).Scan(&permCount)
	if permCount != 0 {
		t.Error("join_voice permission should have been dropped")
	}

	// The cascade must leave no orphaned per-channel override.
	var orphans int
	pool.QueryRow(ctx, `SELECT count(*) FROM channel_permission_overrides WHERE permission_id='join_voice'`).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("found %d orphaned join_voice overrides", orphans)
	}

	var roleCount int
	pool.QueryRow(ctx, `SELECT count(*) FROM roles WHERE id IN ('Contributor','Librarian')`).Scan(&roleCount)
	if roleCount != 0 {
		t.Error("Contributor and Librarian should have been deleted")
	}

	// Member must not gain a destructive capability in the collapse.
	var escalated int
	pool.QueryRow(ctx, `
		SELECT count(*) FROM role_permissions
		WHERE role_id='Member' AND permission_id IN ('manage_files','manage_library')
	`).Scan(&escalated)
	if escalated != 0 {
		t.Error("Member must not gain manage_files or manage_library")
	}

	// Member absorbs Contributor's upload_books capability.
	var memberUpload int
	pool.QueryRow(ctx, `SELECT count(*) FROM role_permissions WHERE role_id='Member' AND permission_id='upload_books'`).Scan(&memberUpload)
	if memberUpload != 1 {
		t.Error("Member should have gained upload_books")
	}

	// Re-applying the whole set is a clean no-op (idempotent drops/inserts).
	if _, err := VerifyMigrations(ctx, pool, migrationsFS); err != nil {
		t.Fatalf("verify after apply: %v", err)
	}
}

func TestRunMigrationsEmptyAppliesAtomically(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()

	state, err := RunMigrations(ctx, pool, migrationsFS)
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if state.CurrentVersion != state.ExpectedVersion || state.CurrentVersion < 7 {
		t.Fatalf("state = %+v", state)
	}

	// schema_migrations carries name + checksum for every applied version.
	var missing int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE checksum IS NULL OR checksum = ''`).Scan(&missing)
	if missing != 0 {
		t.Fatalf("%d applied migrations lack a checksum", missing)
	}

	// Re-run is a no-op and verifies clean.
	if _, err := VerifyMigrations(ctx, pool, migrationsFS); err != nil {
		t.Fatalf("verify after apply: %v", err)
	}
}

func TestRunMigrationsBlessesLegacyContiguousHistory(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()

	// Simulate the legacy version-only table with rows 1..3 and no checksums.
	pool.Exec(ctx, `CREATE TABLE schema_migrations (version INT PRIMARY KEY, applied_at TIMESTAMPTZ DEFAULT NOW())`)
	// Apply the real 0001..0003 SQL so the schema actually matches.
	local, _ := DiscoverMigrations(migrationsFS, "db/migrations")
	for i := 0; i < 3; i++ {
		if _, err := pool.Exec(ctx, string(local[i].SQL)); err != nil {
			t.Fatalf("seed legacy %d: %v", i+1, err)
		}
		pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, i+1)
	}

	state, err := RunMigrations(ctx, pool, migrationsFS)
	if err != nil {
		t.Fatalf("RunMigrations over legacy: %v", err)
	}
	if state.CurrentVersion != len(local) {
		t.Fatalf("did not complete: %+v", state)
	}
	var blessed int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version <= 3 AND checksum <> ''`).Scan(&blessed)
	if blessed != 3 {
		t.Fatalf("legacy rows not blessed: %d/3", blessed)
	}
}

func TestRunMigrationsRejectsLegacyGap(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()
	pool.Exec(ctx, `CREATE TABLE schema_migrations (version INT PRIMARY KEY, applied_at TIMESTAMPTZ DEFAULT NOW())`)
	// Non-contiguous legacy history (1 and 3, missing 2).
	pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES (1), (3)`)

	if _, err := RunMigrations(ctx, pool, migrationsFS); err == nil {
		t.Fatal("expected refusal to bless a non-contiguous legacy history")
	}
}

func TestRunMigrationsRejectsChangedChecksum(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()
	if _, err := RunMigrations(ctx, pool, migrationsFS); err != nil {
		t.Fatal(err)
	}
	// Tamper with a recorded checksum.
	pool.Exec(ctx, `UPDATE schema_migrations SET checksum = 'tampered' WHERE version = 1`)
	if _, err := VerifyMigrations(ctx, pool, migrationsFS); err == nil {
		t.Fatal("verify accepted a tampered checksum")
	}
}

func TestConcurrentMigratorsApplyOnce(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = RunMigrations(ctx, pool, migrationsFS)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("migrator %d failed: %v", i, e)
		}
	}
	// Exactly one row per version — no double application under the advisory lock.
	var dupes int
	pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
			SELECT version FROM schema_migrations GROUP BY version HAVING COUNT(*) > 1
		) d`).Scan(&dupes)
	if dupes != 0 {
		t.Fatalf("found %d duplicated migration versions", dupes)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	pool := testutil.FreshDatabase(t)
	ctx := context.Background()

	badFS := fsFrom(map[string]string{
		"0001_ok.sql":  "CREATE TABLE t_ok (id int);",
		"0002_bad.sql": "CREATE TABLE t_bad (id int); INSERT INTO nonexistent_table VALUES (1);",
	})
	if _, err := RunMigrations(ctx, pool, badFS); err == nil {
		t.Fatal("expected failure on bad migration")
	}
	// 0001 committed; 0002 rolled back entirely (its table must not exist).
	var okExists, badExists bool
	pool.QueryRow(ctx, `SELECT to_regclass('public.t_ok') IS NOT NULL`).Scan(&okExists)
	pool.QueryRow(ctx, `SELECT to_regclass('public.t_bad') IS NOT NULL`).Scan(&badExists)
	if !okExists {
		t.Fatal("0001 was not committed")
	}
	if badExists {
		t.Fatal("failed 0002 was not rolled back")
	}
}
