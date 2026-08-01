package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

type AudiobookTrack struct {
	Index             int    `json:"index"`
	Title             string `json:"title"`
	DurationMS        int64  `json:"durationMs"`
	CumulativeStartMS int64  `json:"cumulativeStartMs"`
}

type AudiobookChapter struct {
	Index       int    `json:"index"`
	Title       string `json:"title"`
	StartTimeMS int64  `json:"startTimeMs"`
	EndTimeMS   int64  `json:"endTimeMs"`
	DurationMS  int64  `json:"durationMs"`
}

type AudiobookInfo struct {
	ID         string             `json:"id"`
	Title      string             `json:"title"`
	Author     string             `json:"author"`
	Narrator   string             `json:"narrator"`
	DurationMS int64              `json:"durationMs"`
	Codec      string             `json:"codec"`
	Tracks     []AudiobookTrack   `json:"tracks"`
	Chapters   []AudiobookChapter `json:"chapters"`
	Progress   MemberProgress     `json:"progress"`
}

type grimmoryAudiobookInfoDTO struct {
	Title      string             `json:"title"`
	Author     string             `json:"author"`
	Narrator   string             `json:"narrator"`
	DurationMS int64              `json:"durationMs"`
	Codec      string             `json:"codec"`
	Tracks     []AudiobookTrack   `json:"tracks"`
	Chapters   []AudiobookChapter `json:"chapters"`
}

func fetchGrimmoryAudiobookInfo(ctx context.Context, userID, rawID string) (AudiobookInfo, CatalogResolution, error) {
	resolution, err := resolveCatalogIdentity(ctx, rawID, SurfaceLibrary)
	if err != nil {
		return AudiobookInfo{}, CatalogResolution{}, errCatalogNotFound
	}
	if resolution.Kind != "audiobook" || !resolution.Active || !resolution.Available {
		return AudiobookInfo{}, CatalogResolution{}, errCatalogNotFound
	}

	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()

	resp, err := grimmoryGET(requestCtx, fmt.Sprintf("/api/v1/audiobooks/%s/info", resolution.UpstreamID))
	if err != nil {
		return AudiobookInfo{}, CatalogResolution{}, libraryProviderError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return AudiobookInfo{}, CatalogResolution{}, libraryProviderError(fmt.Errorf("upstream status %d", resp.StatusCode))
	}

	var dto grimmoryAudiobookInfoDTO
	if err := json.NewDecoder(resp.Body).Decode(&dto); err != nil {
		return AudiobookInfo{}, CatalogResolution{}, libraryCatalogError(err)
	}

	tracks := dto.Tracks
	if tracks == nil {
		tracks = []AudiobookTrack{}
	}
	chapters := dto.Chapters
	if chapters == nil {
		chapters = []AudiobookChapter{}
	}

	info := AudiobookInfo{
		ID:         resolution.ID,
		Title:      dto.Title,
		Author:     dto.Author,
		Narrator:   dto.Narrator,
		DurationMS: dto.DurationMS,
		Codec:      dto.Codec,
		Tracks:     tracks,
		Chapters:   chapters,
		Progress:   MemberProgress{ProgressInput: ProgressInput{Locator: json.RawMessage(`{}`)}},
	}

	if continuityRepo != nil {
		pMap, err := continuityRepo.GetMany(ctx, userID, []string{resolution.ID})
		if err == nil {
			if p, ok := pMap[resolution.ID]; ok {
				info.Progress = p
			}
		}
	}

	return info, resolution, nil
}

func handleAudiobookInfo(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil || user.ID == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_id", "Audiobook ID required.")
		return
	}

	info, _, err := fetchGrimmoryAudiobookInfo(r.Context(), user.ID, id)
	if err != nil {
		writeLibraryMemberError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func handleAudiobookStream(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil || user.ID == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_id", "Audiobook ID required.")
		return
	}

	resolution, err := resolveCatalogIdentity(r.Context(), id, SurfaceLibrary)
	if err != nil || resolution.Kind != "audiobook" || !resolution.Active || !resolution.Available {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}

	if releaseSlot, acquired := acquireStreamSlot(w, r); acquired {
		defer releaseSlot()
	} else {
		return
	}

	upstreamPath := fmt.Sprintf("/api/v1/audiobooks/%s/stream", resolution.UpstreamID)
	proxyGrimmoryBinary(w, r, upstreamPath, "")
}

func handleAudiobookTrackStream(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil || user.ID == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "Unauthorized.")
		return
	}
	id := r.PathValue("id")
	trackIdxStr := r.PathValue("index")
	if id == "" || trackIdxStr == "" {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_id", "Audiobook ID and track index required.")
		return
	}

	idx, err := strconv.Atoi(trackIdxStr)
	if err != nil || idx < 0 {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_track", "Invalid track index.")
		return
	}

	resolution, err := resolveCatalogIdentity(r.Context(), id, SurfaceLibrary)
	if err != nil || resolution.Kind != "audiobook" || !resolution.Active || !resolution.Available {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}

	if releaseSlot, acquired := acquireStreamSlot(w, r); acquired {
		defer releaseSlot()
	} else {
		return
	}

	upstreamPath := fmt.Sprintf("/api/v1/audiobooks/%s/track/%d/stream", resolution.UpstreamID, idx)
	proxyGrimmoryBinary(w, r, upstreamPath, "")
}
