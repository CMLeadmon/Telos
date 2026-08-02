package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type recordingLibraryProgressReader struct {
	calls   int
	userID  string
	itemIDs []string
}

func (r *recordingLibraryProgressReader) GetMany(_ context.Context, userID string, itemIDs []string) (map[string]MemberProgress, error) {
	r.calls++
	r.userID = userID
	r.itemIDs = append([]string(nil), itemIDs...)
	if len(itemIDs) == 0 {
		return map[string]MemberProgress{}, nil
	}
	return map[string]MemberProgress{
		itemIDs[0]: {
			ProgressInput: ProgressInput{
				Locator:    json.RawMessage(`{"trackIndex":2}`),
				PositionMS: 125_000,
				DurationMS: 3_600_000,
				Percent:    0.034,
			},
		},
	}, nil
}

func TestLibraryKindFromGrimmory(t *testing.T) {
	cases := map[string]CatalogKind{
		"EPUB":      "epub",
		" pdf ":     "pdf",
		"audiobook": "audiobook",
	}
	for format, want := range cases {
		got, err := libraryKindFromGrimmory(format)
		if err != nil || got != want {
			t.Errorf("%q: got=%q err=%v, want=%q", format, got, err, want)
		}
	}
	for _, format := range []string{"CBX", "MOBI", "", "PHYSICAL"} {
		if got, err := libraryKindFromGrimmory(format); got != "" || !errors.Is(err, errUnsupportedLibraryKind) {
			t.Errorf("unsupported %q: got=%q err=%v", format, got, err)
		}
	}
}

func TestNormalizedLibraryFormatCompatibilityComesFromKind(t *testing.T) {
	for _, tc := range []struct {
		providerFormat string
		wantKind       CatalogKind
		wantFormat     string
	}{
		{providerFormat: "EPUB", wantKind: "epub", wantFormat: "EPUB"},
		{providerFormat: "PDF", wantKind: "pdf", wantFormat: "PDF"},
		{providerFormat: "AUDIOBOOK", wantKind: "audiobook", wantFormat: "AUDIOBOOK"},
	} {
		item, err := normalizedLibraryItem(LibraryBook{
			ID: "00000000-0000-4000-8000-000000000001", Format: tc.providerFormat,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.providerFormat, err)
		}
		if item.Kind != tc.wantKind || item.Format != tc.wantFormat {
			t.Errorf("%s normalized kind/format=%q/%q, want=%q/%q", tc.providerFormat, item.Kind, item.Format, tc.wantKind, tc.wantFormat)
		}
	}
}

// The manage modal prefills its whole form from this shape and PUTs all of it
// back under Grimmory's REPLACE_WHEN_PROVIDED. A field the normalizer drops
// therefore returns as "" and erases the upstream value on the next save, so
// everything the metadata form owns has to survive normalization.
func TestNormalizedLibraryItemKeepsEditableBibliographicMetadata(t *testing.T) {
	item, err := normalizedLibraryItem(LibraryBook{
		ID:        "00000000-0000-4000-8000-000000000002",
		Format:    "EPUB",
		Publisher: "Penguin Classics",
		ISBN10:    "0141439513",
		ISBN13:    "9780141439518",
	})
	if err != nil {
		t.Fatalf("normalizedLibraryItem: %v", err)
	}
	for _, tc := range []struct{ field, got, want string }{
		{"publisher", item.Publisher, "Penguin Classics"},
		{"isbn10", item.ISBN10, "0141439513"},
		{"isbn13", item.ISBN13, "9780141439518"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}

	// The member contract still hides provider identity, which is what the
	// normalized shape exists to do. Check keys, not substrings — cover URLs
	// legitimately contain "library".
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, leaked := range []string{"upstreamId", "libraryId", "library"} {
		if _, present := fields[leaked]; present {
			t.Errorf("normalized item leaks the %q field: %s", leaked, raw)
		}
	}
}

func TestGrimmoryCatalogListItemsNormalizesAudiobookAndBatchesProgress(t *testing.T) {
	var logs bytes.Buffer
	oldLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldLogOutput) })

	installCatalogIdentityTest(t)
	oldAuthorizer := grimmoryAuthorizer
	grimmoryAuthorizer, _ = NewGrimmoryAuthorizer(grimmoryAPIResolver{}, []string{"1"})
	t.Cleanup(func() { grimmoryAuthorizer = oldAuthorizer })

	installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[
			{
				"id":73,
				"libraryId":1,
				"libraryName":"Spoken Books",
				"addedOn":"2026-07-30T15:04:05Z",
				"metadata":{
					"title":"Braiding Sweetgrass",
					"subtitle":"Indigenous Wisdom",
					"authors":["Robin Wall Kimmerer"],
					"narrator":"Robin Wall Kimmerer",
					"categories":["Nature","History"],
					"description":"A meeting of science and story.",
					"language":"en",
					"seriesName":"Earth Stories",
					"seriesNumber":2.5,
					"publishedDate":"2020-01-01",
					"audiobookMetadata":{"durationSeconds":3600}
				},
				"primaryFile":{"bookType":"AUDIOBOOK","fileSizeKb":42100}
			},
			{
				"id":74,
				"libraryId":1,
				"addedOn":"2026-07-29T10:00:00Z",
				"metadata":{"title":"Untitled Lists"},
				"primaryFile":{"bookType":"EPUB"}
			},
			{
				"id":75,
				"libraryId":1,
				"metadata":{"title":"Deferred Comic"},
				"primaryFile":{"bookType":"CBX"}
			}
		]`)
	})

	progress := &recordingLibraryProgressReader{}
	catalog := &GrimmoryCatalog{continuity: progress}
	items, err := catalog.ListItems(t.Context(), "00000000-0000-4000-8000-000000000099")
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%+v, want supported audiobook and EPUB only", items)
	}

	audio := items[0]
	if !looksLikeUUID(audio.ID) || audio.ID == "73" {
		t.Fatalf("public id=%q, want canonical UUID", audio.ID)
	}
	if audio.Kind != "audiobook" || audio.Title != "Braiding Sweetgrass" || audio.Subtitle != "Indigenous Wisdom" {
		t.Fatalf("audiobook identity fields=%+v", audio)
	}
	if audio.Format != "AUDIOBOOK" || items[1].Format != "EPUB" {
		t.Fatalf("temporary format compatibility audio/epub=%q/%q", audio.Format, items[1].Format)
	}
	if got := strings.Join(audio.Authors, ","); got != "Robin Wall Kimmerer" || audio.Narrator != "Robin Wall Kimmerer" {
		t.Fatalf("audiobook people authors=%q narrator=%q", got, audio.Narrator)
	}
	if got := strings.Join(audio.Categories, ","); got != "Nature,History" {
		t.Fatalf("categories=%q", got)
	}
	if audio.Description != "A meeting of science and story." || audio.Language != "en" ||
		audio.SeriesName != "Earth Stories" || audio.SeriesNumber == nil || *audio.SeriesNumber != 2.5 ||
		audio.PublishedDate != "2020-01-01" || audio.AddedOn != "2026-07-30T15:04:05Z" {
		t.Fatalf("audiobook descriptive fields=%+v", audio)
	}
	if audio.DurationMS != 3_600_000 {
		t.Fatalf("durationMs=%d, want 3600000", audio.DurationMS)
	}
	wantCover := "/api/v1/library/books/" + audio.ID + "/cover"
	if audio.CoverURL != wantCover {
		t.Fatalf("coverUrl=%q, want=%q", audio.CoverURL, wantCover)
	}
	if audio.Progress.PositionMS != 125_000 || audio.Progress.DurationMS != 3_600_000 {
		t.Fatalf("progress=%+v", audio.Progress)
	}
	if items[1].Authors == nil || items[1].Categories == nil {
		t.Fatalf("nil provider lists were not normalized: %+v", items[1])
	}

	if progress.calls != 1 || progress.userID != "00000000-0000-4000-8000-000000000099" {
		t.Fatalf("GetMany calls=%d user=%q", progress.calls, progress.userID)
	}
	if len(progress.itemIDs) != 2 || progress.itemIDs[0] != audio.ID || progress.itemIDs[1] != items[1].ID {
		t.Fatalf("GetMany item IDs=%v, want canonical page IDs", progress.itemIDs)
	}

	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"id":"73"`) || strings.Contains(string(raw), "upstream") || strings.Contains(string(raw), "Spoken Books") {
		t.Fatalf("provider identity leaked in public payload: %s", raw)
	}
	if got := strings.Count(logs.String(), "unsupported=1"); got != 1 {
		t.Fatalf("bounded unsupported-format observations=%d log=%q, want one aggregate count", got, logs.String())
	}
}

func TestLibraryBookDetailRouteReturnsNormalizedMemberItem(t *testing.T) {
	f := withFixture(t)
	oldCatalog, oldContinuity, oldAuthorizer := catalogRepo, continuityRepo, grimmoryAuthorizer
	catalogRepo, continuityRepo, grimmoryAuthorizer = NewCatalogRepository(f.DB), NewContinuityRepository(f.DB), nil
	t.Cleanup(func() {
		catalogRepo, continuityRepo, grimmoryAuthorizer = oldCatalog, oldContinuity, oldAuthorizer
	})
	userID := createContinuityUser(t, "normalized-detail-reader", f.DB)
	resolution, err := catalogRepo.Observe(t.Context(), CatalogObservation{
		Provider: ProviderGrimmory, UpstreamID: "81", LibraryID: "1",
		Surface: SurfaceLibrary, Kind: "audiobook",
	})
	if err != nil {
		t.Fatal(err)
	}
	progress, err := ValidateProgress("audiobook", ProgressInput{
		Locator: json.RawMessage(`{"trackIndex":2}`), PositionMS: 125_000,
		DurationMS: 3_600_000, Percent: 0.034,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := continuityRepo.Put(t.Context(), userID, resolution.ID, progress); err != nil {
		t.Fatal(err)
	}

	upstream := http.NewServeMux()
	upstream.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "detail-route-token", "expires": 7200})
	})
	upstream.HandleFunc("GET /api/v1/books/81", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{
			"id":81,"libraryId":1,"libraryName":"Provider Shelf","addedOn":"2026-07-31T10:00:00Z",
			"metadata":{"title":"A Spoken Book","authors":["A. Writer"],"narrator":"N. Reader",
				"description":"Member-safe description","audiobookMetadata":{"durationSeconds":3600}},
			"primaryFile":{"bookType":"AUDIOBOOK","fileSizeKb":42}
		}`)
	})
	server := httptest.NewServer(upstream)
	defer server.Close()
	oldURL := grimmoryBaseURL
	grimmoryBaseURL = server.URL
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

	routes := http.NewServeMux()
	routes.Handle("GET /api/v1/library/books/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := &UserContext{ID: userID, Roles: []string{"Owner"}}
		handleLibraryBookByID(w, r.WithContext(context.WithValue(r.Context(), userContextKey, actor)))
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/library/books/"+resolution.ID, nil)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var item LibraryItem
	if err := json.Unmarshal(rec.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.ID != resolution.ID || item.Kind != "audiobook" || item.Format != "AUDIOBOOK" ||
		item.Narrator != "N. Reader" || item.DurationMS != 3_600_000 ||
		item.Progress.PositionMS != 125_000 || item.CoverURL != "/api/v1/library/books/"+resolution.ID+"/cover" {
		t.Fatalf("normalized detail=%+v", item)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, providerField := range []string{"upstreamId", "libraryId", "library", "publisher", "isbn10", "isbn13", "fileSizeKb"} {
		if _, leaked := payload[providerField]; leaked {
			t.Errorf("provider/management field %q leaked: %s", providerField, rec.Body.String())
		}
	}
}

func assertLibraryAPIError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string, forbiddenDetails ...string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status=%d body=%s, want=%d", rec.Code, rec.Body.String(), wantStatus)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("content-type=%q body=%s", contentType, rec.Body.String())
	}
	var body apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode API error: %v body=%q", err, rec.Body.String())
	}
	if body.Error.Code != wantCode {
		t.Fatalf("error code=%q body=%s, want=%q", body.Error.Code, rec.Body.String(), wantCode)
	}
	for _, detail := range forbiddenDetails {
		if strings.Contains(rec.Body.String(), detail) {
			t.Errorf("internal detail %q leaked in %s", detail, rec.Body.String())
		}
	}
}

func TestLibraryMemberHandlersClassifyErrorsWithoutInternalDetails(t *testing.T) {
	const userID = "00000000-0000-4000-8000-000000000099"
	memberRequest := func(method, target string) *http.Request {
		req := httptest.NewRequest(method, target, nil)
		return req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: userID}))
	}

	t.Run("list provider unavailable is terse 503", func(t *testing.T) {
		oldURL, oldCatalog, oldContinuity, oldAuthorizer := grimmoryBaseURL, catalogRepo, continuityRepo, grimmoryAuthorizer
		grimmoryBaseURL, catalogRepo, continuityRepo, grimmoryAuthorizer = "http://127.0.0.1:1", nil, nil, nil
		grimmoryTok.mu.Lock()
		oldToken, oldExpiry := grimmoryTok.token, grimmoryTok.expiresAt
		grimmoryTok.token, grimmoryTok.expiresAt = "", time.Time{}
		grimmoryTok.mu.Unlock()
		t.Cleanup(func() {
			grimmoryBaseURL, catalogRepo, continuityRepo, grimmoryAuthorizer = oldURL, oldCatalog, oldContinuity, oldAuthorizer
			grimmoryTok.mu.Lock()
			grimmoryTok.token, grimmoryTok.expiresAt = oldToken, oldExpiry
			grimmoryTok.mu.Unlock()
		})
		rec := httptest.NewRecorder()
		handleLibraryBooks(rec, memberRequest(http.MethodGet, "/api/v1/library/books"))
		assertLibraryAPIError(t, rec, http.StatusServiceUnavailable, "upstream_unavailable", "127.0.0.1", "connect")
	})

	t.Run("list local catalog failure is terse 500", func(t *testing.T) {
		cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/books" {
				_, _ = fmt.Fprint(w, `[]`)
				return
			}
			http.NotFound(w, r)
		})
		defer cleanup()
		oldReconcile := reconcileCatalogEnumeration
		reconcileCatalogEnumeration = func(context.Context, CatalogEnumeration) (CatalogReconciliationReport, error) {
			return CatalogReconciliationReport{}, errors.New("local catalog secret")
		}
		t.Cleanup(func() { reconcileCatalogEnumeration = oldReconcile })
		rec := httptest.NewRecorder()
		handleLibraryBooks(rec, memberRequest(http.MethodGet, "/api/v1/library/books"))
		assertLibraryAPIError(t, rec, http.StatusInternalServerError, "internal_error", "local catalog secret")
	})

	t.Run("detail provider unavailable is terse 503", func(t *testing.T) {
		cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/books/81" {
				http.Error(w, "provider detail", http.StatusBadGateway)
				return
			}
			http.NotFound(w, r)
		})
		defer cleanup()
		oldCatalog, oldContinuity, oldResolve := catalogRepo, continuityRepo, resolveCatalogIdentity
		catalogRepo, continuityRepo = nil, nil
		resolveCatalogIdentity = func(context.Context, string, CatalogSurface) (CatalogResolution, error) {
			return CatalogResolution{}, errCatalogNotFound
		}
		t.Cleanup(func() { catalogRepo, continuityRepo, resolveCatalogIdentity = oldCatalog, oldContinuity, oldResolve })
		req := memberRequest(http.MethodGet, "/api/v1/library/books/81")
		req.SetPathValue("id", "81")
		rec := httptest.NewRecorder()
		handleLibraryBookByID(rec, req)
		assertLibraryAPIError(t, rec, http.StatusServiceUnavailable, "upstream_unavailable", "provider detail", "502")
	})

	t.Run("detail local continuity failure is terse 500", func(t *testing.T) {
		const canonicalID = "00000000-0000-4000-8000-000000000081"
		cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/books/81" {
				_, _ = fmt.Fprint(w, `{"id":81,"libraryId":1,"metadata":{"title":"Local failure"},"primaryFile":{"bookType":"PDF"}}`)
				return
			}
			http.NotFound(w, r)
		})
		defer cleanup()
		oldCatalog, oldContinuity, oldObserve, oldResolve := catalogRepo, continuityRepo, observeCatalogIdentity, resolveCatalogIdentity
		catalogRepo, continuityRepo = nil, nil
		resolveCatalogIdentity = func(context.Context, string, CatalogSurface) (CatalogResolution, error) {
			return CatalogResolution{ID: canonicalID, Surface: SurfaceLibrary, Kind: "pdf", Provider: ProviderGrimmory,
				UpstreamID: "81", LibraryID: "1", Active: true, Available: true}, nil
		}
		observeCatalogIdentity = func(_ context.Context, in CatalogObservation) (CatalogResolution, error) {
			return CatalogResolution{ID: canonicalID, Surface: in.Surface, Kind: in.Kind, Provider: in.Provider,
				UpstreamID: in.UpstreamID, LibraryID: in.LibraryID, Active: true, Available: true}, nil
		}
		t.Cleanup(func() {
			catalogRepo, continuityRepo, observeCatalogIdentity, resolveCatalogIdentity = oldCatalog, oldContinuity, oldObserve, oldResolve
		})
		req := memberRequest(http.MethodGet, "/api/v1/library/books/"+canonicalID)
		req.SetPathValue("id", canonicalID)
		rec := httptest.NewRecorder()
		handleLibraryBookByID(rec, req)
		assertLibraryAPIError(t, rec, http.StatusInternalServerError, "internal_error", "continuity repository")
	})
}

func catalogEnumerationSamples(t *testing.T, result CatalogResult) uint64 {
	t.Helper()
	observer := CatalogReconcileDuration.WithLabelValues(
		string(ProviderGrimmory), string(CatalogOperationEnumerate), string(result),
	)
	metric, ok := observer.(prometheus.Metric)
	if !ok {
		t.Fatal("catalog enumeration observer does not expose a metric")
	}
	var out dto.Metric
	if err := metric.Write(&out); err != nil {
		t.Fatal(err)
	}
	return out.GetHistogram().GetSampleCount()
}

func TestGrimmoryCatalogRecordsEnumerationOutcome(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		installCatalogIdentityTest(t)
		before := catalogEnumerationSamples(t, CatalogResultSuccess)
		installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `[{"id":91,"libraryId":1,"metadata":{"title":"Metric Book"},"primaryFile":{"bookType":"PDF"}}]`)
		})
		if _, err := fetchGrimmoryBooks(t.Context()); err != nil {
			t.Fatalf("successful enumeration: %v", err)
		}
		if got := catalogEnumerationSamples(t, CatalogResultSuccess); got != before+1 {
			t.Fatalf("success enumeration samples=%d, want=%d", got, before+1)
		}
	})

	t.Run("upstream failure", func(t *testing.T) {
		before := catalogEnumerationSamples(t, CatalogResultFailed)
		installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unavailable", http.StatusBadGateway)
		})
		if _, err := fetchGrimmoryBooks(t.Context()); err == nil {
			t.Fatal("failed enumeration unexpectedly succeeded")
		}
		if got := catalogEnumerationSamples(t, CatalogResultFailed); got != before+1 {
			t.Fatalf("failed enumeration samples=%d, want=%d", got, before+1)
		}
	})
}

func TestGrimmoryCatalogRejectsIncompleteOrOversizedDocumentsBeforeReconciliation(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "over 8 MiB", body: `[]` + strings.Repeat(" ", 8<<20)},
		{name: "valid array plus junk", body: `[] trailing-junk`},
		{name: "multiple JSON values", body: `[] []`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity := installCatalogIdentityTest(t)
			coordinatorCalls := 0
			oldReconcile := reconcileCatalogEnumeration
			reconcileCatalogEnumeration = func(context.Context, CatalogEnumeration) (CatalogReconciliationReport, error) {
				coordinatorCalls++
				return CatalogReconciliationReport{}, nil
			}
			t.Cleanup(func() { reconcileCatalogEnumeration = oldReconcile })
			installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, tc.body)
			})

			failedBefore := catalogEnumerationSamples(t, CatalogResultFailed)
			if _, err := fetchGrimmoryBooks(t.Context()); err == nil {
				t.Fatal("unsafe complete-catalog document unexpectedly succeeded")
			}
			if coordinatorCalls != 0 || identity.next != 0 {
				t.Fatalf("coordinator calls=%d observed identities=%d, want zero before full-document validation", coordinatorCalls, identity.next)
			}
			if got := catalogEnumerationSamples(t, CatalogResultFailed); got != failedBefore+1 {
				t.Fatalf("failed enumeration samples=%d, want=%d", got, failedBefore+1)
			}
		})
	}
}

func TestGrimmoryCatalogListItemsHydrates501ItemsWithOneBatch(t *testing.T) {
	installCatalogIdentityTest(t)
	installGrimmoryEnumerationFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		books := make([]map[string]any, 501)
		for i := range books {
			books[i] = map[string]any{
				"id":        i + 1,
				"libraryId": 1,
				"metadata": map[string]any{
					"title": fmt.Sprintf("Book %d", i+1),
				},
				"primaryFile": map[string]any{"bookType": "EPUB"},
			}
		}
		_ = json.NewEncoder(w).Encode(books)
	})

	progress := &recordingLibraryProgressReader{}
	items, err := (&GrimmoryCatalog{continuity: progress}).ListItems(
		t.Context(), "00000000-0000-4000-8000-000000000099",
	)
	if err != nil {
		t.Fatalf("ListItems with 501 records: %v", err)
	}
	if len(items) != 501 || progress.calls != 1 || len(progress.itemIDs) != 501 {
		t.Fatalf("items=%d GetMany calls=%d ids=%d, want 501/1/501", len(items), progress.calls, len(progress.itemIDs))
	}
}

func TestGrimmoryCatalogGetItemUsesCanonicalIdentityAndMemberProgress(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000081"
	cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/81" {
			_, _ = fmt.Fprint(w, `{
				"id":81,"libraryId":1,"addedOn":"2026-07-31T10:00:00Z",
				"metadata":{"title":"A Single Book","authors":["A. Writer"]},
				"primaryFile":{"bookType":"PDF"}
			}`)
			return
		}
		http.NotFound(w, r)
	})
	defer cleanup()

	oldObserve, oldResolve := observeCatalogIdentity, resolveCatalogIdentity
	observeCatalogIdentity = func(_ context.Context, in CatalogObservation) (CatalogResolution, error) {
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceLibrary, Kind: in.Kind, Provider: ProviderGrimmory,
			UpstreamID: in.UpstreamID, LibraryID: in.LibraryID, Active: true, Available: true, Revision: 3,
		}, nil
	}
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		if rawID != canonicalID || surface != SurfaceLibrary {
			return CatalogResolution{}, errCatalogNotFound
		}
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceLibrary, Kind: "pdf", Provider: ProviderGrimmory,
			UpstreamID: "81", LibraryID: "1", Active: true, Available: true, Revision: 3,
		}, nil
	}
	t.Cleanup(func() { observeCatalogIdentity, resolveCatalogIdentity = oldObserve, oldResolve })

	progress := &recordingLibraryProgressReader{}
	catalog := &GrimmoryCatalog{continuity: progress}
	item, resolution, err := catalog.GetItem(t.Context(), "00000000-0000-4000-8000-000000000099", canonicalID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if resolution.ID != canonicalID || item.ID != canonicalID || item.Kind != "pdf" || item.Title != "A Single Book" {
		t.Fatalf("item=%+v resolution=%+v", item, resolution)
	}
	if progress.calls != 1 || len(progress.itemIDs) != 1 || progress.itemIDs[0] != canonicalID {
		t.Fatalf("GetMany calls=%d ids=%v", progress.calls, progress.itemIDs)
	}
}

func TestGrimmoryCatalogGetItemDiscoversLegacyNumericID(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000081"
	cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/books/81" {
			_, _ = fmt.Fprint(w, `{
				"id":81,"libraryId":1,"addedOn":"2026-07-31T10:00:00Z",
				"metadata":{"title":"Legacy Deep Link","authors":["A. Writer"]},
				"primaryFile":{"bookType":"PDF"}
			}`)
			return
		}
		http.NotFound(w, r)
	})
	defer cleanup()

	oldObserve, oldResolve := observeCatalogIdentity, resolveCatalogIdentity
	observeCatalogIdentity = func(_ context.Context, in CatalogObservation) (CatalogResolution, error) {
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceLibrary, Kind: in.Kind, Provider: ProviderGrimmory,
			UpstreamID: in.UpstreamID, LibraryID: in.LibraryID, Active: true, Available: true, Revision: 3,
		}, nil
	}
	resolveCatalogIdentity = func(_ context.Context, _ string, _ CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{}, errCatalogNotFound
	}
	t.Cleanup(func() { observeCatalogIdentity, resolveCatalogIdentity = oldObserve, oldResolve })

	progress := &recordingLibraryProgressReader{}
	item, resolution, err := (&GrimmoryCatalog{continuity: progress}).GetItem(
		t.Context(), "00000000-0000-4000-8000-000000000099", "81",
	)
	if err != nil {
		t.Fatalf("GetItem legacy numeric ID: %v", err)
	}
	if resolution.ID != canonicalID || item.ID != canonicalID || item.Kind != "pdf" || item.Format != "PDF" {
		t.Fatalf("item=%+v resolution=%+v", item, resolution)
	}
	if progress.calls != 1 || len(progress.itemIDs) != 1 || progress.itemIDs[0] != canonicalID {
		t.Fatalf("GetMany calls=%d ids=%v", progress.calls, progress.itemIDs)
	}
}

// The discovery shelves consume Grimmory's own record shape — a numeric id and
// nested metadata — not the gateway's internal LibraryBook. They also route
// through the authorized enumeration, so /api/v1/books has to answer too.
func TestGrimmoryCatalogRecentAndAuthors(t *testing.T) {
	oldAuthorizer := grimmoryAuthorizer
	grimmoryAuthorizer, _ = NewGrimmoryAuthorizer(grimmoryAPIResolver{}, []string{"1"})
	t.Cleanup(func() { grimmoryAuthorizer = oldAuthorizer })

	cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/books":
			_, _ = fmt.Fprint(w, `[
				{
					"id":101,
					"libraryId":1,
					"addedOn":"2026-07-31T12:00:00Z",
					"metadata":{"title":"Recent Audio Book","authors":["Voice Artist"]},
					"primaryFile":{"bookType":"AUDIOBOOK"}
				}
			]`)
		case "/api/v1/app/books/recently-added":
			_, _ = fmt.Fprint(w, `[
				{
					"id":101,
					"libraryId":1,
					"addedOn":"2026-07-31T12:00:00Z",
					"metadata":{"title":"Recent Audio Book","authors":["Voice Artist"]},
					"primaryFile":{"bookType":"AUDIOBOOK"}
				}
			]`)
		case "/api/v1/app/authors":
			_, _ = fmt.Fprint(w, `[
				{"id": 42, "name": "Voice Artist", "bookCount": 3}
			]`)
		case "/api/v1/app/authors/42":
			_, _ = fmt.Fprint(w, `{"id": 42, "name": "Voice Artist", "bookCount": 3}`)
		case "/api/v1/app/books":
			_, _ = fmt.Fprint(w, `[
				{
					"id":101,
					"libraryId":1,
					"addedOn":"2026-07-31T12:00:00Z",
					"metadata":{"title":"Recent Audio Book","authors":["Voice Artist"]},
					"primaryFile":{"bookType":"AUDIOBOOK"}
				}
			]`)
		case "/api/v1/app/series":
			_, _ = fmt.Fprint(w, `[
				{"name": "Vaporwave Series", "bookCount": 2}
			]`)
		case "/api/v1/app/series/Vaporwave Series/books", "/api/v1/app/series/Vaporwave%20Series/books":
			_, _ = fmt.Fprint(w, `[
				{
					"id":101,
					"libraryId":1,
					"addedOn":"2026-07-31T12:00:00Z",
					"metadata":{"title":"Recent Audio Book","authors":["Voice Artist"]},
					"primaryFile":{"bookType":"AUDIOBOOK"}
				}
			]`)
		default:
			http.NotFound(w, r)
		}
	})
	defer cleanup()

	progress := &recordingLibraryProgressReader{}
	cat := &GrimmoryCatalog{continuity: progress}

	items, err := cat.Recent(t.Context(), "user-1", 10)
	if err != nil {
		t.Fatalf("Recent error: %v", err)
	}
	if len(items) != 1 || items[0].Kind != "audiobook" {
		t.Fatalf("Recent items=%+v", items)
	}

	authors, err := cat.Authors(t.Context())
	if err != nil {
		t.Fatalf("Authors error: %v", err)
	}
	if len(authors) != 1 || authors[0].ID != "42" || authors[0].Name != "Voice Artist" {
		t.Fatalf("Authors=%+v", authors)
	}

	authorBooks, err := cat.AuthorBooks(t.Context(), "user-1", "42")
	if err != nil {
		t.Fatalf("AuthorBooks error: %v", err)
	}
	if len(authorBooks) != 1 || authorBooks[0].Title != "Recent Audio Book" {
		t.Fatalf("AuthorBooks=%+v", authorBooks)
	}

	seriesList, err := cat.Series(t.Context())
	if err != nil {
		t.Fatalf("Series error: %v", err)
	}
	if len(seriesList) != 1 || seriesList[0].Name != "Vaporwave Series" {
		t.Fatalf("Series=%+v", seriesList)
	}

	seriesBooks, err := cat.SeriesBooks(t.Context(), "user-1", "Vaporwave Series")
	if err != nil {
		t.Fatalf("SeriesBooks error: %v", err)
	}
	if len(seriesBooks) != 1 || seriesBooks[0].Title != "Recent Audio Book" {
		t.Fatalf("SeriesBooks=%+v", seriesBooks)
	}
}

