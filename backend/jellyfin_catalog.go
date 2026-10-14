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
	// The source view is the item's library, and it is needed for both the
	// allowlist check and catalog identity, so carry it alongside the record.
	type latestItem struct {
		raw       jellyfinItemRaw
		libraryID string
	}
	var allItems []latestItem
	for _, v := range views {
		if strings.Contains(strings.ToLower(v.Name), "audiobook") {
			continue
		}
		if !jellyfinLibraryAllowed(v.ID) {
			continue
		}
		items, err := fetchJellyfinLatestItems(ctx, v.ID, limit)
		if err != nil {
			continue
		}
		for _, raw := range items {
			allItems = append(allItems, latestItem{raw: raw, libraryID: v.ID})
		}
	}
	sort.Slice(allItems, func(i, j int) bool {
		return allItems[i].raw.DateCreated > allItems[j].raw.DateCreated
	})
	if len(allItems) > limit {
		allItems = allItems[:limit]
	}
	// Latest queries run against every Jellyfin view, so the shelf has to be
	// filtered and canonicalized the same way the browse listing is. Without
	// this it returns upstream Jellyfin IDs, which every canonical route then
	// rejects — the card 404s on open and its share link would carry a
	// provider ID into chat — and it can surface libraries outside
	// JELLYFIN_LIBRARY_IDS.
	ids := make([]string, 0, len(allItems))
	seen := map[string]bool{}
	for _, entry := range allItems {
		if entry.raw.ID == "" || seen[entry.raw.ID] {
			continue
		}
		seen[entry.raw.ID] = true
		ids = append(ids, entry.raw.ID)
	}

	// A nil authorizer is the development allow-all mode, the same convention
	// the browse listing uses; the source view is then the library of record.
	authorized := make(map[string]AuthorizedMediaItem, len(allItems))
	if jellyfinAuthorizer == nil {
		for _, entry := range allItems {
			authorized[entry.raw.ID] = AuthorizedMediaItem{
				ID: entry.raw.ID, LibraryID: entry.libraryID,
				MediaType: entry.raw.Type, IsFolder: entry.raw.IsFolder,
			}
		}
	} else {
		var err error
		authorized, err = jellyfinAuthorizer.AuthorizeItems(ctx, ids)
		if err != nil {
			return nil, err
		}
	}

	resItems := make([]MediaItem, 0, len(allItems))
	emitted := map[string]bool{}
	for _, entry := range allItems {
		raw := entry.raw
		if emitted[raw.ID] {
			continue
		}
		authorizedItem, ok := authorized[raw.ID]
		if !ok || !jellyfinLibraryAllowed(authorizedItem.LibraryID) {
			continue
		}
		emitted[raw.ID] = true
		resolution, err := observeJellyfinCatalogItem(
			ctx, raw.ID, authorizedItem.LibraryID, raw.Type, raw.IsFolder,
		)
		if err != nil {
			continue
		}
		resItems = append(resItems, MediaItem{
			ID:           resolution.ID,
			Title:        raw.Name,
			Kind:         mediaKindFromJellyfin(raw.Type, raw.IsFolder),
			JellyfinType: raw.Type,
			IsFolder:     raw.IsFolder,
			ChildCount:   raw.ChildCount,
			DurationMS:   raw.RunTimeTicks / 10_000,
			CoverURL:     "/api/v1/media/items/" + resolution.ID + "/cover",
		})
	}
	return resItems, nil
}

func (c *JellyfinCatalog) Related(ctx context.Context, userID, rawID string, limit int) ([]MediaItem, error) {
	if limit <= 0 || limit > 12 {
		limit = 12
	}
	// Related was emitting raw upstream Jellyfin IDs, unfiltered — exactly the
	// defect the comment above Recent describes and that Recent already fixes.
	// Every canonical route rejects a provider ID, so each card 404'd on open,
	// its cover 404'd, sharing it pushed a provider ID into chat, and items from
	// libraries outside JELLYFIN_LIBRARY_IDS surfaced. Resolve the parent first
	// rather than falling back to the caller-supplied string.
	resolution, err := resolveCatalogIdentity(ctx, rawID, SurfaceStream)
	if err != nil {
		return nil, err
	}
	items, err := fetchJellyfinSimilarItems(ctx, resolution.UpstreamID, limit)
	if err != nil {
		return nil, err
	}

	// Similar items arrive without a library, so membership has to come from the
	// authorizer. A nil authorizer is the development allow-all mode used
	// elsewhere in this file; there the parent's library is the best available
	// answer, and similar items come from the same library in practice.
	authorized := make(map[string]AuthorizedMediaItem, len(items))
	if jellyfinAuthorizer == nil {
		for _, raw := range items {
			authorized[raw.ID] = AuthorizedMediaItem{
				ID: raw.ID, LibraryID: resolution.LibraryID,
				MediaType: raw.Type, IsFolder: raw.IsFolder,
			}
		}
	} else {
		ids := make([]string, 0, len(items))
		seen := map[string]bool{}
		for _, raw := range items {
			if raw.ID == "" || seen[raw.ID] {
				continue
			}
			seen[raw.ID] = true
			ids = append(ids, raw.ID)
		}
		authorized, err = jellyfinAuthorizer.AuthorizeItems(ctx, ids)
		if err != nil {
			return nil, err
		}
	}

	resItems := make([]MediaItem, 0, len(items))
	emitted := map[string]bool{}
	for _, raw := range items {
		if emitted[raw.ID] {
			continue
		}
		authorizedItem, ok := authorized[raw.ID]
		if !ok || !jellyfinLibraryAllowed(authorizedItem.LibraryID) {
			continue
		}
		emitted[raw.ID] = true
		// observeJellyfinCatalogItem re-checks the allowlist and yields the
		// canonical ID the rest of the API expects.
		itemResolution, err := observeJellyfinCatalogItem(
			ctx, raw.ID, authorizedItem.LibraryID, raw.Type, raw.IsFolder,
		)
		if err != nil {
			continue
		}
		resItems = append(resItems, MediaItem{
			ID:           itemResolution.ID,
			Title:        raw.Name,
			Kind:         mediaKindFromJellyfin(raw.Type, raw.IsFolder),
			JellyfinType: raw.Type,
			IsFolder:     raw.IsFolder,
			ChildCount:   raw.ChildCount,
			DurationMS:   raw.RunTimeTicks / 10_000,
			CoverURL:     "/api/v1/media/items/" + itemResolution.ID + "/cover",
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
