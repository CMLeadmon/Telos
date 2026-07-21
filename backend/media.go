package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
