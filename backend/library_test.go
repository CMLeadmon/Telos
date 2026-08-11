package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupManagementGrimmory(t *testing.T, upstream http.HandlerFunc) func() {
	t.Helper()
	installCatalogIdentityTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accessToken": "manage-token", "expires": 7200,
		})
	})
	mux.HandleFunc("/", upstream)
	server := httptest.NewServer(mux)
	oldURL := grimmoryBaseURL
	oldRedis := redisClient
	grimmoryBaseURL = server.URL
	redisClient = nil
	grimmoryTok.mu.Lock()
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()
	return func() {
		server.Close()
		grimmoryBaseURL = oldURL
		redisClient = oldRedis
		grimmoryTok.mu.Lock()
		grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
		grimmoryTok.mu.Unlock()
	}
}

// fakeGrimmory returns a test server that accepts login for gw/pw and
// serves authed endpoints, counting logins.
func fakeGrimmory(t *testing.T, logins *int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		json.NewDecoder(r.Body).Decode(&body)
		if body.Username != "gw" || body.Password != "pw" {
			http.Error(w, "bad creds", http.StatusUnauthorized)
			return
		}
		*logins++
		json.NewEncoder(w).Encode(map[string]any{
			"accessToken": "tok-1", "expires": 7200, "refreshToken": "r",
		})
	})
	mux.HandleFunc("GET /api/v1/books", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"addedOn":"2026-07-12T20:09:10Z","id":1,"libraryId":1,"libraryName":"Books",
			"metadata":{"bookId":1,"title":"Pride and Prejudice","language":"en",
				"authors":["Jane Austen"],"categories":["Love stories","England -- Fiction"]},
			"primaryFile":{"bookType":"EPUB","extension":"epub","fileName":"pp.epub","fileSizeKb":24253}}]`))
	})
	mux.HandleFunc("GET /api/v1/books/1", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":1,"libraryId":1,"libraryName":"Books","metadata":{"title":"Pride and Prejudice","authors":["Jane Austen"]},"primaryFile":{"bookType":"EPUB"}}`)
	})
	mux.HandleFunc("GET /api/v1/media/book/1/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Grimmory mislabels JPEG bytes as JSON — reproduce that.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("\xff\xd8\xff\xe0fake-jpeg"))
	})
	mux.HandleFunc("GET /api/v1/books/1/content", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/epub+zip")
		w.Write([]byte("PK\x03\x04fake-epub"))
	})
	return httptest.NewServer(mux)
}

func setupGrimmoryTest(t *testing.T) func() {
	t.Helper()
	installCatalogIdentityTest(t)
	logins := 0
	srv := fakeGrimmory(t, &logins)
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = srv.URL
	os.Setenv("GRIMMORY_ADMIN_USER", "gw")
	os.Setenv("GRIMMORY_ADMIN_PASSWORD", "pw")
	grimmoryTok.mu.Lock()
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()
	return func() { grimmoryBaseURL = oldURL; srv.Close() }
}

func setupCurrentGrimmoryDetails(t *testing.T, formats map[string]string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"accessToken": "detail-token", "expires": 7200})
	})
	mux.HandleFunc("GET /api/v1/books/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		format, ok := formats[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"id":%s,"libraryId":1,"metadata":{"title":"Book %s"},"primaryFile":{"bookType":%q}}`, id, id, format)
	})
	server := httptest.NewServer(mux)
	oldURL, oldRedis := grimmoryBaseURL, redisClient
	grimmoryBaseURL, redisClient = server.URL, nil
	grimmoryTok.mu.Lock()
	oldToken, oldExpiry := grimmoryTok.token, grimmoryTok.expiresAt
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()
	t.Cleanup(func() {
		server.Close()
		grimmoryBaseURL, redisClient = oldURL, oldRedis
		grimmoryTok.mu.Lock()
		grimmoryTok.token, grimmoryTok.expiresAt = oldToken, oldExpiry
		grimmoryTok.mu.Unlock()
	})
}

func TestHandleLibraryBooksReturnsCanonicalID(t *testing.T) {
	defer setupGrimmoryTest(t)()
	f := withFixture(t)
	oldContinuity := continuityRepo
	continuityRepo = NewContinuityRepository(f.DB)
	t.Cleanup(func() { continuityRepo = oldContinuity })
	var userID string
	if err := f.DB.QueryRow(t.Context(), `
		INSERT INTO users (username, password_hash)
		VALUES ('library-catalog-reader', 'x') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/library/books", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: userID}))
	rec := httptest.NewRecorder()
	handleLibraryBooks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var books []LibraryItem
	if err := json.Unmarshal(rec.Body.Bytes(), &books); err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 || books[0].Title != "Pride and Prejudice" ||
		books[0].Kind != "epub" || books[0].Format != "EPUB" || books[0].Authors[0] != "Jane Austen" {
		t.Fatalf("bad translation: %+v", books)
	}
	if !looksLikeUUID(fmt.Sprint(books[0].ID)) {
		t.Fatalf("public id = %q, want canonical UUID", fmt.Sprint(books[0].ID))
	}
}

func TestFreshLegacyGrimmoryDeepLinkDiscoversSupportedCurrentBook(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000012"
	var observed CatalogObservation
	providerPaths := []string{}
	cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		providerPaths = append(providerPaths, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/books/12":
			io.WriteString(w, `{"id":12,"libraryId":1,"metadata":{"title":"Fresh book"},"primaryFile":{"bookType":"EPUB"}}`)
		case "/api/v1/books/12/content":
			io.WriteString(w, "epub")
		default:
			http.NotFound(w, r)
		}
	})
	defer cleanup()
	oldObserve, oldResolve := observeCatalogIdentity, resolveCatalogIdentity
	observeCatalogIdentity = func(_ context.Context, in CatalogObservation) (CatalogResolution, error) {
		observed = in
		return CatalogResolution{ID: canonicalID, Provider: in.Provider, UpstreamID: in.UpstreamID,
			LibraryID: in.LibraryID, Surface: in.Surface, Kind: in.Kind, Active: true, Available: true}, nil
	}
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		if rawID == "12" {
			return CatalogResolution{}, errCatalogNotFound
		}
		if rawID == canonicalID {
			return CatalogResolution{ID: canonicalID, Provider: ProviderGrimmory, UpstreamID: "12",
				LibraryID: "1", Surface: surface, Kind: "epub", Active: true, Available: true}, nil
		}
		return CatalogResolution{}, errors.New("unexpected catalog identifier")
	}
	t.Cleanup(func() { observeCatalogIdentity, resolveCatalogIdentity = oldObserve, oldResolve })
	oldAuthorizer := grimmoryAuthorizer
	grimmoryAuthorizer, _ = NewGrimmoryAuthorizer(grimmoryAPIResolver{}, []string{"1"})
	t.Cleanup(func() { grimmoryAuthorizer = oldAuthorizer })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/library/books/12/content", nil)
	req.SetPathValue("id", "12")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{Roles: []string{"Owner"}}))
	rec := httptest.NewRecorder()
	handleLibraryBookContent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if observed.Provider != ProviderGrimmory || observed.UpstreamID != "12" || observed.LibraryID != "1" || observed.Kind != "epub" {
		t.Fatalf("observation=%+v, want current supported Grimmory source", observed)
	}
	for _, path := range providerPaths {
		if strings.Contains(path, canonicalID) {
			t.Fatalf("canonical UUID reached Grimmory: %q", path)
		}
	}
}

func TestHandleLibraryFacetsDerives(t *testing.T) {
	defer setupGrimmoryTest(t)()
	req := httptest.NewRequest("GET", "/api/v1/library/facets", nil)
	rec := httptest.NewRecorder()
	handleLibraryFacets(rec, req)
	var f LibraryFacets
	if err := json.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Authors) != 1 || f.Authors[0].Value != "Jane Austen" || f.Authors[0].Count != 1 {
		t.Fatalf("bad authors facet: %+v", f.Authors)
	}
	if len(f.Categories) != 2 {
		t.Fatalf("expected 2 categories, got %+v", f.Categories)
	}
}

func TestHandleLibraryBookCoverForcesJPEG(t *testing.T) {
	defer setupGrimmoryTest(t)()
	req := httptest.NewRequest("GET", "/api/v1/library/books/1/cover", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	handleLibraryBookCover(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("expected image/jpeg (upstream mislabels), got %q", ct)
	}
}

func TestHandleLibraryBookContentStreams(t *testing.T) {
	defer setupGrimmoryTest(t)()
	req := httptest.NewRequest("GET", "/api/v1/library/books/1/content", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	handleLibraryBookContent(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "application/epub+zip" {
		t.Fatalf("expected epub content-type passthrough, got %q", ct)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("PK")) {
		t.Fatal("expected epub bytes")
	}
}

func TestCanonicalLibraryBookContentUsesLegacyGrimmoryID(t *testing.T) {
	defer setupGrimmoryTest(t)()
	resolution, err := observeCatalogIdentity(t.Context(), CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "1", LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/library/books/"+resolution.ID+"/content", nil)
	req.SetPathValue("id", resolution.ID)
	rec := httptest.NewRecorder()
	handleLibraryBookContent(rec, req)
	if rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte("PK")) {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.Bytes())
	}
}

func TestLibraryBooksReturnsUnavailable(t *testing.T) {
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = "http://127.0.0.1:1" // unreachable
	defer func() { grimmoryBaseURL = oldURL }()
	grimmoryTok.mu.Lock()
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()
	req := httptest.NewRequest("GET", "/api/v1/library/books", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: "00000000-0000-4000-8000-000000000001"}))
	rec := httptest.NewRecorder()
	handleLibraryBooks(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable upstream should serve 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetGrimmoryTokenLogsInAndCaches(t *testing.T) {
	logins := 0
	srv := fakeGrimmory(t, &logins)
	defer srv.Close()
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = srv.URL
	defer func() { grimmoryBaseURL = oldURL }()
	os.Setenv("GRIMMORY_ADMIN_USER", "gw")
	os.Setenv("GRIMMORY_ADMIN_PASSWORD", "pw")
	grimmoryTok.mu.Lock()
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()

	tok, err := getGrimmoryToken(context.Background())
	if err != nil || tok != "tok-1" {
		t.Fatalf("got %q, %v; want tok-1", tok, err)
	}
	if _, err := getGrimmoryToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logins != 1 {
		t.Fatalf("expected 1 login (cached second call), got %d", logins)
	}
}

func TestGrimmoryGETAttachesBearer(t *testing.T) {
	logins := 0
	srv := fakeGrimmory(t, &logins)
	defer srv.Close()
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = srv.URL
	defer func() { grimmoryBaseURL = oldURL }()
	os.Setenv("GRIMMORY_ADMIN_USER", "gw")
	os.Setenv("GRIMMORY_ADMIN_PASSWORD", "pw")
	grimmoryTok.mu.Lock()
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()

	resp, err := grimmoryGET(context.Background(), "/api/v1/books")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestValidateProgress(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"valid", `{"locator":{"cfi":"epubcfi(/6/4!/4)","fraction":0.42},"percent":0.42}`, true},
		{"percent out of range", `{"locator":{},"percent":1.5}`, false},
		{"negative percent", `{"locator":{},"percent":-0.1}`, false},
		{"not json", `nope`, false},
		{"oversized locator", `{"locator":{"pad":"` + strings.Repeat("x", 5000) + `"},"percent":0}`, false},
	}
	for _, c := range cases {
		_, _, err := validateProgress([]byte(c.in))
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestValidateBookProgressFormatSpecific(t *testing.T) {
	cases := []struct {
		name, format, in string
		ok               bool
	}{
		// EPUB
		{"EPUBProgress valid", "EPUB", `{"locator":{"cfi":"epubcfi(/6/4!/4)","fraction":0.4},"percent":0.4}`, true},
		{"EPUBProgress empty cfi", "EPUB", `{"locator":{"cfi":"","fraction":0.4},"percent":0.4}`, false},
		{"EPUBProgress fraction oob", "EPUB", `{"locator":{"cfi":"x","fraction":1.4},"percent":0.4}`, false},
		{"EPUBProgress mixed pdf fields", "EPUB", `{"locator":{"cfi":"x","fraction":0.4,"page":2},"percent":0.4}`, false},
		// PDF
		{"PDFProgress valid", "PDF", `{"locator":{"page":12,"zoom":1.5},"percent":0.3}`, true},
		{"PDFProgress page zero", "PDF", `{"locator":{"page":0,"zoom":1},"percent":0.3}`, false},
		{"PDFProgress fractional page", "PDF", `{"locator":{"page":2.5,"zoom":1},"percent":0.3}`, false},
		{"PDFProgress zoom oob", "PDF", `{"locator":{"page":1,"zoom":99},"percent":0.3}`, false},
		{"PDFProgress mixed epub fields", "PDF", `{"locator":{"page":1,"zoom":1,"cfi":"x"},"percent":0.3}`, false},
		// Unknown format
		{"BookProgress unknown format", "mobi", `{"locator":{"page":1,"zoom":1},"percent":0.3}`, false},
	}
	for _, c := range cases {
		_, _, err := validateBookProgress(c.format, []byte(c.in))
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestGrimmoryCatalogKindDoesNotMislabelUnknownFormats(t *testing.T) {
	for _, format := range []string{"EPUB", "PDF", "AUDIOBOOK"} {
		if _, ok := grimmoryCatalogKind(format); !ok {
			t.Fatalf("supported format %q rejected", format)
		}
	}
	if kind, ok := grimmoryCatalogKind("MOBI"); ok || kind != "" {
		t.Fatalf("MOBI mapped to %q, want unsupported", kind)
	}
}

// serveGrimmoryEnumeration installs a Grimmory whose /api/v1/books returns the
// given document, and disables the library allowlist so these tests exercise
// only the canonical-identity gate.
func serveGrimmoryEnumeration(t *testing.T, document string) {
	t.Helper()
	cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/books" {
			io.WriteString(w, document)
			return
		}
		http.NotFound(w, r)
	})
	oldAuthorizer := grimmoryAuthorizer
	grimmoryAuthorizer = nil
	t.Cleanup(func() {
		grimmoryAuthorizer = oldAuthorizer
		cleanup()
	})
}

func TestGrimmoryEnumerationSuppressesInactiveAliasAfterStreamCutover(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000211"
	serveGrimmoryEnumeration(t, `[{"id":211,"libraryId":1,"metadata":{"title":"Cut over"},"primaryFile":{"bookType":"EPUB"}}]`)
	reconcileCatalogEnumeration = func(_ context.Context, enumeration CatalogEnumeration) (CatalogReconciliationReport, error) {
		return CatalogReconciliationReport{
			Observed: len(enumeration.Observations),
			Scans:    map[string]CatalogScanReport{},
			// Deliberately flagged active and available: the reconciliation only
			// reports the source it just observed, so this is what a healthy
			// observation of a cut-over book looks like. Suppression must come
			// from the active-source lookup below and nowhere else, or the test
			// would pass without exercising the gate at all.
			Resolutions: map[string]CatalogResolution{"211": {
				ID: canonicalID, Surface: SurfaceLibrary, Kind: "epub",
				Provider: ProviderGrimmory, UpstreamID: "211", LibraryID: "1",
				Active: true, Available: true,
			}},
		}, nil
	}
	// The item's active source moved to Stream, so a Library-surface resolve
	// finds no active library source for it — absence must read as a denial.
	resolveCatalogIdentitiesFor = func(context.Context, []string, CatalogSurface) (map[string]CatalogResolution, error) {
		return map[string]CatalogResolution{}, nil
	}

	books, err := fetchGrimmoryBooks(t.Context())
	if err != nil {
		t.Fatalf("fetch enumeration: %v", err)
	}
	if len(books) != 0 {
		t.Fatalf("books = %+v, want the cut-over alias suppressed", books)
	}
}

func TestGrimmoryEnumerationPublishesAliasAfterLibraryRollback(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000212"
	serveGrimmoryEnumeration(t, `[{"id":212,"libraryId":1,"metadata":{"title":"Rolled back"},"primaryFile":{"bookType":"EPUB"}}]`)
	reconcileCatalogEnumeration = func(_ context.Context, enumeration CatalogEnumeration) (CatalogReconciliationReport, error) {
		return CatalogReconciliationReport{
			Observed: len(enumeration.Observations),
			Scans:    map[string]CatalogScanReport{},
			Resolutions: map[string]CatalogResolution{"212": {
				ID: canonicalID, Surface: SurfaceLibrary, Kind: "epub",
				Provider: ProviderGrimmory, UpstreamID: "212", LibraryID: "1",
				Active: true, Available: true,
			}},
		}, nil
	}
	resolveCatalogIdentitiesFor = func(context.Context, []string, CatalogSurface) (map[string]CatalogResolution, error) {
		return map[string]CatalogResolution{canonicalID: {
			ID: canonicalID, Surface: SurfaceLibrary, Kind: "epub", Provider: ProviderGrimmory,
			UpstreamID: "212", LibraryID: "1", Active: true, Available: true,
		}}, nil
	}

	books, err := fetchGrimmoryBooks(t.Context())
	if err != nil {
		t.Fatalf("fetch enumeration: %v", err)
	}
	if len(books) != 1 || books[0].ID != canonicalID {
		t.Fatalf("rollback books = %+v, want one book at %s", books, canonicalID)
	}
}

// The enumeration used to cost three sequential round trips per record — an
// Observe and a canonical re-resolve per book, then a second Observe of the
// whole set inside the reconciliation — on four uncached member routes. Assert
// the per-book resolve is gone and the batch runs once, while still requiring
// every book to come back so the test cannot pass by publishing nothing.
func TestGrimmoryEnumerationResolvesEveryBookInOneBatchWithNoPerBookLookup(t *testing.T) {
	serveGrimmoryEnumeration(t, `[
		{"id":213,"libraryId":1,"metadata":{"title":"One"},"primaryFile":{"bookType":"EPUB"}},
		{"id":214,"libraryId":1,"metadata":{"title":"Two"},"primaryFile":{"bookType":"PDF"}},
		{"id":215,"libraryId":1,"metadata":{"title":"Three"},"primaryFile":{"bookType":"EPUB"}}
	]`)
	perBookResolves, batchResolves := 0, 0
	perBook := resolveCatalogIdentity
	resolveCatalogIdentity = func(ctx context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		perBookResolves++
		return perBook(ctx, rawID, surface)
	}
	batch := resolveCatalogIdentitiesFor
	resolveCatalogIdentitiesFor = func(ctx context.Context, ids []string, surface CatalogSurface) (map[string]CatalogResolution, error) {
		batchResolves++
		return batch(ctx, ids, surface)
	}

	books, err := fetchGrimmoryBooks(t.Context())
	if err != nil {
		t.Fatalf("fetch enumeration: %v", err)
	}
	if len(books) != 3 {
		t.Fatalf("books = %d, want all three published", len(books))
	}
	if perBookResolves != 0 {
		t.Fatalf("per-book canonical resolves = %d, want the enumeration to make none", perBookResolves)
	}
	if batchResolves != 1 {
		t.Fatalf("batched resolves = %d, want exactly one for the whole catalog", batchResolves)
	}
}

func TestResolveGrimmoryBookRejectsCurrentUnsupportedFormat(t *testing.T) {
	defer setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/7" {
			io.WriteString(w, `{"id":7,"libraryId":1,"metadata":{"title":"Comic"},"primaryFile":{"bookType":"CBX"}}`)
			return
		}
		http.NotFound(w, r)
	})()
	resolution, err := observeCatalogIdentity(t.Context(), CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "7", LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/library/books/"+resolution.ID, nil)
	rec := httptest.NewRecorder()
	if _, _, ok := resolveGrimmoryBookHTTP(rec, req, resolution.ID, BookRead); ok {
		t.Fatal("unsupported current Grimmory format authorized from stale catalog kind")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestLegacyBookProgressMigratesToCanonicalContinuity(t *testing.T) {
	f := withFixture(t)
	setupCurrentGrimmoryDetails(t, map[string]string{"7": "EPUB"})
	oldCatalog, oldContinuity := catalogRepo, continuityRepo
	catalogRepo, continuityRepo = NewCatalogRepository(f.DB), NewContinuityRepository(f.DB)
	t.Cleanup(func() { catalogRepo, continuityRepo = oldCatalog, oldContinuity })

	var userID string
	if err := f.DB.QueryRow(t.Context(), `
		INSERT INTO users (username, password_hash) VALUES ('reader-progress', 'x')
		RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	book, err := catalogRepo.Observe(t.Context(), CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "7", LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.DB.Exec(t.Context(), `
		INSERT INTO book_progress (user_id, book_id, locator, percent)
		VALUES ($1::uuid, 7, '{"cfi":"legacy","fraction":0.4}'::jsonb, 0.4)`, userID); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/library/books/7/progress", nil)
	req.SetPathValue("id", "7")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: userID}))
	rec := httptest.NewRecorder()
	handleGetBookProgress(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var canonicalRows int
	if err := f.DB.QueryRow(t.Context(), `
		SELECT count(*) FROM member_progress
		WHERE user_id=$1::uuid AND catalog_item_id=$2::uuid`, userID, book.ID).Scan(&canonicalRows); err != nil {
		t.Fatal(err)
	}
	if canonicalRows != 1 {
		t.Fatalf("canonical progress rows = %d, want 1", canonicalRows)
	}
}

func TestBookProgressDualWritesOnlyEPUBAndPDF(t *testing.T) {
	f := withFixture(t)
	setupCurrentGrimmoryDetails(t, map[string]string{"8": "EPUB", "9": "PDF", "10": "AUDIOBOOK"})
	oldCatalog, oldContinuity := catalogRepo, continuityRepo
	catalogRepo, continuityRepo = NewCatalogRepository(f.DB), NewContinuityRepository(f.DB)
	t.Cleanup(func() { catalogRepo, continuityRepo = oldCatalog, oldContinuity })
	var userID string
	if err := f.DB.QueryRow(t.Context(), `
		INSERT INTO users (username, password_hash) VALUES ('writer-progress', 'x')
		RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		upstream, kind, body string
		wantLegacy           int
	}{
		{"8", "epub", `{"locator":{"cfi":"x","fraction":0.5},"percent":0.5}`, 1},
		{"9", "pdf", `{"locator":{"page":2,"zoom":1},"percent":0.2}`, 1},
		{"10", "audiobook", `{"locator":{"trackIndex":1,"positionMs":1000},"positionMs":1000,"durationMs":5000,"percent":0.2}`, 0},
	}
	for _, tc := range cases {
		item, err := catalogRepo.Observe(t.Context(), CatalogObservation{
			Provider: ProviderGrimmory, UpstreamID: tc.upstream, LibraryID: "1",
			Surface: SurfaceLibrary, Kind: CatalogKind(tc.kind),
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/library/books/"+item.ID+"/progress", strings.NewReader(tc.body))
		req.SetPathValue("id", item.ID)
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: userID}))
		rec := httptest.NewRecorder()
		handlePutBookProgress(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", tc.kind, rec.Code, rec.Body.String())
		}
		var legacyRows int
		if err := f.DB.QueryRow(t.Context(), `SELECT count(*) FROM book_progress WHERE user_id=$1::uuid AND book_id=$2::bigint`, userID, tc.upstream).Scan(&legacyRows); err != nil {
			t.Fatal(err)
		}
		if legacyRows != tc.wantLegacy {
			t.Fatalf("%s legacy rows=%d, want %d", tc.kind, legacyRows, tc.wantLegacy)
		}
	}
}

func TestBookProgressDualWriteRollsBackCanonicalOnLegacyFailure(t *testing.T) {
	f := withFixture(t)
	setupCurrentGrimmoryDetails(t, map[string]string{"11": "EPUB"})
	oldCatalog, oldContinuity := catalogRepo, continuityRepo
	catalogRepo, continuityRepo = NewCatalogRepository(f.DB), NewContinuityRepository(f.DB)
	t.Cleanup(func() { catalogRepo, continuityRepo = oldCatalog, oldContinuity })
	var userID string
	if err := f.DB.QueryRow(t.Context(), `
		INSERT INTO users (username, password_hash) VALUES ('atomic-progress', 'x')
		RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	item, err := catalogRepo.Observe(t.Context(), CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "11", LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.DB.Exec(t.Context(), `
		ALTER TABLE book_progress ADD CONSTRAINT task4_legacy_write_failure CHECK (book_id <> 11)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.DB.Exec(context.Background(), `ALTER TABLE book_progress DROP CONSTRAINT IF EXISTS task4_legacy_write_failure`)
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/library/books/"+item.ID+"/progress",
		strings.NewReader(`{"locator":{"cfi":"x","fraction":0.5},"percent":0.5}`))
	req.SetPathValue("id", item.ID)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: userID}))
	rec := httptest.NewRecorder()
	handlePutBookProgress(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", rec.Code, rec.Body.String())
	}
	var canonicalRows int
	if err := f.DB.QueryRow(t.Context(), `
		SELECT count(*) FROM member_progress WHERE user_id=$1::uuid AND catalog_item_id=$2::uuid`,
		userID, item.ID).Scan(&canonicalRows); err != nil {
		t.Fatal(err)
	}
	if canonicalRows != 0 {
		t.Fatalf("canonical rows=%d after legacy failure, want transaction rollback", canonicalRows)
	}
}

func TestUpdateLibraryMetadataWhitelistsAndPreservesUpstreamFields(t *testing.T) {
	var forwarded map[string]any
	defer setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer manage-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/books/7/metadata":
			if got := r.URL.Query().Get("replaceMode"); got != "REPLACE_WHEN_PROVIDED" {
				t.Errorf("replaceMode = %q", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&forwarded); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"title": "New title"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/7":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":7,"libraryId":1,"libraryName":"Books","metadata":{"title":"New title","subtitle":"Sub","authors":["A"],"categories":["C"],"description":"D","seriesNumber":2.5},"primaryFile":{"bookType":"EPUB","fileSizeKb":10}}`)
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	})()

	body := `{"title":"New title","subtitle":"Sub","authors":["A"],"categories":["C"],"language":"en","description":"D","seriesName":"S","seriesNumber":2.5,"publisher":"P","publishedDate":"2026-01-02","isbn10":"1234567890","isbn13":"1234567890123","adminNotes":"must not pass"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/library/books/7/metadata", strings.NewReader(body))
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()
	handleUpdateLibraryBookMetadata(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	metadata, ok := forwarded["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("missing metadata wrapper: %#v", forwarded)
	}
	if _, exists := metadata["adminNotes"]; exists {
		t.Fatalf("unknown field forwarded: %#v", metadata)
	}
	if metadata["title"] != "New title" || metadata["isbn13"] != "1234567890123" {
		t.Fatalf("whitelisted fields missing: %#v", metadata)
	}
	if _, ok := forwarded["clearFlags"]; !ok {
		t.Fatalf("clearFlags missing: %#v", forwarded)
	}
	var updated LibraryBook
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Title != "New title" || updated.SeriesNumber == nil || *updated.SeriesNumber != 2.5 {
		t.Fatalf("bad normalized response: %+v", updated)
	}
	if !looksLikeUUID(updated.ID) {
		t.Fatalf("updated public id = %q, want canonical UUID", updated.ID)
	}
}

func TestUpdateLibraryMetadataRejectsUnsupportedReload(t *testing.T) {
	changed := false
	defer setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/7":
			format := "EPUB"
			if changed {
				format = "CBX"
			}
			fmt.Fprintf(w, `{"id":7,"libraryId":1,"metadata":{"title":"Book"},"primaryFile":{"bookType":%q}}`, format)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/books/7/metadata":
			changed = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	})()
	resolution, err := observeCatalogIdentity(t.Context(), CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "7", LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/library/books/"+resolution.ID+"/metadata", strings.NewReader(`{"title":"Book"}`))
	req.SetPathValue("id", resolution.ID)
	rec := httptest.NewRecorder()
	handleUpdateLibraryBookMetadata(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want unsupported reload hidden", rec.Code, rec.Body.String())
	}
}

func TestUpdateLibraryMetadataMapsUpstreamErrors(t *testing.T) {
	tests := []struct {
		name     string
		upstream int
		want     int
	}{
		{name: "missing", upstream: http.StatusNotFound, want: http.StatusNotFound},
		{name: "failure", upstream: http.StatusInternalServerError, want: http.StatusBadGateway},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/7" {
					io.WriteString(w, `{"id":7,"libraryId":1,"metadata":{"title":"Book"},"primaryFile":{"bookType":"EPUB"}}`)
					return
				}
				if r.Method == http.MethodPut {
					http.Error(w, "no", test.upstream)
					return
				}
				http.Error(w, "unexpected", http.StatusNotFound)
			})()
			req := httptest.NewRequest(http.MethodPut, "/api/v1/library/books/7/metadata", strings.NewReader(`{"title":"x"}`))
			req.SetPathValue("id", "7")
			rec := httptest.NewRecorder()
			handleUpdateLibraryBookMetadata(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, test.want, rec.Body.String())
			}
		})
	}
}

func TestFetchLibraryMetadataNormalizesSSE(t *testing.T) {
	defer setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/4" {
			io.WriteString(w, `{"id":4,"libraryId":1,"metadata":{"title":"Book"},"primaryFile":{"bookType":"EPUB"}}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/books/4/metadata/prospective" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		var request struct {
			Providers []string `json:"providers"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if len(request.Providers) < 4 {
			t.Errorf("expected default providers, got %#v", request.Providers)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"provider\":\"Google\",\"title\":\"Candidate\",\"authors\":[\"Writer\"],\"thumbnailUrl\":\"https://covers.example/c.jpg\"}\n\n")
	})()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/books/4/metadata/fetch", nil)
	req.SetPathValue("id", "4")
	rec := httptest.NewRecorder()
	handleFetchLibraryBookMetadata(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Candidates []LibraryMetadataCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Candidates) != 1 || body.Candidates[0].Provider != "Google" ||
		body.Candidates[0].CoverURL != "https://covers.example/c.jpg" || body.Candidates[0].Authors[0] != "Writer" {
		t.Fatalf("bad candidates: %+v", body.Candidates)
	}
}

func multipartCoverRequest(t *testing.T, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("cover", "cover.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/library/books/9/cover", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("id", "9")
	return req
}

func TestUpdateLibraryCoverValidatesAndForwardsMultipart(t *testing.T) {
	jpeg := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0}, 600)...)
	forwarded := false
	defer setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/9" {
			io.WriteString(w, `{"id":9,"libraryId":1,"metadata":{"title":"Book"},"primaryFile":{"bookType":"EPUB"}}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/books/9/metadata/cover/upload" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("missing upstream file part: %v", err)
			return
		}
		defer file.Close()
		got, _ := io.ReadAll(file)
		forwarded = bytes.Equal(got, jpeg) && header.Header.Get("Content-Type") == "image/jpeg"
		w.WriteHeader(http.StatusOK)
	})()
	rec := httptest.NewRecorder()
	handleUpdateLibraryBookCover(rec, multipartCoverRequest(t, jpeg))
	if rec.Code != http.StatusNoContent || !forwarded {
		t.Fatalf("status=%d forwarded=%v body=%s", rec.Code, forwarded, rec.Body.String())
	}

	bad := httptest.NewRecorder()
	handleUpdateLibraryBookCover(bad, multipartCoverRequest(t, []byte("not an image")))
	if bad.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("bad type status %d", bad.Code)
	}

	tooLarge := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0}, maxLibraryCoverBytes)...)
	large := httptest.NewRecorder()
	handleUpdateLibraryBookCover(large, multipartCoverRequest(t, tooLarge))
	if large.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large status %d: %s", large.Code, large.Body.String())
	}
}

func TestUpdateLibraryCoverRejectsPrivateURL(t *testing.T) {
	installCatalogIdentityTest(t)
	setupCurrentGrimmoryDetails(t, map[string]string{"2": "EPUB"})
	for _, rawURL := range []string{
		"http://127.0.0.1/cover.jpg",
		"http://10.0.0.1/cover.jpg",
		"file:///etc/passwd",
	} {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/library/books/2/cover",
			strings.NewReader(`{"coverUrl":`+strconv.Quote(rawURL)+`}`))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", "2")
		rec := httptest.NewRecorder()
		handleUpdateLibraryBookCover(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", rawURL, rec.Code)
		}
	}
}

func TestDeleteLibraryBookOnlyCleansUpAfterUpstreamSuccess(t *testing.T) {
	called := 0
	defer setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/11" {
			io.WriteString(w, `{"id":11,"libraryId":1,"metadata":{"title":"Book"},"primaryFile":{"bookType":"EPUB"}}`)
			return
		}
		called++
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/books" || r.URL.Query().Get("ids") != "11" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		http.Error(w, "failed", http.StatusInternalServerError)
	})()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/library/books/11", nil)
	req.SetPathValue("id", "11")
	rec := httptest.NewRecorder()
	handleDeleteLibraryBook(rec, req)
	if rec.Code != http.StatusBadGateway || called != 1 {
		t.Fatalf("status=%d calls=%d body=%s", rec.Code, called, rec.Body.String())
	}
}

func TestDeleteLibraryBookRemovesLegacyAndCanonicalProgress(t *testing.T) {
	f := withFixture(t)
	oldDB, oldCatalog, oldAuthorizer := dbPool, catalogRepo, grimmoryAuthorizer
	dbPool, catalogRepo = f.DB, NewCatalogRepository(f.DB)
	grimmoryAuthorizer, _ = NewGrimmoryAuthorizer(grimmoryAPIResolver{}, []string{"1"})
	t.Cleanup(func() { dbPool, catalogRepo, grimmoryAuthorizer = oldDB, oldCatalog, oldAuthorizer })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login":
			json.NewEncoder(w).Encode(map[string]any{"accessToken": "delete-token", "expires": 7200})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/31":
			io.WriteString(w, `{"id":31,"libraryId":1,"metadata":{"title":"Delete me"},"primaryFile":{"bookType":"EPUB"}}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/books" && r.URL.Query().Get("ids") == "31":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = server.URL
	t.Setenv("GRIMMORY_ADMIN_USER", "gateway")
	t.Setenv("GRIMMORY_ADMIN_PASSWORD", "secret")
	grimmoryTok.mu.Lock()
	oldToken, oldExpiry := grimmoryTok.token, grimmoryTok.expiresAt
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()
	t.Cleanup(func() {
		grimmoryBaseURL = oldURL
		grimmoryTok.mu.Lock()
		grimmoryTok.token, grimmoryTok.expiresAt = oldToken, oldExpiry
		grimmoryTok.mu.Unlock()
	})

	item, err := catalogRepo.Observe(t.Context(), CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "31", LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := f.DB.QueryRow(t.Context(), `INSERT INTO users (username, password_hash) VALUES ('delete-progress', 'x') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.DB.Exec(t.Context(), `
		INSERT INTO book_progress (user_id, book_id, locator, percent)
		VALUES ($1::uuid, 31, '{"cfi":"legacy","fraction":0.5}'::jsonb, 0.5)`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.DB.Exec(t.Context(), `
		INSERT INTO member_progress (user_id, catalog_item_id, locator, percent)
		VALUES ($1::uuid, $2::uuid, '{"cfi":"canonical","fraction":0.5}'::jsonb, 0.5)`, userID, item.ID); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/library/books/"+item.ID, nil)
	req.SetPathValue("id", item.ID)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: userID, Roles: []string{"Owner"}}))
	rec := httptest.NewRecorder()
	handleDeleteLibraryBook(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var legacyRows, canonicalRows int
	if err := f.DB.QueryRow(t.Context(), `SELECT count(*) FROM book_progress WHERE book_id=31`).Scan(&legacyRows); err != nil {
		t.Fatal(err)
	}
	if err := f.DB.QueryRow(t.Context(), `SELECT count(*) FROM member_progress WHERE catalog_item_id=$1::uuid`, item.ID).Scan(&canonicalRows); err != nil {
		t.Fatal(err)
	}
	if legacyRows != 0 || canonicalRows != 0 {
		t.Fatalf("progress rows legacy/canonical=%d/%d, want 0/0", legacyRows, canonicalRows)
	}
}

func setupDeletionProgressScenario(t *testing.T, upstreamDeleteStatus int, upstreamID string) (*pgxpool.Pool, CatalogResolution, string) {
	t.Helper()
	f := withFixture(t)
	oldDB, oldCatalog, oldAuthorizer := dbPool, catalogRepo, grimmoryAuthorizer
	dbPool, catalogRepo = f.DB, NewCatalogRepository(f.DB)
	grimmoryAuthorizer, _ = NewGrimmoryAuthorizer(grimmoryAPIResolver{}, []string{"1"})
	t.Cleanup(func() { dbPool, catalogRepo, grimmoryAuthorizer = oldDB, oldCatalog, oldAuthorizer })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login":
			json.NewEncoder(w).Encode(map[string]any{"accessToken": "delete-scenario-token", "expires": 7200})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/"+upstreamID:
			fmt.Fprintf(w, `{"id":%s,"libraryId":1,"metadata":{"title":"Delete scenario"},"primaryFile":{"bookType":"EPUB"}}`, upstreamID)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/books" && r.URL.Query().Get("ids") == upstreamID:
			w.WriteHeader(upstreamDeleteStatus)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = server.URL
	t.Setenv("GRIMMORY_ADMIN_USER", "gateway")
	t.Setenv("GRIMMORY_ADMIN_PASSWORD", "secret")
	grimmoryTok.mu.Lock()
	oldToken, oldExpiry := grimmoryTok.token, grimmoryTok.expiresAt
	grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
	grimmoryTok.mu.Unlock()
	t.Cleanup(func() {
		grimmoryBaseURL = oldURL
		grimmoryTok.mu.Lock()
		grimmoryTok.token, grimmoryTok.expiresAt = oldToken, oldExpiry
		grimmoryTok.mu.Unlock()
	})

	item, err := catalogRepo.Observe(t.Context(), CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: upstreamID, LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "epub",
	})
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := f.DB.QueryRow(t.Context(), `INSERT INTO users (username, password_hash) VALUES ($1, 'x') RETURNING id::text`, "delete-scenario-"+upstreamID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.DB.Exec(t.Context(), `INSERT INTO book_progress (user_id, book_id, locator, percent) VALUES ($1::uuid, $2::bigint, '{}', 0.5)`, userID, upstreamID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.DB.Exec(t.Context(), `INSERT INTO member_progress (user_id, catalog_item_id, locator, percent) VALUES ($1::uuid, $2::uuid, '{}', 0.5)`, userID, item.ID); err != nil {
		t.Fatal(err)
	}
	return f.DB, item, userID
}

func assertDeletionProgressCounts(t *testing.T, db *pgxpool.Pool, item CatalogResolution, upstreamID string, wantLegacy, wantCanonical int) {
	t.Helper()
	var legacyRows, canonicalRows int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM book_progress WHERE book_id=$1::bigint`, upstreamID).Scan(&legacyRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM member_progress WHERE catalog_item_id=$1::uuid`, item.ID).Scan(&canonicalRows); err != nil {
		t.Fatal(err)
	}
	if legacyRows != wantLegacy || canonicalRows != wantCanonical {
		t.Fatalf("progress rows legacy/canonical=%d/%d, want %d/%d", legacyRows, canonicalRows, wantLegacy, wantCanonical)
	}
}

func deleteScenarioRequest(t *testing.T, item CatalogResolution, userID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/library/books/"+item.ID, nil)
	req.SetPathValue("id", item.ID)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: userID, Roles: []string{"Owner"}}))
	rec := httptest.NewRecorder()
	handleDeleteLibraryBook(rec, req)
	return rec
}

func TestDeleteLibraryBookUpstreamFailurePreservesBothProgressTables(t *testing.T) {
	db, item, userID := setupDeletionProgressScenario(t, http.StatusInternalServerError, "32")
	rec := deleteScenarioRequest(t, item, userID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s, want upstream failure", rec.Code, rec.Body.String())
	}
	assertDeletionProgressCounts(t, db, item, "32", 1, 1)
}

func TestDeleteLibraryBookProgressCleanupRollsBackBothTables(t *testing.T) {
	db, item, userID := setupDeletionProgressScenario(t, http.StatusNoContent, "33")
	if _, err := db.Exec(t.Context(), `
		CREATE FUNCTION fail_member_progress_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'forced canonical cleanup failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `
		CREATE TRIGGER fail_member_progress_delete
		BEFORE DELETE ON member_progress FOR EACH ROW
		EXECUTE FUNCTION fail_member_progress_delete()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_member_progress_delete ON member_progress`)
		db.Exec(context.Background(), `DROP FUNCTION IF EXISTS fail_member_progress_delete()`)
	})

	rec := deleteScenarioRequest(t, item, userID)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want cleanup failure", rec.Code, rec.Body.String())
	}
	assertDeletionProgressCounts(t, db, item, "33", 1, 1)
}

func TestHandleMediaReturnsCanonicalLibraryIDs(t *testing.T) {
	installCatalogIdentityTest(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/Users", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{{"Id": "id-alice", "Name": "alice"}})
	})
	mux.HandleFunc("/Users/id-alice/Views", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"Items": []map[string]string{
				{"Id": "lib-1", "Name": "Movies", "CollectionType": "movies"},
				// Verified live against Jellyfin 10.11.11: it has no distinct
				// "audiobooks" collection type — audiobook libraries are
				// created with CollectionType "books".
				{"Id": "lib-2", "Name": "Audiobooks", "CollectionType": "books"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	oldBase := jellyfinBaseURL
	jellyfinBaseURL = srv.URL
	defer func() { jellyfinBaseURL = oldBase }()
	oldRedis := redisClient
	redisClient = nil
	defer func() { redisClient = oldRedis }()

	req := httptest.NewRequest("GET", "/api/v1/media", nil)
	rec := httptest.NewRecorder()
	handleMedia(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var libs []mediaLibrary
	if err := json.Unmarshal(rec.Body.Bytes(), &libs); err != nil {
		t.Fatal(err)
	}
	if len(libs) != 2 {
		t.Fatalf("expected 2 libraries, got %+v", libs)
	}
	if libs[1].Type != "audio" || libs[1].CollectionType != "books" {
		t.Fatalf("expected audio/books mapping, got %+v", libs[1])
	}
	if libs[0].Type != "video" || libs[0].CollectionType != "movies" {
		t.Fatalf("expected video/movies mapping, got %+v", libs[0])
	}
	for _, library := range libs {
		if !looksLikeUUID(library.ID) {
			t.Fatalf("public id = %q, want canonical UUID", library.ID)
		}
	}
}

func TestGrimmoryEnumerationAbsentLibraryIDIsCountedSeparatelyAndLogged(t *testing.T) {
	var logs bytes.Buffer
	oldLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldLogOutput) })

	installCatalogIdentityTest(t)
	installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[
			{
				"id":101,
				"metadata":{"title":"Book Without Library ID"},
				"primaryFile":{"bookType":"EPUB"}
			}
		]`)
	})

	books, err := fetchGrimmoryBooks(t.Context())
	if err != nil {
		t.Fatalf("fetchGrimmoryBooks failed: %v", err)
	}
	if len(books) != 0 {
		t.Fatalf("books count = %d, want 0 (absent libraryId must be rejected)", len(books))
	}
	if !strings.Contains(logs.String(), "catalog normalization provider=grimmory missing_library_id=1") {
		t.Fatalf("logs = %q, want 'catalog normalization provider=grimmory missing_library_id=1'", logs.String())
	}
}
