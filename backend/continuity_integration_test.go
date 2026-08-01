package main

import (
	"errors"
	"fmt"
	"testing"
)

func createContinuityUser(t *testing.T, username string, db DBTX) string {
	t.Helper()
	var id string
	if err := db.QueryRow(t.Context(), `
		INSERT INTO users (username, password_hash)
		VALUES ($1, 'x')
		RETURNING id::text`, username).Scan(&id); err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return id
}

func TestContinuityRepositoryIsolatesLatestProgressAndBulkReads(t *testing.T) {
	catalog, db := catalogFixture(t)
	repo := NewContinuityRepository(db)
	alice := createContinuityUser(t, "continuity-alice", db)
	bob := createContinuityUser(t, "continuity-bob", db)
	book := observeCatalog(t, catalog, ProviderGrimmory, "continuity-book", "books", SurfaceLibrary, "audiobook")
	otherBook := observeCatalog(t, catalog, ProviderGrimmory, "continuity-other", "books", SurfaceLibrary, "epub")

	aliceInput := ProgressInput{
		Locator:    []byte(`{"trackIndex":1}`),
		PositionMS: 1000,
		DurationMS: 10000,
		Percent:    0.1,
	}
	putAcquiresBefore := db.Stat().AcquireCount()
	aliceProgress, err := repo.Put(t.Context(), alice, book.ID, aliceInput)
	if err != nil {
		t.Fatalf("Put alice: %v", err)
	}
	if aliceProgress.UpdatedAt == nil || aliceProgress.PositionMS != 1000 {
		t.Fatalf("alice Put result = %+v", aliceProgress)
	}
	if delta := db.Stat().AcquireCount() - putAcquiresBefore; delta != 1 {
		t.Fatalf("Put acquired %d connections, want one upsert query", delta)
	}

	bobInput := aliceInput
	bobInput.PositionMS = 8000
	bobInput.Percent = 0.8
	if _, err := repo.Put(t.Context(), bob, book.ID, bobInput); err != nil {
		t.Fatalf("Put bob: %v", err)
	}

	for _, tt := range []struct {
		name   string
		userID string
		want   int64
	}{
		{name: "alice", userID: alice, want: 1000},
		{name: "bob", userID: bob, want: 8000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.Get(t.Context(), tt.userID, book.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.PositionMS != tt.want || got.UpdatedAt == nil {
				t.Fatalf("progress = %+v, want position %d", got, tt.want)
			}
		})
	}

	missing, err := repo.Get(t.Context(), alice, otherBook.ID)
	if err != nil {
		t.Fatalf("Get missing: %v", err)
	}
	if missing.UpdatedAt != nil || missing.PositionMS != 0 || missing.Percent != 0 {
		t.Fatalf("missing progress = %+v, want zero value", missing)
	}

	acquiresBefore := db.Stat().AcquireCount()
	many, err := repo.GetMany(t.Context(), alice, []string{book.ID, book.ID, otherBook.ID})
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if delta := db.Stat().AcquireCount() - acquiresBefore; delta != 1 {
		t.Fatalf("GetMany acquired %d connections, want one query", delta)
	}
	if len(many) != 1 || many[book.ID].PositionMS != 1000 {
		t.Fatalf("GetMany = %+v, want only alice's recorded item", many)
	}

	latest := aliceInput
	latest.Locator = []byte(`{"trackIndex":2}`)
	latest.PositionMS = 2500
	latest.Percent = 0.25
	updated, err := repo.Put(t.Context(), alice, book.ID, latest)
	if err != nil {
		t.Fatalf("update alice: %v", err)
	}
	if updated.PositionMS != 2500 || updated.UpdatedAt == nil || updated.UpdatedAt.Before(*aliceProgress.UpdatedAt) {
		t.Fatalf("updated progress = %+v, first = %+v", updated, aliceProgress)
	}

	var progressRows, outboxRows, userEventRows, historyTables int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM member_progress`).Scan(&progressRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events`).Scan(&outboxRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM user_events`).Scan(&userEventRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(t.Context(), `
		SELECT count(*) FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename ~ '^(member_progress|playback).*(event|history)'`).Scan(&historyTables); err != nil {
		t.Fatal(err)
	}
	if progressRows != 2 || outboxRows != 0 || userEventRows != 0 || historyTables != 0 {
		t.Fatalf("rows progress/outbox/user-events/history-tables = %d/%d/%d/%d", progressRows, outboxRows, userEventRows, historyTables)
	}
}

func TestContinuityContinueOrdersRecentIncompleteItemsWithinSurface(t *testing.T) {
	catalog, db := catalogFixture(t)
	repo := NewContinuityRepository(db)
	alice := createContinuityUser(t, "continuity-order", db)
	bob := createContinuityUser(t, "continuity-order-bob", db)
	first := observeCatalog(t, catalog, ProviderGrimmory, "continue-first", "books", SurfaceLibrary, "epub")
	second := observeCatalog(t, catalog, ProviderGrimmory, "continue-second", "books", SurfaceLibrary, "pdf")
	streamItem := observeCatalog(t, catalog, ProviderJellyfin, "continue-video", "movies", SurfaceStream, "video")

	for itemID, input := range map[string]ProgressInput{
		first.ID:      {Locator: []byte(`{"cfi":"x","fraction":0.1}`), Percent: 0.1},
		second.ID:     {Locator: []byte(`{"page":2,"zoom":1}`), Percent: 0.2},
		streamItem.ID: {Locator: []byte(`{}`), PositionMS: 3000, DurationMS: 10000, Percent: 0.3},
	} {
		if _, err := repo.Put(t.Context(), alice, itemID, input); err != nil {
			t.Fatalf("Put %s: %v", itemID, err)
		}
	}
	if _, err := repo.Put(t.Context(), bob, first.ID, ProgressInput{
		Locator: []byte(`{"cfi":"x","fraction":0.9}`), Percent: 0.9,
	}); err != nil {
		t.Fatalf("Put bob: %v", err)
	}

	if _, err := db.Exec(t.Context(), `
		UPDATE member_progress
		SET updated_at = CASE catalog_item_id
			WHEN $2::uuid THEN now() - interval '2 minutes'
			WHEN $3::uuid THEN now() - interval '1 minute'
			ELSE updated_at
		END
		WHERE user_id = $1::uuid`, alice, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Continue(t.Context(), alice, SurfaceLibrary, 50)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if len(got) != 2 || got[0] != second.ID || got[1] != first.ID {
		t.Fatalf("Continue = %v, want [%s %s]", got, second.ID, first.ID)
	}

	completed := ProgressInput{
		Locator: []byte(`{"page":2,"zoom":1}`), Percent: 1, Completed: true,
	}
	if _, err := repo.Put(t.Context(), alice, second.ID, completed); err != nil {
		t.Fatalf("complete second: %v", err)
	}
	got, err = repo.Continue(t.Context(), alice, SurfaceLibrary, 50)
	if err != nil {
		t.Fatalf("Continue after completion: %v", err)
	}
	if len(got) != 1 || got[0] != first.ID {
		t.Fatalf("Continue after completion = %v, want [%s]", got, first.ID)
	}
}

func TestContinuityRepositoryEnforcesBatchAndContinueLimits(t *testing.T) {
	f := withFixture(t)
	repo := NewContinuityRepository(f.DB)
	userID := createContinuityUser(t, "continuity-limits", f.DB)

	ids := make([]string, 501)
	for i := range ids {
		ids[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
	}
	if _, err := repo.GetMany(t.Context(), userID, ids); !errors.Is(err, errProgressInvalid) {
		t.Fatalf("GetMany over limit error = %v, want errProgressInvalid", err)
	}
	if _, err := repo.GetMany(t.Context(), userID, []string{"not-a-uuid"}); !errors.Is(err, errProgressInvalid) {
		t.Fatalf("GetMany malformed ID error = %v, want errProgressInvalid", err)
	}
	duplicates := make([]string, 501)
	for i := range duplicates {
		duplicates[i] = ids[0]
	}
	if got, err := repo.GetMany(t.Context(), userID, duplicates); err != nil || len(got) != 0 {
		t.Fatalf("deduplicated GetMany = %+v, err=%v", got, err)
	}

	if _, err := f.DB.Exec(t.Context(), `
		WITH items AS (
			INSERT INTO catalog_items (surface, kind)
			SELECT 'stream', 'video' FROM generate_series(1, 51)
			RETURNING id
		)
		INSERT INTO member_progress (user_id, catalog_item_id, updated_at)
		SELECT $1::uuid, id, now() FROM items`, userID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Continue(t.Context(), userID, SurfaceStream, 1000)
	if err != nil {
		t.Fatalf("Continue over limit: %v", err)
	}
	if len(got) != 50 {
		t.Fatalf("Continue returned %d items, want cap of 50", len(got))
	}

	if _, err := repo.Continue(t.Context(), userID, CatalogSurface("bogus"), 10); !errors.Is(err, errProgressInvalid) {
		t.Fatalf("Continue invalid surface error = %v, want errProgressInvalid", err)
	}
}

func TestContinuityContinueUsesCatalogIDTieBreakAtLimitBoundary(t *testing.T) {
	f := withFixture(t)
	repo := NewContinuityRepository(f.DB)
	userID := createContinuityUser(t, "continuity-tie-break", f.DB)
	const total = 51
	if _, err := f.DB.Exec(t.Context(), `
		WITH items AS (
			INSERT INTO catalog_items (id, surface, kind)
			SELECT ('00000000-0000-4000-8000-' || lpad(n::text, 12, '0'))::uuid,
				'stream', 'video'
			FROM generate_series(1, $2) AS n
			RETURNING id
		)
		INSERT INTO member_progress (user_id, catalog_item_id, updated_at)
		SELECT $1::uuid, id, '2026-08-01T12:00:00Z'::timestamptz FROM items`, userID, total); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Continue(t.Context(), userID, SurfaceStream, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("Continue returned %d items, want exact limit 50", len(got))
	}
	for i, id := range got {
		want := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		if id != want {
			t.Fatalf("Continue[%d] = %q, want catalog ID tie-break %q", i, id, want)
		}
	}
	if got[len(got)-1] == "00000000-0000-4000-8000-000000000051" {
		t.Fatal("limit boundary included the 51st catalog ID")
	}
}
