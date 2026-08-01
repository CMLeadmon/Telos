package main

import (
	"context"
	"encoding/json"
	"testing"

	"telos-core/testutil"
)

func TestBackfillCatalogReferencesMigratesOnlyResolvableLegacyRows(t *testing.T) {
	f := testutil.Setup(t)
	ctx := t.Context()

	var userID, channelID string
	if err := f.DB.QueryRow(ctx, `
		INSERT INTO users (username, password_hash)
		VALUES ('backfill-member', 'x')
		RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := f.DB.QueryRow(ctx, `
		INSERT INTO channels (name)
		VALUES ('backfill-channel')
		RETURNING id::text`).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	repo := NewCatalogRepository(f.DB)
	book, err := repo.Observe(ctx, CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "42", LibraryID: "library-a",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatalf("observe book: %v", err)
	}
	media, err := repo.Observe(ctx, CatalogObservation{
		Provider: ProviderJellyfin, UpstreamID: "jf-film-7", LibraryID: "movies",
		Surface: SurfaceStream, Kind: "video",
	})
	if err != nil {
		t.Fatalf("observe media: %v", err)
	}
	incompatible, err := repo.Observe(ctx, CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "43", LibraryID: "library-a",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatalf("observe incompatible progress source: %v", err)
	}
	for _, observation := range []CatalogObservation{
		{Provider: ProviderGrimmory, UpstreamID: "77", LibraryID: "library-a", Surface: SurfaceLibrary, Kind: "epub"},
		{Provider: ProviderJellyfin, UpstreamID: "77", LibraryID: "movies", Surface: SurfaceStream, Kind: "video"},
	} {
		if _, err := repo.Observe(ctx, observation); err != nil {
			t.Fatalf("observe ambiguous source: %v", err)
		}
	}

	if _, err := f.DB.Exec(ctx, `
		INSERT INTO book_progress (user_id, book_id, locator, percent)
		VALUES ($1::uuid, 42, '{"cfi":"epubcfi(/6/4)"}'::jsonb, 0.37),
		       ($1::uuid, 43, '{"page":9,"zoom":2}'::jsonb, 42),
		       ($1::uuid, 77, '{"cfi":"ambiguous"}'::jsonb, 0.77),
		       ($1::uuid, 404, '{"cfi":"unresolved"}'::jsonb, 0.81)`, userID); err != nil {
		t.Fatalf("seed progress: %v", err)
	}

	var bookAnnotationID, mediaAnnotationID, fileAnnotationID, ambiguousAnnotationID, unresolvedAnnotationID, canonicalAnnotationID string
	if err := f.DB.QueryRow(ctx, `
		INSERT INTO annotations
			(target_type, target_id, user_id, visibility, locator, selected_text, note)
		VALUES ('book', '42', $1::uuid, 'community', '{"kind":"epub","cfi":"epubcfi(/6/4)"}'::jsonb, 'passage', 'book note')
		RETURNING id::text`, userID).Scan(&bookAnnotationID); err != nil {
		t.Fatalf("seed book annotation: %v", err)
	}
	if err := f.DB.QueryRow(ctx, `
		INSERT INTO annotations (target_type, target_id, user_id, visibility, note)
		VALUES ('book', '77', $1::uuid, 'community', 'ambiguous note')
		RETURNING id::text`, userID).Scan(&ambiguousAnnotationID); err != nil {
		t.Fatalf("seed ambiguous annotation: %v", err)
	}
	if err := f.DB.QueryRow(ctx, `
		INSERT INTO annotations (target_type, target_id, user_id, visibility, note)
		VALUES ('media', 'jf-film-7', $1::uuid, 'private', 'media note')
		RETURNING id::text`, userID).Scan(&mediaAnnotationID); err != nil {
		t.Fatalf("seed media annotation: %v", err)
	}
	if err := f.DB.QueryRow(ctx, `
		INSERT INTO annotations (target_type, target_id, user_id, visibility, locator, selected_text, note)
		VALUES ('file', 'file-legacy-ref', $1::uuid, 'community', '{"page":3}'::jsonb, 'file selection', 'file note')
		RETURNING id::text`, userID).Scan(&fileAnnotationID); err != nil {
		t.Fatalf("seed file annotation: %v", err)
	}
	if err := f.DB.QueryRow(ctx, `
		INSERT INTO annotations (target_type, target_id, user_id, visibility, note)
		VALUES ('media', 'missing-film', $1::uuid, 'community', 'unresolved note')
		RETURNING id::text`, userID).Scan(&unresolvedAnnotationID); err != nil {
		t.Fatalf("seed unresolved annotation: %v", err)
	}
	if err := f.DB.QueryRow(ctx, `
		INSERT INTO annotations (target_type, target_id, user_id, visibility, note)
		VALUES ('book', $2, $1::uuid, 'community', 'already canonical')
		RETURNING id::text`, userID, book.ID).Scan(&canonicalAnnotationID); err != nil {
		t.Fatalf("seed canonical annotation: %v", err)
	}
	if _, err := f.DB.Exec(ctx, `
		INSERT INTO annotation_replies (annotation_id, user_id, body)
		VALUES ($1::uuid, $2::uuid, 'preserve this reply')`, bookAnnotationID, userID); err != nil {
		t.Fatalf("seed reply: %v", err)
	}

	type seededMessage struct {
		kind, ref, snapshot string
	}
	messages := []seededMessage{
		{"library_book", "42", `{"title":"Legacy Book","cover":"/old/book"}`},
		{"stream_film", "jf-film-7", `{"title":"Legacy Film","duration":"91m"}`},
		{"library_book", "77", `{"title":"Ambiguous Book"}`},
		{"library_book", "404", `{"title":"Unresolved Book"}`},
		{"stream_film", media.ID, `{"title":"Already Canonical"}`},
		{"file", "file-ref", `{"title":"Shared File"}`},
	}
	messageIDs := make([]string, 0, len(messages))
	for _, message := range messages {
		var id string
		if err := f.DB.QueryRow(ctx, `
			INSERT INTO messages (channel_id, user_id, content, embed_kind, embed_ref, embed_snapshot)
			VALUES ($1::uuid, $2::uuid, 'embedded item', $3, $4, $5::jsonb)
			RETURNING id::text`, channelID, userID, message.kind, message.ref, message.snapshot).Scan(&id); err != nil {
			t.Fatalf("seed %s message: %v", message.kind, err)
		}
		messageIDs = append(messageIDs, id)
	}

	first, err := BackfillCatalogReferences(ctx, f.DB)
	if err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if want := (CatalogBackfillReport{Progress: 1, Annotations: 2, Embeds: 2, Updated: 5}); first != want {
		t.Fatalf("first report = %+v, want %+v", first, want)
	}
	second, err := BackfillCatalogReferences(ctx, f.DB)
	if err != nil || second.Updated != 0 {
		t.Fatalf("second=%+v err=%v", second, err)
	}

	var locator []byte
	var percent float32
	if err := f.DB.QueryRow(ctx, `
		SELECT locator, percent
		FROM member_progress
		WHERE user_id = $1::uuid AND catalog_item_id = $2::uuid`, userID, book.ID).Scan(&locator, &percent); err != nil {
		t.Fatalf("read migrated progress: %v", err)
	}
	if string(locator) != `{"cfi": "epubcfi(/6/4)"}` || percent != 0.37 {
		t.Fatalf("migrated progress locator=%s percent=%v", locator, percent)
	}
	var unresolvedProgress int
	if err := f.DB.QueryRow(ctx, `
		SELECT count(*) FROM member_progress WHERE user_id = $1::uuid`, userID).Scan(&unresolvedProgress); err != nil {
		t.Fatalf("count migrated progress: %v", err)
	}
	if unresolvedProgress != 1 {
		t.Fatalf("member progress rows = %d, want 1", unresolvedProgress)
	}
	var incompatibleLegacyRows, incompatibleCanonicalRows int
	if err := f.DB.QueryRow(ctx, `SELECT count(*) FROM book_progress WHERE user_id=$1::uuid AND book_id=43 AND percent=42`, userID).Scan(&incompatibleLegacyRows); err != nil {
		t.Fatal(err)
	}
	if err := f.DB.QueryRow(ctx, `SELECT count(*) FROM member_progress WHERE user_id=$1::uuid AND catalog_item_id=$2::uuid`, userID, incompatible.ID).Scan(&incompatibleCanonicalRows); err != nil {
		t.Fatal(err)
	}
	if incompatibleLegacyRows != 1 || incompatibleCanonicalRows != 0 {
		t.Fatalf("incompatible progress legacy/canonical=%d/%d, want preserved legacy only", incompatibleLegacyRows, incompatibleCanonicalRows)
	}

	annotationCases := []struct {
		id, wantTarget, wantVisibility string
	}{
		{bookAnnotationID, book.ID, "community"},
		{mediaAnnotationID, media.ID, "private"},
		{ambiguousAnnotationID, "77", "community"},
		{unresolvedAnnotationID, "missing-film", "community"},
		{canonicalAnnotationID, book.ID, "community"},
	}
	for _, tc := range annotationCases {
		var targetID, visibility string
		if err := f.DB.QueryRow(ctx, `
			SELECT target_id, visibility FROM annotations WHERE id = $1::uuid`, tc.id).Scan(&targetID, &visibility); err != nil {
			t.Fatalf("read annotation %s: %v", tc.id, err)
		}
		if targetID != tc.wantTarget || visibility != tc.wantVisibility {
			t.Errorf("annotation %s target=%q visibility=%q, want %q/%q", tc.id, targetID, visibility, tc.wantTarget, tc.wantVisibility)
		}
	}
	var preservedTarget, preservedVisibility, preservedSelected, preservedNote string
	var preservedLocator []byte
	if err := f.DB.QueryRow(ctx, `
		SELECT target_id, visibility, locator, selected_text, note
		FROM annotations WHERE id=$1::uuid`, bookAnnotationID).Scan(
		&preservedTarget, &preservedVisibility, &preservedLocator, &preservedSelected, &preservedNote); err != nil {
		t.Fatal(err)
	}
	if preservedTarget != book.ID || preservedVisibility != "community" ||
		string(preservedLocator) != `{"cfi": "epubcfi(/6/4)", "kind": "epub"}` ||
		preservedSelected != "passage" || preservedNote != "book note" {
		t.Fatalf("canonicalized annotation changed non-target fields: target=%q visibility=%q locator=%s selected=%q note=%q", preservedTarget, preservedVisibility, preservedLocator, preservedSelected, preservedNote)
	}
	if err := f.DB.QueryRow(ctx, `
		SELECT target_id, visibility, locator, selected_text, note
		FROM annotations WHERE id=$1::uuid`, fileAnnotationID).Scan(
		&preservedTarget, &preservedVisibility, &preservedLocator, &preservedSelected, &preservedNote); err != nil {
		t.Fatal(err)
	}
	if preservedTarget != "file-legacy-ref" || preservedVisibility != "community" ||
		string(preservedLocator) != `{"page": 3}` || preservedSelected != "file selection" || preservedNote != "file note" {
		t.Fatalf("file annotation changed during catalog backfill: target=%q visibility=%q locator=%s selected=%q note=%q", preservedTarget, preservedVisibility, preservedLocator, preservedSelected, preservedNote)
	}
	var replyBody string
	if err := f.DB.QueryRow(ctx, `SELECT body FROM annotation_replies WHERE annotation_id = $1::uuid`, bookAnnotationID).Scan(&replyBody); err != nil {
		t.Fatalf("read preserved reply: %v", err)
	}
	if replyBody != "preserve this reply" {
		t.Fatalf("reply body = %q", replyBody)
	}

	wantRefs := []string{book.ID, media.ID, "77", "404", media.ID, "file-ref"}
	for i, id := range messageIDs {
		var ref string
		var snapshot []byte
		if err := f.DB.QueryRow(ctx, `
			SELECT embed_ref, embed_snapshot FROM messages WHERE id = $1::uuid`, id).Scan(&ref, &snapshot); err != nil {
			t.Fatalf("read message %s: %v", id, err)
		}
		if ref != wantRefs[i] {
			t.Errorf("message %d ref = %q, want %q", i, ref, wantRefs[i])
		}
		var gotSnapshot, wantSnapshot any
		if err := json.Unmarshal(snapshot, &gotSnapshot); err != nil {
			t.Fatalf("decode stored snapshot: %v", err)
		}
		if err := json.Unmarshal([]byte(messages[i].snapshot), &wantSnapshot); err != nil {
			t.Fatalf("decode fixture snapshot: %v", err)
		}
		if got, want := mustJSON(t, gotSnapshot), mustJSON(t, wantSnapshot); got != want {
			t.Errorf("message %d snapshot = %s, want %s", i, got, want)
		}
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON comparison value: %v", err)
	}
	return string(raw)
}

func TestBackfillCatalogReferencesReportsCommittedBatchesBeforeFailure(t *testing.T) {
	f := testutil.Setup(t)
	ctx := t.Context()

	var userID string
	if err := f.DB.QueryRow(ctx, `
		INSERT INTO users (username, password_hash)
		VALUES ('partial-backfill-member', 'x')
		RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	repo := NewCatalogRepository(f.DB)
	for _, upstreamID := range []string{"good-book", "failing-book"} {
		if _, err := repo.Observe(ctx, CatalogObservation{
			Provider: ProviderGrimmory, UpstreamID: upstreamID, LibraryID: "library-a",
			Surface: SurfaceLibrary, Kind: "epub",
		}); err != nil {
			t.Fatalf("observe %s: %v", upstreamID, err)
		}
	}
	if _, err := f.DB.Exec(ctx, `
		INSERT INTO annotations (id, target_type, target_id, user_id, visibility, note)
		SELECT ('00000000-0000-0000-0000-' || lpad(n::text, 12, '0'))::uuid,
			'book', 'good-book', $1::uuid, 'community', 'good batch'
		FROM generate_series(1, 200) AS n`, userID); err != nil {
		t.Fatalf("seed committed batch: %v", err)
	}
	if _, err := f.DB.Exec(ctx, `
		INSERT INTO annotations (id, target_type, target_id, user_id, visibility, note)
		VALUES ('ffffffff-ffff-ffff-ffff-ffffffffffff', 'book', 'failing-book', $1::uuid, 'community', 'fail batch')`, userID); err != nil {
		t.Fatalf("seed failing batch: %v", err)
	}
	if _, err := f.DB.Exec(ctx, `
		ALTER TABLE annotations ADD CONSTRAINT task5_fail_later_annotation_batch
		CHECK (note <> 'fail batch' OR target_id !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')`); err != nil {
		t.Fatalf("install failure constraint: %v", err)
	}
	t.Cleanup(func() {
		f.DB.Exec(context.Background(), `ALTER TABLE annotations DROP CONSTRAINT IF EXISTS task5_fail_later_annotation_batch`)
	})

	report, err := BackfillCatalogReferences(ctx, f.DB)
	if err == nil {
		t.Fatal("backfill succeeded despite the forced later-batch failure")
	}
	if report.Annotations != 200 || report.Updated != 200 {
		t.Fatalf("report after committed batch = %+v, want annotations=200 updated=200", report)
	}
	var canonicalized int
	if err := f.DB.QueryRow(ctx, `
		SELECT count(*) FROM annotations
		WHERE note = 'good batch' AND target_id <> 'good-book'`).Scan(&canonicalized); err != nil {
		t.Fatalf("count committed rows: %v", err)
	}
	if canonicalized != 200 {
		t.Fatalf("committed canonical rows = %d, want 200", canonicalized)
	}
}
