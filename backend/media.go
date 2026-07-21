package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
)

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
