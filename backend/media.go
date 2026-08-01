package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// AuthorizedMediaItem is a Jellyfin item confirmed to belong to a configured
// Telos library.
type AuthorizedMediaItem struct {
	ID        string
	LibraryID string
	Name      string
	MediaType string
	IsFolder  bool
}

// JellyfinItemResolver resolves an item's identity and its ancestor library
// IDs. The production implementation queries Jellyfin; tests fake it.
type JellyfinItemResolver interface {
	ResolveItems(ctx context.Context, itemIDs []string) (map[string]resolvedItem, error)
}

type resolvedItem struct {
	Name        string
	MediaType   string
	IsFolder    bool
	AncestorIDs []string
}

// JellyfinAuthorizer verifies every item belongs to a configured stable
// library, with short positive/negative membership caches.
type JellyfinAuthorizer struct {
	resolver JellyfinItemResolver
	allowed  map[string]struct{}

	mu       sync.Mutex
	posCache map[string]cachedAuth // itemID -> authorized
	negCache map[string]time.Time  // itemID -> denied-until
}

type cachedAuth struct {
	item      AuthorizedMediaItem
	expiresAt time.Time
}

const (
	authPosTTL   = 30 * time.Second
	authNegTTL   = 5 * time.Second
	maxAuthBatch = 50
)

var errItemNotAuthorized = errors.New("media item not authorized")

// NewJellyfinAuthorizer requires a nonempty allowlist of stable library IDs.
func NewJellyfinAuthorizer(resolver JellyfinItemResolver, allowedLibraryIDs []string) (*JellyfinAuthorizer, error) {
	if len(allowedLibraryIDs) == 0 {
		return nil, errors.New("JELLYFIN_LIBRARY_IDS must be a nonempty list of stable library IDs")
	}
	allowed := map[string]struct{}{}
	for _, id := range allowedLibraryIDs {
		if id != "" {
			allowed[id] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return nil, errors.New("JELLYFIN_LIBRARY_IDS contains no valid IDs")
	}
	return &JellyfinAuthorizer{
		resolver: resolver, allowed: allowed,
		posCache: map[string]cachedAuth{}, negCache: map[string]time.Time{},
	}, nil
}

// AuthorizeItem authorizes one item, returning an indistinguishable not-found
// error for out-of-scope IDs.
func (a *JellyfinAuthorizer) AuthorizeItem(ctx context.Context, itemID string) (AuthorizedMediaItem, error) {
	m, err := a.AuthorizeItems(ctx, []string{itemID})
	if err != nil {
		return AuthorizedMediaItem{}, err
	}
	item, ok := m[itemID]
	if !ok {
		return AuthorizedMediaItem{}, errItemNotAuthorized
	}
	return item, nil
}

// AuthorizeItems authorizes a batch in one bounded upstream call (deduped, max
// 50), consulting the caches first. Unauthorized IDs are omitted from the map.
func (a *JellyfinAuthorizer) AuthorizeItems(ctx context.Context, itemIDs []string) (map[string]AuthorizedMediaItem, error) {
	now := time.Now()
	result := map[string]AuthorizedMediaItem{}
	var toResolve []string
	seen := map[string]struct{}{}

	a.mu.Lock()
	for _, id := range itemIDs {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if c, ok := a.posCache[id]; ok && c.expiresAt.After(now) {
			result[id] = c.item
			continue
		}
		if until, ok := a.negCache[id]; ok && until.After(now) {
			continue // still denied
		}
		toResolve = append(toResolve, id)
	}
	a.mu.Unlock()

	if len(toResolve) > maxAuthBatch {
		toResolve = toResolve[:maxAuthBatch]
	}
	if len(toResolve) == 0 {
		return result, nil
	}

	resolved, err := a.resolver.ResolveItems(ctx, toResolve)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range toResolve {
		ri, ok := resolved[id]
		authorized := false
		if ok {
			for _, anc := range ri.AncestorIDs {
				if _, allowed := a.allowed[anc]; allowed {
					authorized = true
					item := AuthorizedMediaItem{ID: id, LibraryID: anc, Name: ri.Name, MediaType: ri.MediaType, IsFolder: ri.IsFolder}
					a.posCache[id] = cachedAuth{item: item, expiresAt: now.Add(authPosTTL)}
					result[id] = item
					break
				}
			}
		}
		if !authorized {
			a.negCache[id] = now.Add(authNegTTL)
		}
	}
	return result, nil
}

// Invalidate clears the caches (on upstream refresh, mutation, or config change).
func (a *JellyfinAuthorizer) Invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.posCache = map[string]cachedAuth{}
	a.negCache = map[string]time.Time{}
}

// jellyfinAPIResolver resolves item ancestry via the Jellyfin Items API,
// injecting the admin token server-side.
type jellyfinAPIResolver struct{}

func (jellyfinAPIResolver) ResolveItems(ctx context.Context, itemIDs []string) (map[string]resolvedItem, error) {
	out := map[string]resolvedItem{}
	token := getJellyfinAdminToken()
	for _, id := range itemIDs {
		if !validJellyfinID(id) {
			continue
		}
		reqCtx, cancel := upstreamRequestContext(ctx)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
			fmt.Sprintf("%s/Items/%s/Ancestors", jellyfinBaseURL, id), nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("X-Emby-Token", token)
		resp, err := upstreamHTTPClient.Do(req)
		if err != nil {
			cancel()
			continue
		}
		var ancestors []struct {
			ID   string `json:"Id"`
			Name string `json:"Name"`
			Type string `json:"Type"`
		}
		json.NewDecoder(resp.Body).Decode(&ancestors)
		resp.Body.Close()
		cancel()
		ri := resolvedItem{}
		for _, anc := range ancestors {
			ri.AncestorIDs = append(ri.AncestorIDs, anc.ID)
		}
		out[id] = ri
	}
	return out, nil
}

var jellyfinAuthorizer *JellyfinAuthorizer

func jellyfinLibraryAllowed(libraryID string) bool {
	if libraryID == "" {
		return false
	}
	if jellyfinAuthorizer == nil {
		return true
	}
	_, ok := jellyfinAuthorizer.allowed[libraryID]
	return ok
}

func jellyfinCatalogKind(mediaType string, isFolder bool) (CatalogKind, bool) {
	if isFolder {
		return "folder", true
	}
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "audio", "audiobook", "music", "musicalbum", "audioepisode":
		return "audio", true
	case "video", "movie", "episode", "trailer", "musicvideo":
		return "video", true
	default:
		return "", false
	}
}

func observeJellyfinCatalogItem(ctx context.Context, upstreamID, libraryID, mediaType string, isFolder bool) (CatalogResolution, error) {
	if !jellyfinLibraryAllowed(libraryID) {
		return CatalogResolution{}, errItemNotAuthorized
	}
	kind, ok := jellyfinCatalogKind(mediaType, isFolder)
	if !ok {
		return CatalogResolution{}, errCatalogInvalid
	}
	observed, err := observeCatalogIdentity(ctx, CatalogObservation{
		Provider: ProviderJellyfin, UpstreamID: upstreamID, LibraryID: libraryID,
		Surface: SurfaceStream, Kind: kind,
	})
	if err != nil {
		return CatalogResolution{}, err
	}
	current, err := resolveCatalogIdentity(ctx, observed.ID, SurfaceStream)
	if err != nil || current.Provider != ProviderJellyfin || current.UpstreamID != upstreamID ||
		!current.Active || !current.Available {
		return CatalogResolution{}, errItemNotAuthorized
	}
	return current, nil
}

type jellyfinItemDetail struct {
	ID       string `json:"Id"`
	Name     string `json:"Name"`
	Type     string `json:"Type"`
	IsFolder bool   `json:"IsFolder"`
}

func fetchJellyfinItemDetail(ctx context.Context, itemID string) (jellyfinItemDetail, error) {
	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		return jellyfinItemDetail{}, err
	}
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet,
		fmt.Sprintf("%s/Users/%s/Items/%s", jellyfinBaseURL, userID, itemID), nil)
	if err != nil {
		return jellyfinItemDetail{}, err
	}
	token := getJellyfinAdminToken()
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))
	resp, err := upstreamHTTPClient.Do(req)
	if err != nil {
		return jellyfinItemDetail{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return jellyfinItemDetail{}, errItemNotAuthorized
	}
	var item jellyfinItemDetail
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&item); err != nil {
		return jellyfinItemDetail{}, err
	}
	if item.ID != itemID {
		return jellyfinItemDetail{}, errItemNotAuthorized
	}
	return item, nil
}

func discoverLegacyJellyfinItem(ctx context.Context, upstreamID string) (CatalogResolution, error) {
	if jellyfinAuthorizer == nil {
		return CatalogResolution{}, errItemNotAuthorized
	}
	libraryID := ""
	if _, topLevel := jellyfinAuthorizer.allowed[upstreamID]; topLevel {
		libraryID = upstreamID
	} else {
		authorized, err := jellyfinAuthorizer.AuthorizeItem(ctx, upstreamID)
		if err != nil {
			return CatalogResolution{}, errItemNotAuthorized
		}
		libraryID = authorized.LibraryID
	}
	item, err := fetchJellyfinItemDetail(ctx, upstreamID)
	if err != nil {
		return CatalogResolution{}, errItemNotAuthorized
	}
	return observeJellyfinCatalogItem(ctx, upstreamID, libraryID, item.Type, item.IsFolder)
}

// resolveJellyfinItemHTTP canonicalizes a browser ID, checks the source is
// still active and in the configured library allowlist, then authorizes the
// upstream Jellyfin ID. That ordering prevents a browser UUID from ever
// reaching Jellyfin or its authorizer.
func resolveJellyfinItemHTTP(w http.ResponseWriter, r *http.Request, rawID string) (CatalogResolution, bool) {
	resolution, err := resolveCatalogIdentity(r.Context(), rawID, SurfaceStream)
	if errors.Is(err, errCatalogNotFound) && !looksLikeUUID(rawID) {
		resolution, err = discoverLegacyJellyfinItem(r.Context(), rawID)
	}
	if err != nil || resolution.Provider != ProviderJellyfin || !resolution.Active || !resolution.Available ||
		!jellyfinLibraryAllowed(resolution.LibraryID) {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return CatalogResolution{}, false
	}
	// A top-level view is itself the allowlisted library and has no ancestor
	// carrying that ID; descendants still require the full ancestry check.
	if resolution.UpstreamID != resolution.LibraryID && !authorizeJellyfinItem(w, r, resolution.UpstreamID) {
		return CatalogResolution{}, false
	}
	return resolution, true
}

// authorizeJellyfinItem is the handler-facing gate: it returns true when the
// item is in a configured library, writing a 404 otherwise. When no authorizer
// is configured (development), it allows access.
func authorizeJellyfinItem(w http.ResponseWriter, r *http.Request, itemID string) bool {
	if jellyfinAuthorizer == nil {
		return true
	}
	if _, err := jellyfinAuthorizer.AuthorizeItem(r.Context(), itemID); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return false
	}
	return true
}

// jellyfinStreamQueryKeys is the allowlist of upstream query parameters the
// HLS video-subpath route may carry to Jellyfin. The browser query is never
// forwarded wholesale; only these named keys pass, and the api_key/token is
// always injected server-side, never accepted from the browser.
var jellyfinStreamQueryKeys = map[string]struct{}{
	"PlaySessionId":               {},
	"MediaSourceId":               {},
	"static":                      {},
	"DeviceId":                    {},
	"Tag":                         {},
	"SegmentContainer":            {},
	"MinSegments":                 {},
	"BreakOnNonKeyFrames":         {},
	"VideoCodec":                  {},
	"AudioCodec":                  {},
	"VideoBitrate":                {},
	"AudioBitrate":                {},
	"MaxWidth":                    {},
	"MaxHeight":                   {},
	"MaxStreamingBitrate":         {},
	"SubtitleMethod":              {},
	"TranscodingMaxAudioChannels": {},
	"RequireAvc":                  {},
	"runtimeTicks":                {},
	"actualSegmentLengthTicks":    {},
	"TranscodeReasons":            {},
	"h264-profile":                {},
	"h264-level":                  {},
}

// proxyRequest relays a validated Jellyfin binary request (cover, audio,
// video/HLS) across the isolation boundary. It builds a fresh upstream request
// with a server-injected admin token, a per-route query allowlist (queryAllow;
// nil means no query), range support, and the strict media response-header
// allowlist. The browser's cookies, Authorization header, and raw query never
// cross the boundary, and no upstream Set-Cookie/CORS/redirect header comes
// back. Full per-item library authorization is layered on in Phase 4.
func proxyRequest(w http.ResponseWriter, r *http.Request, targetURLStr, token string, queryAllow map[string]struct{}) {
	target, err := url.Parse(targetURLStr)
	if err != nil {
		log.Printf("proxy: bad target URL request_id=%s: %v", requestIDFrom(r.Context()), err)
		writeAPIError(w, r, http.StatusInternalServerError, "proxy_error", "The request could not be prepared.")
		return
	}

	// The target may itself carry fixed server-set parameters (e.g. static=true
	// on audio streams); preserve those and merge only allowlisted browser keys.
	fixed := target.Query()
	target.RawQuery = ""

	policy := ProxyPolicy{
		ResponseHeaders: mediaResponseHeaders,
		AllowRanges:     true,
		BuildQuery: func(browser url.Values) (url.Values, error) {
			out := url.Values{}
			for k, vs := range fixed {
				for _, v := range vs {
					out.Add(k, v)
				}
			}
			for k := range queryAllow {
				// Exactly one value per allowlisted key; ignore duplicates.
				if v := browser.Get(k); v != "" && browser[k] != nil {
					out.Set(k, v)
				}
			}
			return out, nil
		},
		UpstreamAuth: func(h http.Header) {
			if token != "" {
				h.Set("X-Emby-Token", token)
				h.Set("Authorization", fmt.Sprintf("MediaBrowser Token=%q", token))
			}
		},
	}

	// Item-level authorization is added in Phase 4 (JellyfinAuthorizer); the
	// handlers already validate the item ID shape before reaching here.
	proxyUpstream(w, r, target, policy, func(context.Context) error { return nil })
}

// hlsMintFor returns a locator-minting closure bound to this request's item,
// user, and session, so every rewritten URI is unforgeable and non-replayable.
func hlsMintFor(r *http.Request, itemID string) (func(resourcePath string) string, bool) {
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil {
		return nil, false
	}
	token, err := sessionTokenFromRequest(r)
	if err != nil {
		return nil, false
	}
	sess := hlsSessionBinding(token)
	now := time.Now()
	return func(resourcePath string) string {
		return mintHLSLocatorURL(itemID, user.ID, sess, resourcePath, now)
	}, true
}

// serveRewrittenManifest fetches an HLS manifest from Jellyfin server-side,
// rewrites every URI to an opaque Telos locator (stripping upstream tokens), and
// serves it. The browser never sees an upstream hostname, path credential, or
// admin token.
func serveRewrittenManifest(w http.ResponseWriter, r *http.Request, itemID, subpath, rawQuery string) {
	mint, ok := hlsMintFor(r, itemID)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}

	target := fmt.Sprintf("%s/Videos/%s/%s", jellyfinBaseURL, itemID, subpath)
	if filtered := filterHLSQuery(rawQuery); filtered != "" {
		target += "?" + filtered
	}
	reqCtx, cancel := upstreamRequestContext(r.Context())
	defer cancel()
	upReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "proxy_error", "The request could not be prepared.")
		return
	}
	upReq.Header.Set("X-Emby-Token", getJellyfinAdminToken())
	resp, err := upstreamHTTPClient.Do(upReq)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "upstream_unavailable", "An upstream service is unavailable.")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeAPIError(w, r, http.StatusBadGateway, "upstream_error", "The upstream returned an unexpected response.")
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHLSBytes+1))
	if err != nil || len(body) > maxHLSBytes {
		writeAPIError(w, r, http.StatusBadGateway, "upstream_error", "The manifest could not be read.")
		return
	}
	rewritten, err := rewriteHLSManifest(body, itemID, mint)
	if err != nil {
		log.Printf("hls: manifest rewrite rejected request_id=%s item=%s: %v", requestIDFrom(r.Context()), itemID, err)
		writeAPIError(w, r, http.StatusBadGateway, "upstream_error", "The manifest could not be served.")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(rewritten)
	}
}

// handleHLSResource serves an opaque HLS locator: it authenticates the locator,
// re-checks that it belongs to the current user and session, reauthorizes the
// item, and proxies (or re-rewrites, for a sub-manifest) the bound resource.
func handleHLSResource(w http.ResponseWriter, r *http.Request) {
	locator := r.PathValue("locator")
	loc, err := verifyHLSLocator(locator, time.Now())
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	// Bind to the current session and user; a locator minted for another
	// session or user is indistinguishably not found.
	token, terr := sessionTokenFromRequest(r)
	if terr != nil || hlsSessionBinding(token) != loc.Sess {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	user, _ := r.Context().Value(userContextKey).(*UserContext)
	if user == nil || user.ID != loc.User {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	if !validJellyfinID(loc.Item) {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	// Re-resolve and reauthorize the internal upstream item on every locator hit
	// so source deactivation and allowlist changes take effect immediately.
	resolution, ok := resolveJellyfinItemHTTP(w, r, loc.Item)
	if !ok || resolution.UpstreamID != loc.Item {
		return
	}
	subpath, rawQuery, err := splitResourcePath(loc.Res)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	if strings.HasSuffix(subpath, ".m3u8") {
		serveRewrittenManifest(w, r, loc.Item, subpath, rawQuery)
		return
	}
	// Binary segment/key/map: admit against long-stream capacity before opening
	// any upstream work, then proxy with the media policy. The stored query is
	// already token-free and allowlisted; the browser query is ignored.
	release, ok := acquireStreamSlot(w, r)
	if !ok {
		return
	}
	defer release()
	target := fmt.Sprintf("%s/Videos/%s/%s", jellyfinBaseURL, loc.Item, subpath)
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	proxyRequest(w, r, target, getJellyfinAdminToken(), nil)
}
