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
	const canonicalID = "00000000-0000-4000-8000-000000000301"

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

	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, _ string, _ CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceStream, Kind: "video", Provider: ProviderJellyfin,
			UpstreamID: "301", Active: true, Available: true,
		}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })

	oldGetAuth := getJellyfinAuthToken
	getJellyfinAuthToken = func(_ context.Context) (string, string, error) {
		return "test-token", "shared-user", nil
	}
	t.Cleanup(func() { getJellyfinAuthToken = oldGetAuth })

	oldGetURL := getJellyfinBaseURL
	getJellyfinBaseURL = func() string { return server.URL }
	t.Cleanup(func() { getJellyfinBaseURL = oldGetURL })

	cat := &JellyfinCatalog{}
	detail, res, err := cat.Detail(t.Context(), "user-1", canonicalID)
	if err != nil {
		t.Fatalf("Detail error: %v", err)
	}
	if res.ID != canonicalID || detail.ID != canonicalID || detail.Title != "Synthwave Movie" || detail.ProductionYear != 2026 {
		t.Fatalf("detail=%+v res=%+v", detail, res)
	}
	if len(detail.Chapters) != 1 || detail.Chapters[0].Title != "Intro" {
		t.Fatalf("chapters=%+v", detail.Chapters)
	}

	related, err := cat.Related(t.Context(), "user-1", canonicalID, 5)
	if err != nil {
		t.Fatalf("Related error: %v", err)
	}
	if len(related) != 1 || related[0].Title != "Synthwave Sequel" {
		t.Fatalf("related=%+v", related)
	}
}
