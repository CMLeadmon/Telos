package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func annotationFixture(t *testing.T) (*pgxpool.Pool, string, string) {
	t.Helper()
	f := withFixture(t)
	old, oldCatalog := dbPool, catalogRepo
	dbPool = f.DB
	catalogRepo = NewCatalogRepository(f.DB)
	t.Cleanup(func() {
		dbPool = old
		catalogRepo = oldCatalog
	})
	mk := func(n string) string {
		var id string
		f.DB.QueryRow(context.Background(), `INSERT INTO users (username,password_hash,active) VALUES ($1,'x',TRUE) RETURNING id::text`, n).Scan(&id)
		return id
	}
	if _, err := catalogRepo.Observe(t.Context(), CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "42", LibraryID: "library-a",
		Surface: SurfaceLibrary, Kind: "epub",
	}); err != nil {
		t.Fatalf("observe annotation book: %v", err)
	}
	if _, err := catalogRepo.Observe(t.Context(), CatalogObservation{
		Provider: ProviderJellyfin, UpstreamID: "jf-item-1", LibraryID: "movies",
		Surface: SurfaceStream, Kind: "video",
	}); err != nil {
		t.Fatalf("observe annotation media: %v", err)
	}
	return f.DB, mk("owner"), mk("reader")
}

var epubLoc = json.RawMessage(`{"kind":"epub","cfi":"epubcfi(/6/4!/4)"}`)

func TestAnnotationPrivateDefaultAndIsolation(t *testing.T) {
	_, owner, reader := annotationFixture(t)
	ctx := context.Background()

	// A default annotation is private and invisible to other users.
	a, err := CreateAnnotation(ctx, "book", "42", owner, "", epubLoc, "selected", "note")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.Visibility != "private" {
		t.Fatalf("default visibility = %q, want private", a.Visibility)
	}
	got, _ := ListAnnotations(ctx, "book", a.TargetID, reader, 100)
	if len(got) != 0 {
		t.Fatal("cross-user private annotation leaked")
	}
	mine, _ := ListAnnotations(ctx, "book", a.TargetID, owner, 100)
	if len(mine) != 1 {
		t.Fatalf("owner sees %d, want 1", len(mine))
	}

	// Transitioning to community makes it visible to everyone.
	if err := UpdateAnnotation(ctx, a.ID, owner, "community", "note2"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = ListAnnotations(ctx, "book", a.TargetID, reader, 100)
	if len(got) != 1 || got[0].Visibility != "community" {
		t.Fatalf("community annotation not enumerated: %+v", got)
	}

	// A non-owner cannot mutate it.
	if err := UpdateAnnotation(ctx, a.ID, reader, "private", "hijack"); err != errAnnotationNotFound {
		t.Fatalf("cross-user update = %v, want not-found", err)
	}
}

func TestAnnotationsGeneralizeAcrossTargets(t *testing.T) {
	_, owner, reader := annotationFixture(t)
	ctx := context.Background()

	// A comment on a streamed media item and a file coexist independently of
	// any book, each scoped to its own (type, id).
	empty := json.RawMessage(`{}`)
	mediaAnnotation, err := CreateAnnotation(ctx, "media", "jf-item-1", owner, "community", empty, "", "great scene")
	if err != nil {
		t.Fatalf("media comment: %v", err)
	}
	if _, err := CreateAnnotation(ctx, "file", "file-uuid-1", owner, "community", empty, "", "handy doc"); err != nil {
		t.Fatalf("file comment: %v", err)
	}
	bookAnnotation, err := CreateAnnotation(ctx, "book", "42", owner, "community", epubLoc, "s", "n")
	if err != nil {
		t.Fatalf("book annotation: %v", err)
	}

	media, _ := ListAnnotations(ctx, "media", mediaAnnotation.TargetID, reader, 100)
	if len(media) != 1 || media[0].TargetType != "media" || media[0].Note != "great scene" {
		t.Fatalf("media listing wrong: %+v", media)
	}
	files, _ := ListAnnotations(ctx, "file", "file-uuid-1", reader, 100)
	if len(files) != 1 || files[0].TargetType != "file" {
		t.Fatalf("file listing wrong: %+v", files)
	}
	book, _ := ListAnnotations(ctx, "book", bookAnnotation.TargetID, reader, 100)
	if len(book) != 1 || book[0].TargetType != "book" {
		t.Fatalf("book listing crossed target types: %+v", book)
	}

	// An unknown target type is rejected.
	if _, err := CreateAnnotation(ctx, "podcast", "x", owner, "community", empty, "", "n"); err == nil {
		t.Fatal("unknown target type accepted")
	}
}

func TestAnnotationRepliesOnlyOnCommunityAndNotify(t *testing.T) {
	db, owner, reader := annotationFixture(t)
	ctx := context.Background()
	priv, _ := CreateAnnotation(ctx, "book", "42", owner, "private", epubLoc, "s", "n")

	// No reply on a private annotation.
	if _, err := CreateReply(ctx, priv.ID, reader, "hi"); err != errReplyOnlyCommunity {
		t.Fatalf("reply on private = %v, want only-community", err)
	}

	comm, _ := CreateAnnotation(ctx, "book", "42", owner, "community", epubLoc, "s", "n")
	reply, err := CreateReply(ctx, comm.ID, reader, "great highlight")
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	// The owner gets an annotation_reply user event; the payload holds no bodies.
	var kind string
	var payload []byte
	if err := db.QueryRow(ctx, `SELECT kind, payload FROM user_events WHERE recipient_id=$1 AND kind='annotation_reply'`, owner).Scan(&kind, &payload); err != nil {
		t.Fatalf("owner not notified: %v", err)
	}
	if string(payload) == "" || strings.Contains(string(payload), "great highlight") {
		t.Fatalf("user event payload leaked the reply body: %s", payload)
	}
	// Replies list the new reply.
	replies, _ := ListReplies(ctx, comm.ID, 100)
	if len(replies) != 1 || replies[0].ID != reply.ID {
		t.Fatalf("reply listing wrong: %+v", replies)
	}
}

func TestModerateAnnotationRequiresPermission(t *testing.T) {
	db, owner, reader := annotationFixture(t)
	ctx := context.Background()
	comm, _ := CreateAnnotation(ctx, "book", "42", owner, "community", epubLoc, "s", "n")

	// A plain reader cannot moderate.
	nobody := &UserContext{ID: reader, Roles: []string{}}
	if err := DeleteAnnotation(ctx, comm.ID, nobody); err != errAnnotationDenied {
		t.Fatalf("non-moderator delete = %v, want denied", err)
	}
	// An Owner-role moderator can (Owner bypasses permission checks).
	mod := &UserContext{ID: reader, Roles: []string{"Owner"}}
	if err := DeleteAnnotation(ctx, comm.ID, mod); err != nil {
		t.Fatalf("moderator delete: %v", err)
	}
	var n int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM annotations WHERE id=$1`, comm.ID).Scan(&n)
	if n != 0 {
		t.Fatal("moderated annotation was not removed")
	}
}

func TestDeleteAccountAnnotations(t *testing.T) {
	db, owner, _ := annotationFixture(t)
	ctx := context.Background()
	CreateAnnotation(ctx, "book", "42", owner, "private", epubLoc, "s", "n")
	CreateAnnotation(ctx, "book", "42", owner, "community", epubLoc, "s", "n")

	if _, err := DeleteAccount(ctx, owner); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	var priv, comm int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM annotations WHERE user_id=$1 AND visibility='private'`, owner).Scan(&priv)
	db.QueryRow(ctx, `SELECT COUNT(*) FROM annotations WHERE user_id=$1 AND visibility='community'`, owner).Scan(&comm)
	if priv != 0 {
		t.Fatalf("private annotations retained after deletion: %d", priv)
	}
	if comm != 1 {
		t.Fatalf("community annotation should be retained (anonymized), got %d", comm)
	}
}

func TestCreateAnnotationStoresCanonicalCatalogTarget(t *testing.T) {
	db, owner, _ := annotationFixture(t)
	const canonicalID = "00000000-0000-4000-8000-000000000042"
	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		if rawID != "42" || surface != SurfaceLibrary {
			t.Fatalf("resolve target = %q/%q, want 42/library", rawID, surface)
		}
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceLibrary, Kind: "epub",
			Provider: ProviderGrimmory, UpstreamID: "42", LibraryID: "library-a",
			Active: true, Available: true,
		}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })

	created, err := CreateAnnotation(t.Context(), "book", "42", owner, "community", epubLoc, "passage", "note")
	if err != nil {
		t.Fatalf("create annotation: %v", err)
	}
	if created.TargetID != canonicalID {
		t.Fatalf("returned target id = %q, want %q", created.TargetID, canonicalID)
	}
	var storedTarget string
	if err := db.QueryRow(t.Context(), `SELECT target_id FROM annotations WHERE id = $1::uuid`, created.ID).Scan(&storedTarget); err != nil {
		t.Fatalf("read stored annotation: %v", err)
	}
	if storedTarget != canonicalID {
		t.Fatalf("stored target id = %q, want %q", storedTarget, canonicalID)
	}
}

func TestCommentCreateResolvesBeforeAuthorization(t *testing.T) {
	_, owner, _ := annotationFixture(t)
	resolved := false
	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		resolved = true
		if rawID != "legacy-film" || surface != SurfaceStream {
			t.Fatalf("resolve target = %q/%q, want legacy-film/stream", rawID, surface)
		}
		return CatalogResolution{
			ID: "00000000-0000-4000-8000-000000000077", Surface: SurfaceStream,
			Kind: "video", Provider: ProviderJellyfin, UpstreamID: "current-film",
			LibraryID: "movies", Active: true, Available: true,
		}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/items/legacy-film/comments", strings.NewReader(`{"visibility":"community","note":"hello"}`))
	req.SetPathValue("id", "legacy-film")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: owner}))
	rec := httptest.NewRecorder()
	commentCreateHandler("media").ServeHTTP(rec, req)

	if !resolved {
		t.Fatal("target authorization ran before catalog resolution")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want target denial hidden as 404", rec.Code)
	}
}

func TestAuthorizeAnnotationTargetCanonicalizesLegacyRow(t *testing.T) {
	db, owner, _ := annotationFixture(t)
	const canonicalID = "00000000-0000-4000-8000-000000000088"
	var annotationID string
	if err := db.QueryRow(t.Context(), `
		INSERT INTO annotations (target_type, target_id, user_id, visibility, note)
		VALUES ('book', '88', $1::uuid, 'community', 'keep this note')
		RETURNING id::text`, owner).Scan(&annotationID); err != nil {
		t.Fatalf("seed legacy annotation: %v", err)
	}
	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		if rawID != "88" || surface != SurfaceLibrary {
			t.Fatalf("resolve target = %q/%q, want 88/library", rawID, surface)
		}
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceLibrary, Kind: "epub",
			Provider: ProviderGrimmory, UpstreamID: "88", LibraryID: "library-a",
			Active: true, Available: true,
		}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/library/annotations/"+annotationID, nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: owner, Roles: []string{"Owner"}}))
	rec := httptest.NewRecorder()
	targetType, targetID, ok := authorizeAnnotationTarget(rec, req, annotationID)
	if !ok {
		t.Fatalf("authorization failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if targetType != "book" || targetID != canonicalID {
		t.Fatalf("authorized target = %q/%q, want book/%q", targetType, targetID, canonicalID)
	}
	var storedTarget, visibility, note string
	if err := db.QueryRow(t.Context(), `
		SELECT target_id, visibility, note FROM annotations WHERE id = $1::uuid`, annotationID).Scan(&storedTarget, &visibility, &note); err != nil {
		t.Fatalf("read canonicalized annotation: %v", err)
	}
	if storedTarget != canonicalID || visibility != "community" || note != "keep this note" {
		t.Fatalf("stored annotation = target:%q visibility:%q note:%q", storedTarget, visibility, note)
	}
}

func TestBookAnnotationCreateResolvesBeforeAuthorization(t *testing.T) {
	_, owner, _ := annotationFixture(t)
	resolved := false
	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		resolved = true
		if rawID != "99" || surface != SurfaceLibrary {
			t.Fatalf("resolve target = %q/%q, want 99/library", rawID, surface)
		}
		return CatalogResolution{
			ID: "00000000-0000-4000-8000-000000000099", Surface: SurfaceLibrary,
			Kind: "epub", Provider: ProviderGrimmory, UpstreamID: "99",
			LibraryID: "library-a", Active: true, Available: true,
		}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })
	oldAuthorizer := grimmoryAuthorizer
	grimmoryAuthorizer, _ = NewGrimmoryAuthorizer(grimmoryAPIResolver{}, []string{"library-a"})
	t.Cleanup(func() { grimmoryAuthorizer = oldAuthorizer })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/books/99/annotations", strings.NewReader(`{"visibility":"community","locator":{},"note":"hello"}`))
	req.SetPathValue("id", "99")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: owner}))
	rec := httptest.NewRecorder()
	handleCreateAnnotation(rec, req)

	if !resolved {
		t.Fatal("book authorization ran before catalog resolution")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestMediaCommentaryHidesUnavailableResolvedTarget(t *testing.T) {
	db, owner, _ := annotationFixture(t)
	const canonicalID = "00000000-0000-4000-8000-000000000177"
	if _, err := db.Exec(t.Context(), `
		INSERT INTO annotations (target_type, target_id, user_id, visibility, note)
		VALUES ('media', $1, $2::uuid, 'community', 'private catalog commentary')`, canonicalID, owner); err != nil {
		t.Fatalf("seed media commentary: %v", err)
	}
	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(context.Context, string, CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceStream, Kind: "video",
			Provider: ProviderJellyfin, UpstreamID: "unavailable-film", LibraryID: "movies",
			Active: true, Available: false,
		}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/items/"+canonicalID+"/comments", nil)
	req.SetPathValue("id", canonicalID)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: owner, Roles: []string{"Owner"}}))
	rec := httptest.NewRecorder()
	commentListHandler("media").ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want unavailable target hidden as 404", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "private catalog commentary") {
		t.Fatal("unavailable target leaked commentary")
	}
}

func TestMediaCommentaryAppliesTightenedProviderAllowlist(t *testing.T) {
	_, owner, _ := annotationFixture(t)
	const canonicalID = "00000000-0000-4000-8000-000000000178"
	oldResolve, oldAuthorizer := resolveCatalogIdentity, jellyfinAuthorizer
	resolveCatalogIdentity = func(context.Context, string, CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceStream, Kind: "video",
			Provider: ProviderJellyfin, UpstreamID: "moved-film", LibraryID: "removed-library",
			Active: true, Available: true,
		}, nil
	}
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(&fakeJellyfinResolver{items: map[string]resolvedItem{
		"moved-film": {AncestorIDs: []string{"removed-library"}},
	}}, []string{"current-library"})
	t.Cleanup(func() {
		resolveCatalogIdentity, jellyfinAuthorizer = oldResolve, oldAuthorizer
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/items/"+canonicalID+"/comments", nil)
	req.SetPathValue("id", canonicalID)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: owner, Roles: []string{"Owner"}}))
	rec := httptest.NewRecorder()
	commentListHandler("media").ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want tightened allowlist hidden as 404", rec.Code, rec.Body.String())
	}
}
