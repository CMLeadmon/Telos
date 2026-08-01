package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func catalogFixture(t *testing.T) (*CatalogRepository, *pgxpool.Pool) {
	t.Helper()
	f := withFixture(t)
	return NewCatalogRepository(f.DB), f.DB
}

func observeCatalog(t *testing.T, repo *CatalogRepository, provider CatalogProvider, upstreamID, libraryID string, surface CatalogSurface, kind CatalogKind) CatalogResolution {
	t.Helper()
	got, err := repo.Observe(t.Context(), CatalogObservation{
		Provider: provider, UpstreamID: upstreamID, LibraryID: libraryID,
		Surface: surface, Kind: kind,
	})
	if err != nil {
		t.Fatalf("Observe(%q, %q): %v", provider, upstreamID, err)
	}
	return got
}

func TestCatalogObserveCreatesAndRefreshesStableIdentity(t *testing.T) {
	repo, db := catalogFixture(t)
	first := observeCatalog(t, repo, ProviderGrimmory, "book-7", "shelf-a", SurfaceLibrary, "audiobook")
	if first.ID == "" || first.Provider != ProviderGrimmory || !first.Active || !first.Available || first.Revision != 1 {
		t.Fatalf("first resolution = %+v", first)
	}

	var originalSeen time.Time
	if err := db.QueryRow(t.Context(), `
		UPDATE catalog_sources
		SET last_seen_at = now() - interval '1 hour', available = false, missing_since = now() - interval '1 hour'
		WHERE provider = 'grimmory' AND upstream_id = 'book-7'
		RETURNING last_seen_at`).Scan(&originalSeen); err != nil {
		t.Fatal(err)
	}

	second := observeCatalog(t, repo, ProviderGrimmory, "book-7", "shelf-b", SurfaceStream, "video")
	if second.ID != first.ID {
		t.Fatalf("second ID = %q, want original %q", second.ID, first.ID)
	}
	if second.Surface != SurfaceLibrary || second.Kind != "audiobook" {
		t.Fatalf("existing catalog identity was retyped: %+v", second)
	}
	if !second.Available || second.LibraryID != "shelf-b" {
		t.Fatalf("source was not restored/refreshed: %+v", second)
	}

	var itemCount, sourceCount int
	var refreshedSeen time.Time
	var missingSince *time.Time
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM catalog_items`).Scan(&itemCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(t.Context(), `
		SELECT count(*), max(last_seen_at), max(missing_since)
		FROM catalog_sources WHERE provider = 'grimmory' AND upstream_id = 'book-7'`).Scan(&sourceCount, &refreshedSeen, &missingSince); err != nil {
		t.Fatal(err)
	}
	if itemCount != 1 || sourceCount != 1 {
		t.Fatalf("item/source counts = %d/%d, want 1/1", itemCount, sourceCount)
	}
	if !refreshedSeen.After(originalSeen) || missingSince != nil {
		t.Fatalf("last_seen_at=%v original=%v missing_since=%v", refreshedSeen, originalSeen, missingSince)
	}
}

func TestCatalogResolveCanonicalLegacyWrongSurfaceAndMissing(t *testing.T) {
	repo, _ := catalogFixture(t)
	created := observeCatalog(t, repo, ProviderJellyfin, "movie-17", "movies", SurfaceStream, "video")

	for _, rawID := range []string{created.ID, "movie-17"} {
		got, err := repo.Resolve(t.Context(), rawID)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", rawID, err)
		}
		if got != created {
			t.Fatalf("Resolve(%q) = %+v, want %+v", rawID, got, created)
		}
	}
	if _, err := repo.ResolveFor(t.Context(), created.ID, SurfaceLibrary); !errors.Is(err, errCatalogWrongSurface) {
		t.Fatalf("wrong surface error = %v", err)
	}
	if _, err := repo.Resolve(t.Context(), "absent"); !errors.Is(err, errCatalogNotFound) {
		t.Fatalf("missing error = %v", err)
	}
}

func TestCatalogResolveLegacyAliasReturnsActiveSource(t *testing.T) {
	repo, db := catalogFixture(t)
	active := observeCatalog(t, repo, ProviderJellyfin, "current-movie", "movies", SurfaceStream, "video")
	if _, err := db.Exec(t.Context(), `
		INSERT INTO catalog_sources
			(catalog_item_id, provider, upstream_id, upstream_library_id, active, available)
		VALUES ($1::uuid, 'jellyfin', 'legacy-movie', 'old-movies', false, false)`, active.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Resolve(t.Context(), "legacy-movie")
	if err != nil {
		t.Fatal(err)
	}
	if got != active {
		t.Fatalf("legacy alias resolution = %+v, want active source %+v", got, active)
	}
}

func TestCatalogResolveRejectsProviderCollision(t *testing.T) {
	repo, _ := catalogFixture(t)
	observeCatalog(t, repo, ProviderGrimmory, "shared-legacy-id", "books", SurfaceLibrary, "epub")
	observeCatalog(t, repo, ProviderJellyfin, "shared-legacy-id", "music", SurfaceStream, "audio")

	if _, err := repo.Resolve(t.Context(), "shared-legacy-id"); !errors.Is(err, errCatalogInvalid) {
		t.Fatalf("collision error = %v, want errCatalogInvalid", err)
	}
}

func TestCatalogObserveSupportsFolderKind(t *testing.T) {
	repo, _ := catalogFixture(t)
	got := observeCatalog(t, repo, ProviderGrimmory, "folder-1", "books", SurfaceLibrary, "folder")
	if got.Kind != "folder" {
		t.Fatalf("kind = %q, want folder", got.Kind)
	}
}

func TestCatalogCompleteScanRestoresSeenAndMarksOnlyCompletedScanMissing(t *testing.T) {
	repo, db := catalogFixture(t)
	seen := observeCatalog(t, repo, ProviderGrimmory, "seen", "books", SurfaceLibrary, "epub")
	unseen := observeCatalog(t, repo, ProviderGrimmory, "unseen", "books", SurfaceLibrary, "pdf")
	otherLibrary := observeCatalog(t, repo, ProviderGrimmory, "other-library", "archive", SurfaceLibrary, "epub")
	otherProvider := observeCatalog(t, repo, ProviderJellyfin, "other-provider", "books", SurfaceStream, "video")
	if _, err := db.Exec(t.Context(), `
		UPDATE catalog_sources SET available = false, missing_since = now() - interval '1 hour'
		WHERE provider = 'grimmory' AND upstream_id = 'seen'`); err != nil {
		t.Fatal(err)
	}

	report, err := repo.CompleteScan(t.Context(), ProviderGrimmory, "books", []string{"seen", "seen"})
	if err != nil {
		t.Fatal(err)
	}
	if report != (CatalogScanReport{Seen: 1, Restored: 1, Missing: 1}) {
		t.Fatalf("report = %+v", report)
	}

	assertCatalogAvailability(t, repo, seen.ID, true)
	assertCatalogAvailability(t, repo, unseen.ID, false)
	assertCatalogAvailability(t, repo, otherLibrary.ID, true)
	assertCatalogAvailability(t, repo, otherProvider.ID, true)
}

func TestCatalogCompleteScanTracksInactiveAliases(t *testing.T) {
	repo, db := catalogFixture(t)
	seen := observeCatalog(t, repo, ProviderJellyfin, "old-seen", "movies", SurfaceStream, "video")
	unseen := observeCatalog(t, repo, ProviderJellyfin, "old-unseen", "movies", SurfaceStream, "video")
	if _, err := db.Exec(t.Context(), `
		UPDATE catalog_sources
		SET active = false,
			available = CASE WHEN upstream_id = 'old-seen' THEN false ELSE true END,
			missing_since = CASE WHEN upstream_id = 'old-seen' THEN now() - interval '1 hour' ELSE NULL END
		WHERE catalog_item_id IN ($1::uuid, $2::uuid)`, seen.ID, unseen.ID); err != nil {
		t.Fatal(err)
	}

	report, err := repo.CompleteScan(t.Context(), ProviderJellyfin, "movies", []string{"old-seen"})
	if err != nil {
		t.Fatal(err)
	}
	if report != (CatalogScanReport{Seen: 1, Restored: 1, Missing: 1}) {
		t.Fatalf("report = %+v", report)
	}
	for id, want := range map[string]bool{seen.ID: true, unseen.ID: false} {
		var available bool
		if err := db.QueryRow(t.Context(), `
			SELECT available FROM catalog_sources WHERE catalog_item_id = $1::uuid`, id).Scan(&available); err != nil {
			t.Fatal(err)
		}
		if available != want {
			t.Fatalf("catalog item %s available = %v, want %v", id, available, want)
		}
	}
}

func TestCatalogEnumerationFailureLeavesAvailabilityUnchanged(t *testing.T) {
	repo, _ := catalogFixture(t)
	created := observeCatalog(t, repo, ProviderJellyfin, "movie", "movies", SurfaceStream, "video")
	if err := RecordCatalogEnumerationFailure(ProviderJellyfin, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	assertCatalogAvailability(t, repo, created.ID, true)
}

func TestCatalogConcurrentObservationReturnsOneIdentity(t *testing.T) {
	repo, db := catalogFixture(t)
	const workers = 12
	start := make(chan struct{})
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := repo.Observe(context.Background(), CatalogObservation{
				Provider: ProviderJellyfin, UpstreamID: "concurrent", LibraryID: "movies",
				Surface: SurfaceStream, Kind: "video",
			})
			if err != nil {
				errs <- err
				return
			}
			ids <- got.ID
		}()
	}
	close(start)
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Observe: %v", err)
		}
	}
	var want string
	for id := range ids {
		if want == "" {
			want = id
		}
		if id != want {
			t.Fatalf("ID = %q, want %q", id, want)
		}
	}
	var items, sources int
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM catalog_items`).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(t.Context(), `SELECT count(*) FROM catalog_sources`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if items != 1 || sources != 1 {
		t.Fatalf("item/source counts = %d/%d, want 1/1", items, sources)
	}
}

func assertCatalogAvailability(t *testing.T, repo *CatalogRepository, id string, want bool) {
	t.Helper()
	got, err := repo.Resolve(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Available != want {
		t.Fatalf("catalog item %s available = %v, want %v", id, got.Available, want)
	}
}
