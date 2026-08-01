package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeJellyfinResolver struct {
	items map[string]resolvedItem
	calls atomic.Int32
}

func (f *fakeJellyfinResolver) ResolveItems(_ context.Context, ids []string) (map[string]resolvedItem, error) {
	f.calls.Add(1)
	out := map[string]resolvedItem{}
	for _, id := range ids {
		if ri, ok := f.items[id]; ok {
			out[id] = ri
		}
	}
	return out, nil
}

func TestJellyfinAuthorizer(t *testing.T) {
	resolver := &fakeJellyfinResolver{items: map[string]resolvedItem{
		"inlib":  {Name: "Movie", MediaType: "Video", AncestorIDs: []string{"root", "lib-allowed"}},
		"outlib": {Name: "Other", MediaType: "Video", AncestorIDs: []string{"root", "lib-forbidden"}},
	}}
	a, err := NewJellyfinAuthorizer(resolver, []string{"lib-allowed"})
	if err != nil {
		t.Fatal(err)
	}

	// In-library item authorizes.
	item, err := a.AuthorizeItem(context.Background(), "inlib")
	if err != nil || item.LibraryID != "lib-allowed" {
		t.Fatalf("in-library item denied: %v (%+v)", err, item)
	}
	// Out-of-library item is denied indistinguishably.
	if _, err := a.AuthorizeItem(context.Background(), "outlib"); err == nil {
		t.Fatal("out-of-library item authorized")
	}
	// Unknown item denied.
	if _, err := a.AuthorizeItem(context.Background(), "ghost"); err == nil {
		t.Fatal("unknown item authorized")
	}

	// Empty library allowlist is rejected at construction.
	if _, err := NewJellyfinAuthorizer(resolver, nil); err == nil {
		t.Fatal("empty allowlist accepted")
	}
}

func TestJellyfinAuthorizerBatchIsBounded(t *testing.T) {
	items := map[string]resolvedItem{}
	ids := make([]string, 50)
	for i := 0; i < 50; i++ {
		id := "item" + itoa(i)
		ids[i] = id
		items[id] = resolvedItem{Name: id, AncestorIDs: []string{"lib-allowed"}}
	}
	resolver := &fakeJellyfinResolver{items: items}
	a, _ := NewJellyfinAuthorizer(resolver, []string{"lib-allowed"})

	// A 50-item batch (with duplicates) uses ONE upstream call, not N+1.
	dup := append(append([]string{}, ids...), ids...) // 100 with dupes
	res, err := a.AuthorizeItems(context.Background(), dup)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 50 {
		t.Fatalf("authorized %d items, want 50", len(res))
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("resolver called %d times, want 1 (no N+1)", resolver.calls.Load())
	}

	// A second call is served from the positive cache — no new upstream call.
	a.AuthorizeItems(context.Background(), ids)
	if resolver.calls.Load() != 1 {
		t.Fatalf("cache miss: resolver called %d times", resolver.calls.Load())
	}
}

func TestLegacyJellyfinIDResolvesBeforeAuthorizationAndUpstream(t *testing.T) {
	events := []string{}
	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		events = append(events, "resolve:"+rawID)
		return CatalogResolution{
			ID: "00000000-0000-4000-8000-000000000017", Surface: surface,
			Kind: "video", Provider: ProviderJellyfin, UpstreamID: "current-movie",
			LibraryID: "lib-allowed", Active: true, Available: true,
		}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })

	resolver := &eventJellyfinResolver{events: &events}
	oldAuthorizer := jellyfinAuthorizer
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(resolver, []string{"lib-allowed"})
	t.Cleanup(func() { jellyfinAuthorizer = oldAuthorizer })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events = append(events, "upstream:"+r.URL.Path)
		fmt.Fprint(w, "jpeg")
	}))
	defer upstream.Close()
	oldBase := jellyfinBaseURL
	jellyfinBaseURL = upstream.URL
	t.Cleanup(func() { jellyfinBaseURL = oldBase })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/items/legacy-movie/cover", nil)
	req.SetPathValue("id", "legacy-movie")
	rec := httptest.NewRecorder()
	handleMediaItemCover(rec, req)

	want := []string{"resolve:legacy-movie", "authorize:current-movie", "upstream:/Items/current-movie/Images/Primary"}
	if strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestFreshLegacyJellyfinDeepLinkDiscoversCurrentAuthorizedItem(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000121"
	var observed CatalogObservation
	oldObserve, oldResolve := observeCatalogIdentity, resolveCatalogIdentity
	observeCatalogIdentity = func(_ context.Context, in CatalogObservation) (CatalogResolution, error) {
		observed = in
		return CatalogResolution{ID: canonicalID, Provider: in.Provider, UpstreamID: in.UpstreamID,
			LibraryID: in.LibraryID, Surface: in.Surface, Kind: in.Kind, Active: true, Available: true}, nil
	}
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		if rawID == "fresh-movie" {
			return CatalogResolution{}, errCatalogNotFound
		}
		if rawID == canonicalID {
			return CatalogResolution{ID: canonicalID, Provider: ProviderJellyfin, UpstreamID: "fresh-movie",
				LibraryID: "lib-allowed", Surface: surface, Kind: "video", Active: true, Available: true}, nil
		}
		return CatalogResolution{}, errors.New("unexpected catalog identifier")
	}
	t.Cleanup(func() { observeCatalogIdentity, resolveCatalogIdentity = oldObserve, oldResolve })

	oldAuthorizer := jellyfinAuthorizer
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(&fakeJellyfinResolver{items: map[string]resolvedItem{
		"fresh-movie": {AncestorIDs: []string{"lib-allowed"}},
	}}, []string{"lib-allowed"})
	t.Cleanup(func() { jellyfinAuthorizer = oldAuthorizer })

	providerPaths := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerPaths = append(providerPaths, r.URL.Path)
		switch r.URL.Path {
		case "/Users":
			json.NewEncoder(w).Encode([]map[string]string{{"Id": "user-1", "Name": "admin"}})
		case "/Users/user-1/Items/fresh-movie":
			json.NewEncoder(w).Encode(map[string]any{"Id": "fresh-movie", "Name": "Fresh movie", "Type": "Movie", "IsFolder": false})
		case "/Items/fresh-movie/Images/Primary":
			fmt.Fprint(w, "jpeg")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	oldBase, oldRedis := jellyfinBaseURL, redisClient
	jellyfinBaseURL, redisClient = upstream.URL, nil
	t.Cleanup(func() { jellyfinBaseURL, redisClient = oldBase, oldRedis })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/items/fresh-movie/cover", nil)
	req.SetPathValue("id", "fresh-movie")
	rec := httptest.NewRecorder()
	handleMediaItemCover(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if observed.Provider != ProviderJellyfin || observed.UpstreamID != "fresh-movie" ||
		observed.LibraryID != "lib-allowed" || observed.Kind != "video" {
		t.Fatalf("observation=%+v, want current authorized Jellyfin movie", observed)
	}
	for _, path := range providerPaths {
		if strings.Contains(path, canonicalID) {
			t.Fatalf("canonical UUID reached Jellyfin: %q", path)
		}
	}
}

func TestObserveJellyfinCatalogItemSuppressesInactiveAliasAfterLibraryCutover(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000221"
	oldObserve, oldResolve, oldAuthorizer := observeCatalogIdentity, resolveCatalogIdentity, jellyfinAuthorizer
	jellyfinAuthorizer = nil
	observeCatalogIdentity = func(_ context.Context, in CatalogObservation) (CatalogResolution, error) {
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceStream, Kind: in.Kind, Provider: ProviderJellyfin,
			UpstreamID: in.UpstreamID, LibraryID: in.LibraryID, Active: false, Available: true,
		}, nil
	}
	resolveCatalogIdentity = func(context.Context, string, CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{}, errCatalogWrongSurface
	}
	t.Cleanup(func() {
		observeCatalogIdentity, resolveCatalogIdentity, jellyfinAuthorizer = oldObserve, oldResolve, oldAuthorizer
	})

	if _, err := observeJellyfinCatalogItem(t.Context(), "legacy-film", "movies", "Movie", false); err == nil {
		t.Fatal("inactive Jellyfin alias remained visible after active source moved to Library")
	}
}

func TestObserveJellyfinCatalogItemPublishesAliasAfterStreamRollback(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000222"
	oldObserve, oldResolve, oldAuthorizer := observeCatalogIdentity, resolveCatalogIdentity, jellyfinAuthorizer
	jellyfinAuthorizer = nil
	observeCatalogIdentity = func(_ context.Context, in CatalogObservation) (CatalogResolution, error) {
		return CatalogResolution{ID: canonicalID, Provider: in.Provider, UpstreamID: in.UpstreamID, LibraryID: in.LibraryID, Surface: in.Surface, Kind: in.Kind}, nil
	}
	resolveCatalogIdentity = func(context.Context, string, CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceStream, Kind: "video", Provider: ProviderJellyfin,
			UpstreamID: "film", LibraryID: "movies", Active: true, Available: true,
		}, nil
	}
	t.Cleanup(func() {
		observeCatalogIdentity, resolveCatalogIdentity, jellyfinAuthorizer = oldObserve, oldResolve, oldAuthorizer
	})

	resolution, err := observeJellyfinCatalogItem(t.Context(), "film", "movies", "Movie", false)
	if err != nil || resolution.ID != canonicalID {
		t.Fatalf("rollback resolution = %+v, err=%v", resolution, err)
	}
}

type eventJellyfinResolver struct{ events *[]string }

func (r *eventJellyfinResolver) ResolveItems(_ context.Context, ids []string) (map[string]resolvedItem, error) {
	out := make(map[string]resolvedItem, len(ids))
	for _, id := range ids {
		*r.events = append(*r.events, "authorize:"+id)
		out[id] = resolvedItem{MediaType: "Video", AncestorIDs: []string{"lib-allowed"}}
	}
	return out, nil
}

func TestCanonicalVideoRouteUsesUpstreamPlaybackIDAndCanonicalRedirect(t *testing.T) {
	canonicalID := "00000000-0000-4000-8000-000000000018"
	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{ID: canonicalID, Surface: surface, Kind: "video", Provider: ProviderJellyfin,
			UpstreamID: "movie-18", LibraryID: "lib-allowed", Active: true, Available: true}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })
	oldAuthorizer := jellyfinAuthorizer
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(&fakeJellyfinResolver{items: map[string]resolvedItem{
		"movie-18": {MediaType: "Video", AncestorIDs: []string{"lib-allowed"}},
	}}, []string{"lib-allowed"})
	t.Cleanup(func() { jellyfinAuthorizer = oldAuthorizer })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users":
			json.NewEncoder(w).Encode([]map[string]string{{"Id": "user-1", "Name": "admin"}})
		case "/Items/movie-18/PlaybackInfo":
			json.NewEncoder(w).Encode(map[string]string{"PlaySessionId": "play-18"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	oldBase, oldRedis := jellyfinBaseURL, redisClient
	jellyfinBaseURL, redisClient = upstream.URL, nil
	t.Cleanup(func() { jellyfinBaseURL, redisClient = oldBase, oldRedis })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/video/"+canonicalID, nil)
	req.SetPathValue("id", canonicalID)
	rec := httptest.NewRecorder()
	handleStreamVideo(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := "/api/v1/stream/video/" + canonicalID + "/main.m3u8?PlaySessionId=play-18"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("redirect = %q, want %q", got, want)
	}
}

func TestCanonicalAudioRouteUsesUpstreamJellyfinID(t *testing.T) {
	canonicalID := "00000000-0000-4000-8000-000000000020"
	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{ID: canonicalID, Surface: surface, Kind: "audio", Provider: ProviderJellyfin,
			UpstreamID: "track-20", LibraryID: "lib-allowed", Active: true, Available: true}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })
	oldAuthorizer := jellyfinAuthorizer
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(&fakeJellyfinResolver{items: map[string]resolvedItem{
		"track-20": {MediaType: "Audio", AncestorIDs: []string{"lib-allowed"}},
	}}, []string{"lib-allowed"})
	t.Cleanup(func() { jellyfinAuthorizer = oldAuthorizer })

	upstreamPath := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		fmt.Fprint(w, "audio")
	}))
	defer upstream.Close()
	oldBase := jellyfinBaseURL
	jellyfinBaseURL = upstream.URL
	t.Cleanup(func() { jellyfinBaseURL = oldBase })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/audio/"+canonicalID, nil)
	req.SetPathValue("id", canonicalID)
	rec := httptest.NewRecorder()
	handleStreamAudio(rec, req)
	if rec.Code != http.StatusOK || upstreamPath != "/Audio/track-20/stream" {
		t.Fatalf("status=%d upstream=%q body=%s", rec.Code, upstreamPath, rec.Body.String())
	}
}

func TestCanonicalHLSRouteKeepsUpstreamIDInsideOpaqueLocator(t *testing.T) {
	canonicalID := "00000000-0000-4000-8000-000000000019"
	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{ID: canonicalID, Surface: surface, Kind: "video", Provider: ProviderJellyfin,
			UpstreamID: "movie-19", LibraryID: "lib-allowed", Active: true, Available: true}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })
	oldAuthorizer := jellyfinAuthorizer
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(&fakeJellyfinResolver{items: map[string]resolvedItem{
		"movie-19": {MediaType: "Video", AncestorIDs: []string{"lib-allowed"}},
	}}, []string{"lib-allowed"})
	t.Cleanup(func() { jellyfinAuthorizer = oldAuthorizer })

	upstreamPath := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n/jellyfin/Videos/movie-19/seg.ts")
	}))
	defer upstream.Close()
	oldBase, oldKey := jellyfinBaseURL, hlsSigningKey
	jellyfinBaseURL, hlsSigningKey = upstream.URL, []byte("01234567890123456789012345678901")
	t.Cleanup(func() { jellyfinBaseURL, hlsSigningKey = oldBase, oldKey })

	userID := "00000000-0000-4000-8000-000000000111"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/video/"+canonicalID+"/main.m3u8", nil)
	req.SetPathValue("id", canonicalID)
	req.SetPathValue("path", "main.m3u8")
	req.AddCookie(&http.Cookie{Name: "telos_session", Value: "session19"})
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: userID}))
	rec := httptest.NewRecorder()
	handleStreamVideoSubpath(rec, req)
	if rec.Code != http.StatusOK || upstreamPath != "/Videos/movie-19/main.m3u8" {
		t.Fatalf("status=%d upstream=%q body=%s", rec.Code, upstreamPath, rec.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "/api/v1/hls/") {
		t.Fatalf("manifest = %q", rec.Body.String())
	}
	loc, err := verifyHLSLocator(strings.TrimPrefix(lines[1], "/api/v1/hls/"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if loc.Item != "movie-19" || loc.User != userID {
		t.Fatalf("locator = %+v, want internal upstream item", loc)
	}
}
