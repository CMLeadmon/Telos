package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type PlaybackAudioTrack struct {
	Index     int    `json:"index"`
	Title     string `json:"title"`
	Language  string `json:"language"`
	Codec     string `json:"codec"`
	Channels  int    `json:"channels"`
	IsDefault bool   `json:"isDefault"`
}

type PlaybackSubtitleTrack struct {
	Index       int    `json:"index"`
	Title       string `json:"title"`
	Language    string `json:"language"`
	Codec       string `json:"codec"`
	IsExternal  bool   `json:"isExternal"`
	IsDefault   bool   `json:"isDefault"`
	DeliveryURL string `json:"deliveryUrl,omitempty"`
}

type PlaybackOptions struct {
	ItemID      string                  `json:"itemId"`
	AudioTracks []PlaybackAudioTrack    `json:"audioTracks"`
	Subtitles   []PlaybackSubtitleTrack `json:"subtitles"`
	DirectPlay  bool                    `json:"directPlay"`
	StreamURL   string                  `json:"streamUrl"`
}

func getPlaybackOptions(ctx context.Context, userID, rawID string, audioIndex, subtitleIndex *int) (PlaybackOptions, error) {
	resolution, err := resolveCatalogIdentity(ctx, rawID, SurfaceStream)
	upstreamID := rawID
	if err == nil {
		upstreamID = resolution.UpstreamID
	}
	tok, _, err := getJellyfinAuthToken(ctx)
	if err != nil {
		return PlaybackOptions{}, err
	}
	u := fmt.Sprintf("/Items/%s/PlaybackInfo?UserId=%s", url.PathEscape(upstreamID), userID)
	resp, err := jellyfinGET(ctx, u, tok)
	if err != nil {
		return PlaybackOptions{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return PlaybackOptions{}, fmt.Errorf("jellyfin playback info status %d", resp.StatusCode)
	}

	var pbRaw struct {
		MediaSources []struct {
			ID                 string `json:"Id"`
			SupportsDirectPlay bool   `json:"SupportsDirectPlay"`
			MediaStreams       []struct {
				Index        int    `json:"Index"`
				Type         string `json:"Type"`
				Codec        string `json:"Codec"`
				Language     string `json:"Language"`
				DisplayTitle string `json:"DisplayTitle"`
				Channels     int    `json:"Channels"`
				IsDefault    bool   `json:"IsDefault"`
				IsExternal   bool   `json:"IsExternal"`
			} `json:"MediaStreams"`
		} `json:"MediaSources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pbRaw); err != nil {
		return PlaybackOptions{}, err
	}

	audioTracks := []PlaybackAudioTrack{}
	subtitles := []PlaybackSubtitleTrack{}
	directPlay := false

	if len(pbRaw.MediaSources) > 0 {
		ms := pbRaw.MediaSources[0]
		directPlay = ms.SupportsDirectPlay
		for _, s := range ms.MediaStreams {
			switch strings.ToLower(s.Type) {
			case "audio":
				audioTracks = append(audioTracks, PlaybackAudioTrack{
					Index:     s.Index,
					Title:     s.DisplayTitle,
					Language:  s.Language,
					Codec:     s.Codec,
					Channels:  s.Channels,
					IsDefault: s.IsDefault,
				})
			case "subtitle":
				sub := PlaybackSubtitleTrack{
					Index:      s.Index,
					Title:      s.DisplayTitle,
					Language:   s.Language,
					Codec:      s.Codec,
					IsExternal: s.IsExternal,
					IsDefault:  s.IsDefault,
				}
				if s.IsExternal {
					sub.DeliveryURL = fmt.Sprintf("/api/v1/stream/video/%s/Subtitles/%d/Stream", resolution.ID, s.Index)
				}
				subtitles = append(subtitles, sub)
			}
		}
	}

	streamURL := fmt.Sprintf("/api/v1/stream/video/%s", resolution.ID)

	return PlaybackOptions{
		ItemID:      resolution.ID,
		AudioTracks: audioTracks,
		Subtitles:   subtitles,
		DirectPlay:  directPlay,
		StreamURL:   streamURL,
	}, nil
}
