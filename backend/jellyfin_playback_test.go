package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
			UpstreamID: "302", Active: true, Available: true,
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
