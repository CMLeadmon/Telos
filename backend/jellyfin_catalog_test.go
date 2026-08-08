package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMediaKindFromJellyfin(t *testing.T) {
	cases := map[string]CatalogKind{
		"Movie":   "video",
		"Episode": "video",
		"Audio":   "audio",
		"Song":    "audio",
		"Unknown": "media",
	}
	for typ, want := range cases {
		got := mediaKindFromJellyfin(typ, false)
		if got != want {
			t.Errorf("typ %s: got=%s want=%s", typ, got, want)
		}
	}
	if got := mediaKindFromJellyfin("Movie", true); got != "folder" {
		t.Errorf("folder got=%s want=folder", got)
	}
}

func TestJellyfinCatalogDetailAndRelated(t *testing.T) {
	installCatalogIdentityTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Users/shared-user/Items/301":
			_, _ = fmt.Fprint(w, `{
				"Id": "301",
				"Name": "Synthwave Movie",
				"Type": "Movie",
				"Overview": "A cyber vaporwave journey.",
				"ProductionYear": 2026,
				"Genres": ["Sci-Fi", "Action"],
				"RunTimeTicks": 72000000000,
				"Chapters": [
					{"Name": "Intro", "StartPositionTicks": 0}
				]
			}`)
		case "/Items/301/Similar":
			_, _ = fmt.Fprint(w, `{
				"Items": [
					{"Id": "302", "Name": "Synthwave Sequel", "Type": "Movie", "RunTimeTicks": 54000000000}
				]
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldGetAuth := getJellyfinAuthToken
	getJellyfinAuthToken = func(_ context.Context) (string, string, error) {
		return "test-token", "shared-user", nil
	}
	t.Cleanup(func() { getJellyfinAuthToken = oldGetAuth })

	oldGetURL := getJellyfinBaseURL
	getJellyfinBaseURL = func() string { return server.URL }
	t.Cleanup(func() { getJellyfinBaseURL = oldGetURL })

	// Seed the parent so its canonical id maps to upstream 301. The previous
	// fixture stubbed resolveCatalogIdentity to answer UpstreamID "301" for every
	// input, which is not how resolution behaves: the similar item then also
	// resolved to 301 and failed the consistency check inside observe.
	parent, err := observeJellyfinCatalogItem(t.Context(), "301", "lib-allowed", "Movie", false)
	if err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	cat := &JellyfinCatalog{}
	detail, res, err := cat.Detail(t.Context(), "user-1", parent.ID)
	if err != nil {
		t.Fatalf("Detail error: %v", err)
	}
	if res.ID != parent.ID || detail.ID != parent.ID || detail.Title != "Synthwave Movie" || detail.ProductionYear != 2026 {
		t.Fatalf("detail=%+v res=%+v", detail, res)
	}
	if len(detail.Chapters) != 1 || detail.Chapters[0].Title != "Intro" {
		t.Fatalf("chapters=%+v", detail.Chapters)
	}

	related, err := cat.Related(t.Context(), "user-1", parent.ID, 5)
	if err != nil {
		t.Fatalf("Related error: %v", err)
	}
	if len(related) != 1 || related[0].Title != "Synthwave Sequel" {
		t.Fatalf("related=%+v", related)
	}
	// Related used to emit the upstream Jellyfin id verbatim, so the card 404'd
	// on open and its cover 404'd with it.
	if related[0].ID == "302" || !looksLikeUUID(related[0].ID) {
		t.Errorf("related id=%q, want a canonical UUID rather than the upstream id", related[0].ID)
	}
	if related[0].CoverURL != "/api/v1/media/items/"+related[0].ID+"/cover" {
		t.Errorf("coverUrl=%q must address the canonical id", related[0].CoverURL)
	}
}

// The Recently Added shelf runs Latest queries against every Jellyfin view, so
// it has to filter and canonicalize exactly like the browse listing. Returning
// upstream Jellyfin IDs makes every card 404 on open and puts a provider ID in
// any share link built from it; skipping the allowlist surfaces libraries the
// node never authorized.
func TestJellyfinCatalogRecentCanonicalizesAndFiltersByAllowlist(t *testing.T) {
	installCatalogIdentityTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/Users/shared-user/Views":
			_, _ = fmt.Fprint(w, `{"Items":[
				{"Id":"lib-allowed","Name":"Movies","Type":"CollectionFolder","CollectionType":"movies"},
				{"Id":"lib-denied","Name":"Private","Type":"CollectionFolder","CollectionType":"movies"}
			]}`)
		case r.URL.Path == "/Users/shared-user/Items/Latest":
			switch r.URL.Query().Get("ParentId") {
			case "lib-allowed":
				_, _ = fmt.Fprint(w, `[{"Id":"allowed-1","Name":"Allowed Film","Type":"Movie","RunTimeTicks":36000000000,"DateCreated":"2026-07-31T00:00:00Z"}]`)
			default:
				_, _ = fmt.Fprint(w, `[{"Id":"denied-1","Name":"Out Of Scope","Type":"Movie","RunTimeTicks":36000000000,"DateCreated":"2026-07-30T00:00:00Z"}]`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldGetAuth := getJellyfinAuthToken
	getJellyfinAuthToken = func(_ context.Context) (string, string, error) {
		return "test-token", "shared-user", nil
	}
	t.Cleanup(func() { getJellyfinAuthToken = oldGetAuth })

	oldGetURL := getJellyfinBaseURL
	getJellyfinBaseURL = func() string { return server.URL }
	t.Cleanup(func() { getJellyfinBaseURL = oldGetURL })

	oldAuthorizer := jellyfinAuthorizer
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(
		stubJellyfinResolver{
			"allowed-1": {MediaType: "Movie", AncestorIDs: []string{"lib-allowed"}},
			"denied-1":  {MediaType: "Movie", AncestorIDs: []string{"lib-denied"}},
		},
		[]string{"lib-allowed"},
	)
	t.Cleanup(func() { jellyfinAuthorizer = oldAuthorizer })

	items, err := (&JellyfinCatalog{}).Recent(t.Context(), "user-1", 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%+v, want only the allowlisted library's item", items)
	}
	if items[0].Title != "Allowed Film" {
		t.Fatalf("title=%q, want Allowed Film", items[0].Title)
	}
	if !looksLikeUUID(items[0].ID) {
		t.Errorf("id=%q, want a canonical UUID rather than the upstream Jellyfin ID", items[0].ID)
	}
	if items[0].CoverURL != "/api/v1/media/items/"+items[0].ID+"/cover" {
		t.Errorf("coverUrl=%q must address the canonical id", items[0].CoverURL)
	}

	// A node with no JELLYFIN_LIBRARY_IDS runs allow-all with a nil
	// authorizer. Calling straight through it panics and takes the request
	// down, so the shelf has to handle that mode the way browse does.
	jellyfinAuthorizer = nil
	allowAll, err := (&JellyfinCatalog{}).Recent(t.Context(), "user-1", 10)
	if err != nil {
		t.Fatalf("Recent with allow-all: %v", err)
	}
	if len(allowAll) != 2 {
		t.Fatalf("allow-all items=%+v, want both views", allowAll)
	}
	for _, item := range allowAll {
		if !looksLikeUUID(item.ID) {
			t.Errorf("allow-all id=%q, want a canonical UUID", item.ID)
		}
	}
}

// stubJellyfinResolver answers the authorizer's ancestry probe from a fixture
// so the allowlist can be exercised without a live Jellyfin.
type stubJellyfinResolver map[string]resolvedItem

func (r stubJellyfinResolver) ResolveItems(_ context.Context, itemIDs []string) (map[string]resolvedItem, error) {
	out := map[string]resolvedItem{}
	for _, id := range itemIDs {
		if item, ok := r[id]; ok {
			out[id] = item
		}
	}
	return out, nil
}

// Related had the same defect Recent was already fixed for: it emitted upstream
// Jellyfin ids with no allowlist filter, so cards 404'd on open, covers 404'd,
// a share link carried a provider id into chat, and items from libraries outside
// JELLYFIN_LIBRARY_IDS surfaced.
func TestJellyfinCatalogRelatedCanonicalizesAndFiltersByAllowlist(t *testing.T) {
	installCatalogIdentityTest(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Items/301/Similar" {
			_, _ = fmt.Fprint(w, `{"Items":[
				{"Id":"allowed-2","Name":"Allowed Sequel","Type":"Movie","RunTimeTicks":54000000000},
				{"Id":"denied-2","Name":"Out Of Scope","Type":"Movie","RunTimeTicks":54000000000}
			]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	oldGetAuth := getJellyfinAuthToken
	getJellyfinAuthToken = func(_ context.Context) (string, string, error) {
		return "test-token", "shared-user", nil
	}
	t.Cleanup(func() { getJellyfinAuthToken = oldGetAuth })

	oldGetURL := getJellyfinBaseURL
	getJellyfinBaseURL = func() string { return server.URL }
	t.Cleanup(func() { getJellyfinBaseURL = oldGetURL })

	oldAuthorizer := jellyfinAuthorizer
	jellyfinAuthorizer, _ = NewJellyfinAuthorizer(
		stubJellyfinResolver{
			"301":       {MediaType: "Movie", AncestorIDs: []string{"lib-allowed"}},
			"allowed-2": {MediaType: "Movie", AncestorIDs: []string{"lib-allowed"}},
			"denied-2":  {MediaType: "Movie", AncestorIDs: []string{"lib-denied"}},
		},
		[]string{"lib-allowed"},
	)
	t.Cleanup(func() { jellyfinAuthorizer = oldAuthorizer })

	parent, err := observeJellyfinCatalogItem(t.Context(), "301", "lib-allowed", "Movie", false)
	if err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	related, err := (&JellyfinCatalog{}).Related(t.Context(), "user-1", parent.ID, 10)
	if err != nil {
		t.Fatalf("Related: %v", err)
	}
	if len(related) != 1 {
		t.Fatalf("related=%+v, want only the allowlisted library's item", related)
	}
	if related[0].Title != "Allowed Sequel" {
		t.Fatalf("title=%q, want Allowed Sequel", related[0].Title)
	}
	if related[0].ID == "allowed-2" || !looksLikeUUID(related[0].ID) {
		t.Errorf("id=%q, want a canonical UUID rather than the upstream Jellyfin id", related[0].ID)
	}
	if related[0].CoverURL != "/api/v1/media/items/"+related[0].ID+"/cover" {
		t.Errorf("coverUrl=%q must address the canonical id", related[0].CoverURL)
	}
}
