package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetPlaybackOptions(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000302"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Items/302/PlaybackInfo" {
			_, _ = fmt.Fprint(w, `{
				"MediaSources": [
					{
						"Id": "source-1",
						"SupportsDirectPlay": true,
						"MediaStreams": [
							{"Index": 0, "Type": "Video", "Codec": "h244", "DisplayTitle": "1080p H.264"},
							{"Index": 1, "Type": "Audio", "Codec": "aac", "Language": "eng", "DisplayTitle": "English AAC", "Channels": 2, "IsDefault": true},
							{"Index": 2, "Type": "Subtitle", "Codec": "srt", "Language": "eng", "DisplayTitle": "English SRT", "IsExternal": true}
						]
					}
				]
			}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, _ string, _ CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceStream, Kind: "video", Provider: ProviderJellyfin,
			// LibraryID was omitted here, which no real resolution ever is: it is
			// read from catalog_items, where a Jellyfin stream row always carries
			// one. Playback now refuses an item whose library is not allowed, so
			// the fixture has to be faithful about it.
			UpstreamID: "302", LibraryID: "lib-allowed", Active: true, Available: true,
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

	opts, err := getPlaybackOptions(t.Context(), "user-1", canonicalID, nil, nil)
	if err != nil {
		t.Fatalf("getPlaybackOptions error: %v", err)
	}
	if opts.ItemID != canonicalID {
		t.Fatalf("itemID got=%s want=%s", opts.ItemID, canonicalID)
	}
	if !opts.DirectPlay {
		t.Fatalf("directPlay got=false want=true")
	}
	if len(opts.AudioTracks) != 1 || opts.AudioTracks[0].Codec != "aac" {
		t.Fatalf("audioTracks=%+v", opts.AudioTracks)
	}
	if len(opts.Subtitles) != 1 || opts.Subtitles[0].Codec != "srt" || !opts.Subtitles[0].IsExternal {
		t.Fatalf("subtitles=%+v", opts.Subtitles)
	}
}

// Playback resolution used to swallow its error, so an unresolvable id produced
// a 200 carrying ItemID "" and StreamURL "/api/v1/stream/video/", and the raw
// caller-supplied id was passed straight to Jellyfin. That made this the one
// stream path able to reach a library outside JELLYFIN_LIBRARY_IDS.
func TestGetPlaybackOptionsRefusesUnresolvedAndDisallowedLibrary(t *testing.T) {
	// A server that answers PlaybackInfo for ANY item id is essential here. With
	// no server the buggy version also fails, but for the wrong reason — the
	// upstream call errors — so the test would pass without proving anything.
	// Answering every id means the unguarded code path succeeds and returns its
	// malformed payload, which is what these assertions actually catch.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/PlaybackInfo") {
			_, _ = fmt.Fprint(w, `{"MediaSources":[{"Id":"source-1","SupportsDirectPlay":true,
				"MediaStreams":[{"Index":0,"Type":"Video","Codec":"h264"}]}]}`)
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
		stubJellyfinResolver{"302": {MediaType: "Movie", AncestorIDs: []string{"lib-denied"}}},
		[]string{"lib-allowed"},
	)
	t.Cleanup(func() { jellyfinAuthorizer = oldAuthorizer })

	oldResolve := resolveCatalogIdentity
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })

	t.Run("unresolvable id is refused, not answered with an empty stream url", func(t *testing.T) {
		resolveCatalogIdentity = func(_ context.Context, _ string, _ CatalogSurface) (CatalogResolution, error) {
			return CatalogResolution{}, errCatalogInvalid
		}
		opts, err := getPlaybackOptions(t.Context(), "user-1", "not-a-known-id", nil, nil)
		if err == nil {
			t.Fatalf("want an error; got opts=%+v", opts)
		}
		if opts.StreamURL != "" || opts.ItemID != "" {
			t.Fatalf("a failed resolution must not yield a payload: %+v", opts)
		}
	})

	t.Run("library outside the allowlist is refused", func(t *testing.T) {
		resolveCatalogIdentity = func(_ context.Context, _ string, _ CatalogSurface) (CatalogResolution, error) {
			return CatalogResolution{
				ID: "00000000-0000-4000-8000-000000000302", Surface: SurfaceStream, Kind: "video",
				Provider: ProviderJellyfin, UpstreamID: "302", LibraryID: "lib-denied",
				Active: true, Available: true,
			}, nil
		}
		if _, err := getPlaybackOptions(t.Context(), "user-1", "00000000-0000-4000-8000-000000000302", nil, nil); err == nil {
			t.Fatal("playback returned options for a library outside JELLYFIN_LIBRARY_IDS")
		}
	})
}
