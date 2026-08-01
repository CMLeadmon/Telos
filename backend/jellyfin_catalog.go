package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type MediaLibrary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	CollectionType string `json:"collectionType"`
}

type MediaItem struct {
	ID           string      `json:"id"`
	Title        string      `json:"title"`
	Kind         CatalogKind `json:"kind"`
	JellyfinType string      `json:"type"`
	IsFolder     bool        `json:"isFolder"`
	ChildCount   int         `json:"childCount,omitempty"`
	DurationMS   int64       `json:"durationMs,omitempty"`
	CoverURL     string      `json:"coverUrl"`
}

type MediaChapter struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	StartMS int64  `json:"startMs"`
}

type MediaDetail struct {
	MediaItem
	Overview       string         `json:"overview"`
	ProductionYear int            `json:"year,omitempty"`
	Genres         []string       `json:"genres"`
	SeriesName     string         `json:"seriesName,omitempty"`
	SeasonName     string         `json:"seasonName,omitempty"`
	EpisodeNumber  *int           `json:"episodeNumber,omitempty"`
	Studios        []string       `json:"studios"`
	Chapters       []MediaChapter `json:"chapters"`
	Progress       MemberProgress `json:"progress"`
}

type jellyfinItemRaw struct {
	ID             string                  `json:"Id"`
	Name           string                  `json:"Name"`
	Type           string                  `json:"Type"`
	IsFolder       bool                    `json:"IsFolder"`
	CollectionType string                  `json:"CollectionType"`
	ChildCount     int                     `json:"ChildCount"`
	RunTimeTicks   int64                   `json:"RunTimeTicks"`
	Overview       string                  `json:"Overview"`
	ProductionYear int                     `json:"ProductionYear"`
	Genres         []string                `json:"Genres"`
	SeriesName     string                  `json:"SeriesName"`
	SeasonName     string                  `json:"SeasonName"`
	IndexNumber    *int                    `json:"IndexNumber"`
	Studios        []struct{ Name string } `json:"Studios"`
	Chapters       []struct {
		Name               string `json:"Name"`
		StartPositionTicks int64  `json:"StartPositionTicks"`
	} `json:"Chapters"`
	DateCreated string `json:"DateCreated"`
}

var getJellyfinBaseURL = func() string {
	return jellyfinBaseURL
}

var getJellyfinAuthToken = func(ctx context.Context) (string, string, error) {
	tok := getJellyfinAdminToken()
	if tok == "" {
		return "", "", errors.New("Jellyfin admin token missing")
	}
	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		return "", "", err
	}
	return tok, userID, nil
}

func jellyfinGET(ctx context.Context, path string, token string) (*http.Response, error) {
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	fullURL := getJellyfinBaseURL() + path
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("X-Emby-Token", token)
	}
	return upstreamHTTPClient.Do(req)
}

func getJellyfinUserViews(ctx context.Context) ([]MediaLibrary, error) {
	tok, userID, err := getJellyfinAuthToken(ctx)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("/Users/%s/Views", userID)
	resp, err := jellyfinGET(ctx, u, tok)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jellyfin views status %d", resp.StatusCode)
	}
	var res struct {
		Items []struct {
			ID             string `json:"Id"`
			Name           string `json:"Name"`
			Type           string `json:"Type"`
			CollectionType string `json:"CollectionType"`
		} `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	libs := make([]MediaLibrary, 0, len(res.Items))
	for _, item := range res.Items {
		libs = append(libs, MediaLibrary{
			ID:             item.ID,
			Name:           item.Name,
			Type:           item.Type,
			CollectionType: item.CollectionType,
		})
	}
	return libs, nil
}

type JellyfinCatalog struct {
	repo *CatalogRepository
}

func currentJellyfinCatalog() *JellyfinCatalog {
	return &JellyfinCatalog{repo: catalogRepo}
}

func mediaKindFromJellyfin(itemType string, isFolder bool) CatalogKind {
	if isFolder {
		return "folder"
	}
	switch strings.ToLower(itemType) {
	case "movie", "video", "episode":
		return "video"
	case "audio", "song", "track":
		return "audio"
	default:
		return "media"
	}
}

func (c *JellyfinCatalog) Libraries(ctx context.Context) ([]MediaLibrary, error) {
	views, err := getJellyfinUserViews(ctx)
	if err != nil {
		return nil, err
	}
	libs := make([]MediaLibrary, 0, len(views))
	for _, v := range views {
		libs = append(libs, MediaLibrary{
			ID:             v.ID,
			Name:           v.Name,
			Type:           v.Type,
			CollectionType: v.CollectionType,
		})
	}
	return libs, nil
}

func (c *JellyfinCatalog) Children(ctx context.Context, parentID string) ([]MediaItem, error) {
	items, err := fetchJellyfinFolderChildren(ctx, parentID)
	if err != nil {
		return nil, err
	}
	resItems := make([]MediaItem, 0, len(items))
	for _, raw := range items {
		kind := mediaKindFromJellyfin(raw.Type, raw.IsFolder)
		durMs := raw.RunTimeTicks / 10_000
		resItems = append(resItems, MediaItem{
			ID:           raw.ID,
			Title:        raw.Name,
			Kind:         kind,
			JellyfinType: raw.Type,
			IsFolder:     raw.IsFolder,
			ChildCount:   raw.ChildCount,
			DurationMS:   durMs,
			CoverURL:     "/api/v1/media/items/" + raw.ID + "/cover",
		})
	}
	return resItems, nil
}

func (c *JellyfinCatalog) Detail(ctx context.Context, userID, rawID string) (MediaDetail, CatalogResolution, error) {
	resolution, err := resolveCatalogIdentity(ctx, rawID, SurfaceStream)
	if err != nil {
		resolution = CatalogResolution{
			ID: rawID, Surface: SurfaceStream, Kind: "media", Provider: ProviderJellyfin,
			UpstreamID: rawID, Active: true, Available: true,
		}
	}
	item, err := fetchJellyfinRichDetail(ctx, resolution.UpstreamID)
	if err != nil {
		return MediaDetail{}, resolution, err
	}
	kind := mediaKindFromJellyfin(item.Type, item.IsFolder)
	durMs := item.RunTimeTicks / 10_000

	genres := item.Genres
	if genres == nil {
		genres = []string{}
	}
	studios := make([]string, 0, len(item.Studios))
	for _, s := range item.Studios {
		if s.Name != "" {
			studios = append(studios, s.Name)
		}
	}
	chapters := make([]MediaChapter, 0, len(item.Chapters))
	for idx, ch := range item.Chapters {
		chapters = append(chapters, MediaChapter{
			Index:   idx,
			Title:   ch.Name,
			StartMS: ch.StartPositionTicks / 10_000,
		})
	}

	detail := MediaDetail{
		MediaItem: MediaItem{
			ID:           resolution.ID,
			Title:        item.Name,
			Kind:         kind,
			JellyfinType: item.Type,
			IsFolder:     item.IsFolder,
			ChildCount:   item.ChildCount,
			DurationMS:   durMs,
			CoverURL:     "/api/v1/media/items/" + resolution.ID + "/cover",
		},
		Overview:       item.Overview,
		ProductionYear: item.ProductionYear,
		Genres:         genres,
		SeriesName:     item.SeriesName,
		SeasonName:     item.SeasonName,
		EpisodeNumber:  item.IndexNumber,
		Studios:        studios,
		Chapters:       chapters,
		Progress:       MemberProgress{ProgressInput: ProgressInput{Locator: json.RawMessage(`{}`)}},
	}

	if continuityRepo != nil {
		pMap, err := continuityRepo.GetMany(ctx, userID, []string{resolution.ID})
		if err == nil {
			if p, ok := pMap[resolution.ID]; ok {
				detail.Progress = p
			}
		}
	}

	return detail, resolution, nil
}

func (c *JellyfinCatalog) Continue(ctx context.Context, userID string, limit int) ([]MediaDetail, error) {
	if continuityRepo == nil {
		return []MediaDetail{}, nil
	}
	itemIDs, err := continuityRepo.Continue(ctx, userID, SurfaceStream, limit)
	if err != nil {
		return nil, err
	}
	details := make([]MediaDetail, 0, len(itemIDs))
	for _, id := range itemIDs {
		detail, _, err := c.Detail(ctx, userID, id)
		if err != nil {
			continue
		}
		details = append(details, detail)
	}
	return details, nil
}

func (c *JellyfinCatalog) Recent(ctx context.Context, userID string, limit int) ([]MediaItem, error) {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	views, err := getJellyfinUserViews(ctx)
	if err != nil {
		return nil, err
	}
	var allItems []jellyfinItemRaw
	for _, v := range views {
		if strings.Contains(strings.ToLower(v.Name), "audiobook") {
			continue
		}
		items, err := fetchJellyfinLatestItems(ctx, v.ID, limit)
		if err != nil {
			continue
		}
		allItems = append(allItems, items...)
	}
	sort.Slice(allItems, func(i, j int) bool {
		return allItems[i].DateCreated > allItems[j].DateCreated
	})
	if len(allItems) > limit {
		allItems = allItems[:limit]
	}
	resItems := make([]MediaItem, 0, len(allItems))
	seen := map[string]bool{}
	for _, raw := range allItems {
		if seen[raw.ID] {
			continue
		}
		seen[raw.ID] = true
		kind := mediaKindFromJellyfin(raw.Type, raw.IsFolder)
		durMs := raw.RunTimeTicks / 10_000
		resItems = append(resItems, MediaItem{
			ID:           raw.ID,
			Title:        raw.Name,
			Kind:         kind,
			JellyfinType: raw.Type,
			IsFolder:     raw.IsFolder,
			ChildCount:   raw.ChildCount,
			DurationMS:   durMs,
			CoverURL:     "/api/v1/media/items/" + raw.ID + "/cover",
		})
	}
	return resItems, nil
}

func (c *JellyfinCatalog) Related(ctx context.Context, userID, rawID string, limit int) ([]MediaItem, error) {
	if limit <= 0 || limit > 12 {
		limit = 12
	}
	resolution, err := resolveCatalogIdentity(ctx, rawID, SurfaceStream)
	upstreamID := rawID
	if err == nil {
		upstreamID = resolution.UpstreamID
	}
	items, err := fetchJellyfinSimilarItems(ctx, upstreamID, limit)
	if err != nil {
		return nil, err
	}
	resItems := make([]MediaItem, 0, len(items))
	for _, raw := range items {
		kind := mediaKindFromJellyfin(raw.Type, raw.IsFolder)
		durMs := raw.RunTimeTicks / 10_000
		resItems = append(resItems, MediaItem{
			ID:           raw.ID,
			Title:        raw.Name,
			Kind:         kind,
			JellyfinType: raw.Type,
			IsFolder:     raw.IsFolder,
			ChildCount:   raw.ChildCount,
			DurationMS:   durMs,
			CoverURL:     "/api/v1/media/items/" + raw.ID + "/cover",
		})
	}
	return resItems, nil
}

func fetchJellyfinFolderChildren(ctx context.Context, parentID string) ([]jellyfinItemRaw, error) {
	tok, userID, err := getJellyfinAuthToken(ctx)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("/Users/%s/Items?ParentId=%s&SortBy=SortName", userID, url.QueryEscape(parentID))
	resp, err := jellyfinGET(ctx, u, tok)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jellyfin children status %d", resp.StatusCode)
	}
	var res struct {
		Items []jellyfinItemRaw `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res.Items, nil
}

func fetchJellyfinRichDetail(ctx context.Context, itemID string) (jellyfinItemRaw, error) {
	tok, userID, err := getJellyfinAuthToken(ctx)
	if err != nil {
		return jellyfinItemRaw{}, err
	}
	u := fmt.Sprintf("/Users/%s/Items/%s?Fields=Overview,Genres,ProductionYear,Studios,SeriesName,SeasonName,IndexNumber,RunTimeTicks,ChildCount,Chapters", userID, url.PathEscape(itemID))
	resp, err := jellyfinGET(ctx, u, tok)
	if err != nil {
		return jellyfinItemRaw{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return jellyfinItemRaw{}, fmt.Errorf("jellyfin item detail status %d", resp.StatusCode)
	}
	var item jellyfinItemRaw
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return jellyfinItemRaw{}, err
	}
	return item, nil
}

func fetchJellyfinLatestItems(ctx context.Context, parentID string, limit int) ([]jellyfinItemRaw, error) {
	tok, userID, err := getJellyfinAuthToken(ctx)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("/Users/%s/Items/Latest?ParentId=%s&Limit=%d", userID, url.QueryEscape(parentID), limit)
	resp, err := jellyfinGET(ctx, u, tok)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jellyfin latest status %d", resp.StatusCode)
	}
	var items []jellyfinItemRaw
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func fetchJellyfinSimilarItems(ctx context.Context, itemID string, limit int) ([]jellyfinItemRaw, error) {
	tok, userID, err := getJellyfinAuthToken(ctx)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("/Items/%s/Similar?UserId=%s&Limit=%d", url.PathEscape(itemID), userID, limit)
	resp, err := jellyfinGET(ctx, u, tok)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jellyfin similar status %d", resp.StatusCode)
	}
	var res struct {
		Items []jellyfinItemRaw `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res.Items, nil
}
