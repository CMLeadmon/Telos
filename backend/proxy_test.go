package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// --- sessionTokenFromRequest -------------------------------------------------

func TestSessionTokenFromRequest(t *testing.T) {
	newReq := func(build func(*http.Request)) *http.Request {
		r := httptest.NewRequest("GET", "/api/v1/media/items/x/cover", nil)
		build(r)
		return r
	}

	t.Run("valid cookie", func(t *testing.T) {
		r := newReq(func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "telos_session", Value: "abcdef0123456789"})
		})
		got, err := sessionTokenFromRequest(r)
		if err != nil || got != "abcdef0123456789" {
			t.Fatalf("got %q err %v", got, err)
		}
	})

	t.Run("no cookie", func(t *testing.T) {
		if _, err := sessionTokenFromRequest(newReq(func(*http.Request) {})); err == nil {
			t.Fatal("expected error with no cookie")
		}
	})

	t.Run("query token rejected", func(t *testing.T) {
		r := newReq(func(r *http.Request) {
			r.URL.RawQuery = "token=abcdef0123456789"
		})
		if _, err := sessionTokenFromRequest(r); err == nil {
			t.Fatal("query token was accepted")
		}
	})

	t.Run("duplicate cookie rejected", func(t *testing.T) {
		r := newReq(func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "telos_session", Value: "aaaa"})
			r.AddCookie(&http.Cookie{Name: "telos_session", Value: "bbbb"})
		})
		if _, err := sessionTokenFromRequest(r); err == nil {
			t.Fatal("duplicate cookie was accepted")
		}
	})

	t.Run("empty value rejected", func(t *testing.T) {
		r := newReq(func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "telos_session", Value: ""})
		})
		if _, err := sessionTokenFromRequest(r); err == nil {
			t.Fatal("empty cookie was accepted")
		}
	})

	t.Run("malformed value rejected", func(t *testing.T) {
		r := newReq(func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "telos_session", Value: "abc/def?x=1"})
		})
		if _, err := sessionTokenFromRequest(r); err == nil {
			t.Fatal("malformed cookie was accepted")
		}
	})
}

// --- proxyUpstream boundary --------------------------------------------------

// capturingUpstream records exactly what the gateway sent and emits a response
// with headers that must never be relayed back.
func capturingUpstream(t *testing.T, seen *http.Header, seenQuery *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Clone()
		*seenQuery = r.URL.RawQuery
		w.Header().Set("Set-Cookie", "upstream_sess=leak; Path=/")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("WWW-Authenticate", "Basic realm=jellyfin")
		w.Header().Set("Server", "Jellyfin/10.9")
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Accept-Ranges", "bytes")
		io.WriteString(w, "BINARY")
	}))
}

func TestProxyRequestDoesNotLeakCredentials(t *testing.T) {
	var seen http.Header
	var seenQuery string
	upstream := capturingUpstream(t, &seen, &seenQuery)
	defer upstream.Close()

	req := httptest.NewRequest("GET", "/api/v1/stream/video/x/main.m3u8?PlaySessionId=ps1&api_key=EVIL&token=EVIL2&foo=bar", nil)
	req.AddCookie(&http.Cookie{Name: "telos_session", Value: "supersecretsession"})
	req.AddCookie(&http.Cookie{Name: "other", Value: "x"})
	req.Header.Set("Authorization", "Bearer browsertoken")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()

	proxyRequest(rec, req, upstream.URL+"/Videos/x/main.m3u8", "server-admin-token", jellyfinStreamQueryKeys)

	// Nothing browser-controlled crossed the boundary.
	if got := seen.Get("Cookie"); got != "" {
		t.Fatalf("browser Cookie relayed upstream: %q", got)
	}
	if got := seen.Get("Authorization"); got != `MediaBrowser Token="server-admin-token"` {
		t.Fatalf("Authorization = %q, want server-injected admin token only", got)
	}
	if got := seen.Get("X-Emby-Token"); got != "server-admin-token" {
		t.Fatalf("X-Emby-Token = %q", got)
	}
	if got := seen.Get("X-Forwarded-For"); got != "" {
		t.Fatalf("X-Forwarded-For relayed upstream: %q", got)
	}

	// Only allowlisted query keys pass; api_key/token/foo are dropped.
	uq, _ := url.ParseQuery(seenQuery)
	if uq.Get("PlaySessionId") != "ps1" {
		t.Fatalf("allowlisted PlaySessionId missing: %q", seenQuery)
	}
	for _, dropped := range []string{"api_key", "token", "foo"} {
		if uq.Has(dropped) {
			t.Fatalf("browser query key %q leaked upstream: %q", dropped, seenQuery)
		}
	}

	// No upstream Set-Cookie/CORS/auth/server headers come back.
	res := rec.Result()
	for _, banned := range []string{"Set-Cookie", "Access-Control-Allow-Origin", "Www-Authenticate", "Server"} {
		if v := res.Header.Values(banned); len(v) > 0 {
			t.Fatalf("upstream header %q relayed to browser: %v", banned, v)
		}
	}
	if res.Header.Get("Content-Type") != "video/mp4" || res.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("allowlisted media headers missing: %+v", res.Header)
	}
	if rec.Body.String() != "BINARY" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestProxyRequestRejectsUpstreamRedirect(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://evil.example/steal")
		w.WriteHeader(http.StatusFound)
	}))
	defer upstream.Close()

	req := httptest.NewRequest("GET", "/api/v1/media/items/x/cover", nil)
	rec := httptest.NewRecorder()
	proxyRequest(rec, req, upstream.URL+"/Items/x/Images/Primary", "tok", nil)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for upstream redirect", rec.Code)
	}
	if loc := rec.Result().Header.Get("Location"); loc != "" {
		t.Fatalf("upstream Location relayed to browser: %q", loc)
	}
}

func TestProxyUpstreamAuthorizationDenied(t *testing.T) {
	var reached int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reached, 1)
	}))
	defer upstream.Close()

	target, _ := url.Parse(upstream.URL + "/x")
	req := httptest.NewRequest("GET", "/api/v1/x", nil)
	rec := httptest.NewRecorder()
	proxyUpstream(rec, req, target, ProxyPolicy{}, func(context.Context) error {
		return errUnauthorizedTest
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if atomic.LoadInt32(&reached) != 0 {
		t.Fatal("upstream was contacted despite authorization failure")
	}
}

var errUnauthorizedTest = &proxyTestError{"denied"}

type proxyTestError struct{ s string }

func (e *proxyTestError) Error() string { return e.s }

func TestProxyRequestCancellationClosesBody(t *testing.T) {
	// A cancelled request context must abort the upstream round-trip cleanly.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		io.WriteString(w, strings.Repeat("x", 1024))
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", "/api/v1/media/items/x/cover", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	proxyRequest(rec, req, upstream.URL+"/Items/x/Images/Primary", "tok", nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("cancelled proxy status = %d, want 502", rec.Code)
	}
}
