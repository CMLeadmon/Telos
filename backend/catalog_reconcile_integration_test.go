package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestGrimmoryCompleteEnumerationKeepsSuppressedInactiveAliasSeen(t *testing.T) {
	f := withFixture(t)
	ctx := t.Context()
	oldDB, oldCatalog, oldAuthorizer := dbPool, catalogRepo, grimmoryAuthorizer
	dbPool, catalogRepo = f.DB, NewCatalogRepository(f.DB)
	grimmoryAuthorizer, _ = NewGrimmoryAuthorizer(grimmoryAPIResolver{}, []string{"1"})
	t.Cleanup(func() {
		dbPool, catalogRepo, grimmoryAuthorizer = oldDB, oldCatalog, oldAuthorizer
	})

	alias, err := catalogRepo.Observe(ctx, CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "77", LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatalf("seed Grimmory alias: %v", err)
	}
	if _, err := f.DB.Exec(ctx, `
		UPDATE catalog_sources SET active = false
		WHERE catalog_item_id = $1::uuid AND provider = 'grimmory'`, alias.ID); err != nil {
		t.Fatalf("deactivate Grimmory alias: %v", err)
	}
	if _, err := f.DB.Exec(ctx, `
		INSERT INTO catalog_sources
			(catalog_item_id, provider, upstream_id, upstream_library_id, active, available)
		VALUES ($1::uuid, 'jellyfin', 'active-audio-77', 'audiobooks', true, true)`, alias.ID); err != nil {
		t.Fatalf("cut over alias to Jellyfin: %v", err)
	}

	installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":77,"libraryId":1,"metadata":{"title":"Inactive alias"},"primaryFile":{"bookType":"EPUB"}},
			{"id":78,"libraryId":1,"metadata":{"title":"Unsupported"},"primaryFile":{"bookType":"MOBI"}},
			{"id":79,"libraryId":2,"metadata":{"title":"Outside allowlist"},"primaryFile":{"bookType":"EPUB"}}
		]`))
	})

	books, err := fetchGrimmoryBooks(ctx)
	if err != nil {
		t.Fatalf("fetch complete Grimmory enumeration: %v", err)
	}
	if len(books) != 0 {
		t.Fatalf("published books = %+v, want inactive/unsupported/out-of-scope records suppressed", books)
	}
	var aliasAvailable bool
	if err := f.DB.QueryRow(ctx, `
		SELECT available FROM catalog_sources
		WHERE provider = 'grimmory' AND upstream_id = '77'`).Scan(&aliasAvailable); err != nil {
		t.Fatalf("read inactive alias availability: %v", err)
	}
	if !aliasAvailable {
		t.Fatal("complete scan marked a returned inactive Grimmory alias unavailable")
	}
	var omittedSources int
	if err := f.DB.QueryRow(ctx, `
		SELECT count(*) FROM catalog_sources
		WHERE provider = 'grimmory' AND upstream_id IN ('78', '79')`).Scan(&omittedSources); err != nil {
		t.Fatalf("count omitted Grimmory sources: %v", err)
	}
	if omittedSources != 0 {
		t.Fatalf("unsupported or out-of-scope observations created = %d, want zero", omittedSources)
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
	responseBody := ""
	installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(responseBody))
	})

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "truncated", body: `[{`},
		{name: "trailing junk", body: `[] junk`},
		{name: "multiple values", body: `[] []`},
		{name: "oversized", body: `[]` + strings.Repeat(" ", 8<<20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responseBody = tc.body
			if _, err := fetchGrimmoryBooks(context.Background()); err == nil {
				t.Fatal("unsafe enumeration unexpectedly succeeded")
			}
			resolved, err := catalogRepo.Resolve(t.Context(), known.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !resolved.Available {
				t.Fatal("failed enumeration marked a known source unavailable")
			}
		})
	}
}
