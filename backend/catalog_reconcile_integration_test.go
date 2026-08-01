package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func installGrimmoryEnumerationFixture(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "reconcile-token", "expires": 7200})
	})
	mux.HandleFunc("GET /api/v1/books", handler)
	server := httptest.NewServer(mux)

	oldURL := grimmoryBaseURL
	grimmoryBaseURL = server.URL
	t.Setenv("GRIMMORY_ADMIN_USER", "gateway")
	t.Setenv("GRIMMORY_ADMIN_PASSWORD", "secret")
	grimmoryTok.mu.Lock()
	oldToken, oldExpiry := grimmoryTok.token, grimmoryTok.expiresAt
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()
	t.Cleanup(func() {
		server.Close()
		grimmoryBaseURL = oldURL
		grimmoryTok.mu.Lock()
		grimmoryTok.token, grimmoryTok.expiresAt = oldToken, oldExpiry
		grimmoryTok.mu.Unlock()
	})
}

func TestGrimmoryCompleteEnumerationScansThenBackfillsReferences(t *testing.T) {
	f := withFixture(t)
	ctx := t.Context()
	oldDB, oldCatalog, oldAuthorizer := dbPool, catalogRepo, grimmoryAuthorizer
	dbPool, catalogRepo = f.DB, NewCatalogRepository(f.DB)
	grimmoryAuthorizer, _ = NewGrimmoryAuthorizer(grimmoryAPIResolver{}, []string{"1"})
	t.Cleanup(func() {
		dbPool, catalogRepo, grimmoryAuthorizer = oldDB, oldCatalog, oldAuthorizer
	})

	missing, err := catalogRepo.Observe(ctx, CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "99", LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "pdf",
	})
	if err != nil {
		t.Fatalf("seed previously visible source: %v", err)
	}
	var userID string
	if err := f.DB.QueryRow(ctx, `
		INSERT INTO users (username, password_hash)
		VALUES ('reconcile-reader', 'x') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if _, err := f.DB.Exec(ctx, `
		INSERT INTO book_progress (user_id, book_id, locator, percent)
		VALUES ($1::uuid, 42, '{"cfi":"legacy","fraction":0.4}'::jsonb, 0.4)`, userID); err != nil {
		t.Fatalf("seed legacy progress: %v", err)
	}

	installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":42,"libraryId":1,"metadata":{"title":"Observed"},"primaryFile":{"bookType":"EPUB"}}]`))
	})

	books, err := fetchGrimmoryBooks(ctx)
	if err != nil {
		t.Fatalf("fetch complete enumeration: %v", err)
	}
	if len(books) != 1 || !looksLikeUUID(books[0].ID) {
		t.Fatalf("books = %+v, want one canonical book", books)
	}
	missingNow, err := catalogRepo.Resolve(ctx, missing.ID)
	if err != nil {
		t.Fatalf("resolve missing source: %v", err)
	}
	if missingNow.Available {
		t.Fatal("complete enumeration did not mark the unseen source unavailable")
	}
	var canonicalProgress int
	if err := f.DB.QueryRow(ctx, `
		SELECT count(*)
		FROM member_progress AS progress
		JOIN catalog_sources AS source ON source.catalog_item_id = progress.catalog_item_id
		WHERE progress.user_id = $1::uuid
		  AND source.provider = 'grimmory' AND source.upstream_id = '42'`, userID).Scan(&canonicalProgress); err != nil {
		t.Fatalf("count canonical progress: %v", err)
	}
	if canonicalProgress != 1 {
		t.Fatalf("canonical progress rows = %d, want backfill after observation", canonicalProgress)
	}
}

func TestGrimmoryFailedEnumerationDoesNotCompleteScan(t *testing.T) {
	f := withFixture(t)
	oldDB, oldCatalog, oldAuthorizer := dbPool, catalogRepo, grimmoryAuthorizer
	dbPool, catalogRepo = f.DB, NewCatalogRepository(f.DB)
	grimmoryAuthorizer, _ = NewGrimmoryAuthorizer(grimmoryAPIResolver{}, []string{"1"})
	t.Cleanup(func() {
		dbPool, catalogRepo, grimmoryAuthorizer = oldDB, oldCatalog, oldAuthorizer
	})
	known, err := catalogRepo.Observe(t.Context(), CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "known", LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatal(err)
	}
	installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{`))
	})

	if _, err := fetchGrimmoryBooks(context.Background()); err == nil {
		t.Fatal("malformed enumeration unexpectedly succeeded")
	}
	resolved, err := catalogRepo.Resolve(t.Context(), known.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Available {
		t.Fatal("failed enumeration marked a known source unavailable")
	}
}
