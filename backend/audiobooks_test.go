package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchGrimmoryAudiobookInfo(t *testing.T) {
	const canonicalID = "00000000-0000-4000-8000-000000000201"

	cleanup := setupManagementGrimmory(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/audiobooks/201/info" {
			_, _ = fmt.Fprint(w, `{
				"title": "Vaporwave Nights",
				"author": "Synth Author",
				"narrator": "Vocal Artist",
				"durationMs": 7200000,
				"codec": "mp3",
				"tracks": [
					{"index": 0, "title": "Track 1", "durationMs": 3600000, "cumulativeStartMs": 0},
					{"index": 1, "title": "Track 2", "durationMs": 3600000, "cumulativeStartMs": 3600000}
				],
				"chapters": [
					{"index": 0, "title": "Chapter 1", "startTimeMs": 0, "endTimeMs": 3600000, "durationMs": 3600000}
				]
			}`)
			return
		}
		http.NotFound(w, r)
	})
	defer cleanup()

	oldResolve := resolveCatalogIdentity
	resolveCatalogIdentity = func(_ context.Context, _ string, _ CatalogSurface) (CatalogResolution, error) {
		return CatalogResolution{
			ID: canonicalID, Surface: SurfaceLibrary, Kind: "audiobook", Provider: ProviderGrimmory,
			UpstreamID: "201", Active: true, Available: true,
		}, nil
	}
	t.Cleanup(func() { resolveCatalogIdentity = oldResolve })

	oldRepo := catalogRepo
	catalogRepo = &CatalogRepository{}
	t.Cleanup(func() { catalogRepo = oldRepo })

	info, res, err := fetchGrimmoryAudiobookInfo(t.Context(), "user-1", canonicalID)
	if err != nil {
		t.Fatalf("fetchGrimmoryAudiobookInfo error: %v", err)
	}
	if res.ID != canonicalID || info.ID != canonicalID || info.Title != "Vaporwave Nights" || info.Codec != "mp3" {
		t.Fatalf("info=%+v res=%+v", info, res)
	}
	if len(info.Tracks) != 2 || len(info.Chapters) != 1 {
		t.Fatalf("tracks=%d chapters=%d", len(info.Tracks), len(info.Chapters))
	}
}

func TestHandleAudiobookStreamRejectsInvalidTrackIndex(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/library/audiobooks/123/tracks/-1/stream", nil)
	req.SetPathValue("id", "123")
	req.SetPathValue("index", "-1")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &UserContext{ID: "user-1"}))

	rec := httptest.NewRecorder()
	handleAudiobookTrackStream(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got status %d", rec.Code)
	}
}
