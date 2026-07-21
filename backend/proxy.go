package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// sessionTokenFromRequest returns the session token from exactly the secure
// telos_session cookie. Query strings, fragments, headers, duplicate cookies,
// and empty/malformed values are rejected so a session token can never reach
// the gateway through a URL or an upstream-relayable channel.
func sessionTokenFromRequest(r *http.Request) (string, error) {
	cookies := r.CookiesNamed("telos_session")
	if len(cookies) == 0 {
		return "", errors.New("missing session cookie")
	}
	if len(cookies) > 1 {
		return "", errors.New("duplicate session cookie")
	}
	token := cookies[0].Value
	if token == "" {
		return "", errors.New("empty session cookie")
	}
	// A session token is opaque hex; reject anything with structure that
	// suggests it was sourced from a URL or header injection.
	if strings.ContainsAny(token, " \t\r\n/?#&=") {
		return "", errors.New("malformed session token")
	}
	return token, nil
}

// ProxyPolicy is the allowlist-based contract for relaying a request to an
// isolated upstream (Jellyfin/Grimmory). Nothing crosses the boundary that is
// not explicitly named here. Later media work extends this struct; the fields
// added in Phase 4 default to zero and are ignored by this base implementation.
type ProxyPolicy struct {
	// RequestHeaders is the set of browser request headers copied upstream.
	RequestHeaders map[string]struct{}
	// ResponseHeaders is the set of upstream response headers relayed back.
	ResponseHeaders map[string]struct{}
	// BuildQuery constructs the complete upstream query from validated fields;
	// the raw browser query is never forwarded. Nil means no query.
	BuildQuery func(browser url.Values) (url.Values, error)
	// AllowRanges permits Range/If-Range/If-None-Match/If-Modified-Since to be
	// copied upstream and 206/conditional responses to pass.
	AllowRanges bool
	// UpstreamAuth injects server-held upstream credentials (never from the
	// browser). It runs after the allowlisted request headers are set.
	UpstreamAuth func(h http.Header)
}

// hop-by-hop headers are never relayed in either direction.
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Proxy-Connection":    {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

var rangeRequestHeaders = map[string]struct{}{
	"Range":             {},
	"If-Range":          {},
	"If-None-Match":     {},
	"If-Modified-Since": {},
}

// proxyUpstream relays r to target under policy after authorize succeeds. It
// builds a brand-new upstream request (approved method/path/validated query,
// explicit request-header allowlist, server-injected credentials), refuses to
// follow upstream redirects, and copies back only allowlisted response headers
// — never Set-Cookie, CORS, auth challenges, Location, or server identity.
func proxyUpstream(w http.ResponseWriter, r *http.Request, target *url.URL, policy ProxyPolicy, authorize func(context.Context) error) {
	if authorize != nil {
		if err := authorize(r.Context()); err != nil {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "You are not authorized for this resource.")
			return
		}
	}

	upstream := *target
	if policy.BuildQuery != nil {
		q, err := policy.BuildQuery(r.URL.Query())
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid_request", "The request could not be built.")
			return
		}
		upstream.RawQuery = q.Encode()
	} else {
		upstream.RawQuery = ""
	}

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstream.String(), http.NoBody)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "proxy_error", "The request could not be prepared.")
		return
	}

	// Only allowlisted browser request headers cross the boundary.
	for name := range policy.RequestHeaders {
		if _, hop := hopByHopHeaders[http.CanonicalHeaderKey(name)]; hop {
			continue
		}
		if v := r.Header.Values(name); len(v) > 0 {
			outReq.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), v...)
		}
	}
	if policy.AllowRanges {
		for name := range rangeRequestHeaders {
			if v := r.Header.Values(name); len(v) > 0 {
				outReq.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), v...)
			}
		}
	}
	if policy.UpstreamAuth != nil {
		policy.UpstreamAuth(outReq.Header)
	}

	// RoundTrip never follows redirects, so an upstream 3xx Location is never
	// acted on or relayed to the browser.
	resp, err := upstreamTransport.RoundTrip(outReq)
	if err != nil {
		log.Printf("proxy: upstream request failed request_id=%s host=%s: %v", requestIDFrom(r.Context()), upstream.Host, err)
		writeAPIError(w, r, http.StatusBadGateway, "upstream_unavailable", "An upstream service is unavailable.")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		writeAPIError(w, r, http.StatusBadGateway, "upstream_redirect", "The upstream returned an unexpected redirect.")
		return
	}
	if !policy.AllowRanges && resp.StatusCode == http.StatusPartialContent {
		writeAPIError(w, r, http.StatusBadGateway, "upstream_error", "The upstream returned an unexpected response.")
		return
	}

	out := w.Header()
	for name := range policy.ResponseHeaders {
		canonical := http.CanonicalHeaderKey(name)
		if _, hop := hopByHopHeaders[canonical]; hop {
			continue
		}
		if v := resp.Header.Values(canonical); len(v) > 0 {
			out[canonical] = append([]string(nil), v...)
		}
	}

	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Printf("proxy: copy failed request_id=%s: %v", requestIDFrom(r.Context()), err)
		}
	}
}

// mediaResponseHeaders is the allowlist for binary media/cover responses.
var mediaResponseHeaders = map[string]struct{}{
	"Content-Type":        {},
	"Content-Length":      {},
	"Content-Range":       {},
	"Accept-Ranges":       {},
	"ETag":                {},
	"Last-Modified":       {},
	"Cache-Control":       {},
	"Content-Disposition": {},
}
