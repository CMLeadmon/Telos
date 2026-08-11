package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type recordingCatalogReconciler struct {
	events      []string
	observeFail string
}

func (r *recordingCatalogReconciler) Observe(_ context.Context, in CatalogObservation) (CatalogResolution, error) {
	r.events = append(r.events, "observe:"+in.UpstreamID)
	if in.UpstreamID == r.observeFail {
		return CatalogResolution{}, errors.New("forced observation failure")
	}
	return CatalogResolution{ID: in.UpstreamID, LibraryID: in.LibraryID}, nil
}

func (r *recordingCatalogReconciler) CompleteScan(_ context.Context, provider CatalogProvider, libraryID string, seen []string) (CatalogScanReport, error) {
	r.events = append(r.events, "scan:"+string(provider)+":"+libraryID+":"+joinCatalogIDs(seen))
	return CatalogScanReport{Seen: len(seen)}, nil
}

func TestReconcileCompleteCatalogEnumerationObservesThenScansThenBackfills(t *testing.T) {
	repo := &recordingCatalogReconciler{}
	enumeration := CatalogEnumeration{
		Provider:  ProviderGrimmory,
		Libraries: []string{"library-a", "library-b"},
		Observations: []CatalogObservation{
			{Provider: ProviderGrimmory, UpstreamID: "book-2", LibraryID: "library-b", Surface: SurfaceLibrary, Kind: "pdf"},
			{Provider: ProviderGrimmory, UpstreamID: "book-1", LibraryID: "library-a", Surface: SurfaceLibrary, Kind: "epub"},
		},
	}
	backfill := func(context.Context, DBTX) (CatalogBackfillReport, error) {
		repo.events = append(repo.events, "backfill")
		return CatalogBackfillReport{Annotations: 1, Updated: 1}, nil
	}

	report, err := ReconcileCompleteCatalogEnumeration(t.Context(), repo, nil, enumeration, backfill)
	if err != nil {
		t.Fatalf("reconcile complete enumeration: %v", err)
	}
	wantEvents := []string{
		"observe:book-2",
		"observe:book-1",
		"scan:grimmory:library-a:book-1",
		"scan:grimmory:library-b:book-2",
		"backfill",
	}
	if !reflect.DeepEqual(repo.events, wantEvents) {
		t.Fatalf("events = %v, want %v", repo.events, wantEvents)
	}
	if report.Observed != 2 || report.Scans["library-a"].Seen != 1 || report.Backfill.Updated != 1 {
		t.Fatalf("report = %+v", report)
	}
	// Enumeration callers read the canonical identity from here. Dropping it
	// would send them back to observing every record a second time.
	if len(report.Resolutions) != 2 ||
		report.Resolutions["book-1"].ID != "book-1" ||
		report.Resolutions["book-2"].ID != "book-2" {
		t.Fatalf("resolutions = %+v, want the identity Observe returned for each record", report.Resolutions)
	}
}

func TestReconcileCompleteCatalogEnumerationDoesNotScanOrBackfillAfterObservationFailure(t *testing.T) {
	repo := &recordingCatalogReconciler{observeFail: "bad-book"}
	enumeration := CatalogEnumeration{
		Provider:  ProviderGrimmory,
		Libraries: []string{"library-a"},
		Observations: []CatalogObservation{
			{Provider: ProviderGrimmory, UpstreamID: "good-book", LibraryID: "library-a", Surface: SurfaceLibrary, Kind: "epub"},
			{Provider: ProviderGrimmory, UpstreamID: "bad-book", LibraryID: "library-a", Surface: SurfaceLibrary, Kind: "pdf"},
		},
	}
	backfillCalled := false
	backfill := func(context.Context, DBTX) (CatalogBackfillReport, error) {
		backfillCalled = true
		return CatalogBackfillReport{}, nil
	}

	report, err := ReconcileCompleteCatalogEnumeration(t.Context(), repo, nil, enumeration, backfill)
	if err == nil {
		t.Fatal("reconciliation succeeded despite observation failure")
	}
	if report.Observed != 1 {
		t.Fatalf("observed report = %d, want one committed observation", report.Observed)
	}
	if backfillCalled {
		t.Fatal("backfill ran after an incomplete observation pass")
	}
	wantEvents := []string{"observe:good-book", "observe:bad-book"}
	if !reflect.DeepEqual(repo.events, wantEvents) {
		t.Fatalf("events = %v, want %v", repo.events, wantEvents)
	}
}

func TestReconcileCompleteCatalogEnumerationReturnsPartialBackfillReport(t *testing.T) {
	repo := &recordingCatalogReconciler{}
	enumeration := CatalogEnumeration{
		Provider:  ProviderGrimmory,
		Libraries: []string{"library-a"},
		Observations: []CatalogObservation{
			{Provider: ProviderGrimmory, UpstreamID: "book-1", LibraryID: "library-a", Surface: SurfaceLibrary, Kind: "epub"},
		},
	}
	backfillErr := errors.New("forced later-batch failure")
	backfill := func(context.Context, DBTX) (CatalogBackfillReport, error) {
		return CatalogBackfillReport{Progress: 2, Annotations: 1, Updated: 3}, backfillErr
	}

	report, err := ReconcileCompleteCatalogEnumeration(t.Context(), repo, nil, enumeration, backfill)
	if !errors.Is(err, backfillErr) {
		t.Fatalf("error = %v, want partial backfill error", err)
	}
	if report.Observed != 1 || report.Scans["library-a"].Seen != 1 {
		t.Fatalf("reconciliation prefix report = %+v", report)
	}
	if report.Backfill.Progress != 2 || report.Backfill.Annotations != 1 || report.Backfill.Updated != 3 {
		t.Fatalf("partial backfill report = %+v", report.Backfill)
	}
}

func TestEnumerateJellyfinCatalogReturnsOnlyCompleteAllowedLibraries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users":
			fmt.Fprint(w, `[{"Id":"user-1","Name":"Telos"}]`)
		case "/Users/user-1/Views":
			fmt.Fprint(w, `{"Items":[{"Id":"movies"},{"Id":"private"}]}`)
		case "/Users/user-1/Items":
			if r.URL.Query().Get("ParentId") != "movies" || r.URL.Query().Get("Recursive") != "true" {
				http.Error(w, "unexpected enumeration query", http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `{"Items":[{"Id":"film-1","Type":"Movie"},{"Id":"season-1","Type":"Season","IsFolder":true},{"Id":"person-1","Type":"Person"}],"TotalRecordCount":3}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	oldBase, oldRedis, oldAuthorizer := jellyfinBaseURL, redisClient, jellyfinAuthorizer
	jellyfinBaseURL, redisClient = server.URL, nil
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(&fakeJellyfinResolver{}, []string{"movies"})
	t.Cleanup(func() {
		jellyfinBaseURL, redisClient, jellyfinAuthorizer = oldBase, oldRedis, oldAuthorizer
	})

	enumeration, err := enumerateJellyfinCatalog(t.Context())
	if err != nil {
		t.Fatalf("enumerate Jellyfin catalog: %v", err)
	}
	if !reflect.DeepEqual(enumeration.Libraries, []string{"movies"}) {
		t.Fatalf("libraries = %v, want only configured library", enumeration.Libraries)
	}
	want := []CatalogObservation{
		{Provider: ProviderJellyfin, UpstreamID: "movies", LibraryID: "movies", Surface: SurfaceStream, Kind: "folder"},
		{Provider: ProviderJellyfin, UpstreamID: "film-1", LibraryID: "movies", Surface: SurfaceStream, Kind: "video"},
		{Provider: ProviderJellyfin, UpstreamID: "season-1", LibraryID: "movies", Surface: SurfaceStream, Kind: "folder"},
	}
	if !reflect.DeepEqual(enumeration.Observations, want) {
		t.Fatalf("observations = %+v, want %+v", enumeration.Observations, want)
	}
}

func TestRunJellyfinReconciliationRejectsPartialEnumerationBeforeReconcile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users":
			fmt.Fprint(w, `[{"Id":"user-1","Name":"Telos"}]`)
		case "/Users/user-1/Views":
			fmt.Fprint(w, `{"Items":[{"Id":"movies"}]}`)
		case "/Users/user-1/Items":
			if r.URL.Query().Get("StartIndex") == "0" {
				fmt.Fprint(w, `{"Items":[{"Id":"film-1","Type":"Movie"}],"TotalRecordCount":2}`)
				return
			}
			http.Error(w, "forced second-page failure", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	oldBase, oldRedis, oldAuthorizer, oldReconcile := jellyfinBaseURL, redisClient, jellyfinAuthorizer, reconcileCatalogEnumeration
	jellyfinBaseURL, redisClient = server.URL, nil
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(&fakeJellyfinResolver{}, []string{"movies"})
	reconciled := false
	reconcileCatalogEnumeration = func(context.Context, CatalogEnumeration) (CatalogReconciliationReport, error) {
		reconciled = true
		return CatalogReconciliationReport{}, nil
	}
	t.Cleanup(func() {
		jellyfinBaseURL, redisClient, jellyfinAuthorizer, reconcileCatalogEnumeration = oldBase, oldRedis, oldAuthorizer, oldReconcile
	})

	if _, err := runJellyfinCatalogReconciliation(t.Context()); err == nil {
		t.Fatal("partial Jellyfin enumeration unexpectedly reconciled")
	}
	if reconciled {
		t.Fatal("partial Jellyfin enumeration reached CompleteScan/backfill coordinator")
	}
}

func TestRunJellyfinReconciliationRejectsPrematureEmptyPageBeforeReconcile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users":
			fmt.Fprint(w, `[{"Id":"user-1","Name":"Telos"}]`)
		case "/Users/user-1/Views":
			fmt.Fprint(w, `{"Items":[{"Id":"movies"}]}`)
		case "/Users/user-1/Items":
			if r.URL.Query().Get("StartIndex") == "0" {
				fmt.Fprint(w, `{"Items":[{"Id":"film-1","Type":"Movie"}],"TotalRecordCount":2}`)
				return
			}
			fmt.Fprint(w, `{"Items":[],"TotalRecordCount":2}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	oldBase, oldRedis, oldAuthorizer, oldReconcile := jellyfinBaseURL, redisClient, jellyfinAuthorizer, reconcileCatalogEnumeration
	jellyfinBaseURL, redisClient = server.URL, nil
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(&fakeJellyfinResolver{}, []string{"movies"})
	reconciled := false
	reconcileCatalogEnumeration = func(context.Context, CatalogEnumeration) (CatalogReconciliationReport, error) {
		reconciled = true
		return CatalogReconciliationReport{}, nil
	}
	t.Cleanup(func() {
		jellyfinBaseURL, redisClient, jellyfinAuthorizer, reconcileCatalogEnumeration = oldBase, oldRedis, oldAuthorizer, oldReconcile
	})

	if _, err := runJellyfinCatalogReconciliation(t.Context()); err == nil {
		t.Fatal("premature empty Jellyfin page unexpectedly completed enumeration")
	}
	if reconciled {
		t.Fatal("premature empty Jellyfin page reached CompleteScan/backfill coordinator")
	}
}

func joinCatalogIDs(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	out := ids[0]
	for _, id := range ids[1:] {
		out += "," + id
	}
	return out
}
