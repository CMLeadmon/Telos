package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// --- start gate --------------------------------------------------------------

func TestStartGateAdmitsAtMostNConcurrentStartsAndReleasesOnce(t *testing.T) {
	gate := startGate(2, 50*time.Millisecond)
	r1, err1 := gate(context.Background())
	r2, err2 := gate(context.Background())
	if err1 != nil || err2 != nil {
		t.Fatalf("the first two starts must be admitted: %v %v", err1, err2)
	}
	if _, err := gate(context.Background()); !errors.Is(err, errUpstreamBusy) {
		t.Fatalf("a third start must wait out and report busy, got %v", err)
	}
	r1()
	r1() // a second release of the same slot must not free another one
	if _, err := gate(context.Background()); err != nil {
		t.Fatalf("a freed slot must admit: %v", err)
	}
	if _, err := gate(context.Background()); !errors.Is(err, errUpstreamBusy) {
		t.Fatalf("the double release freed a slot it did not own: %v", err)
	}
	r2()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	full := startGate(1, time.Minute)
	if _, err := full(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := full(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled caller must stop waiting: %v", err)
	}
}

func TestGatedTransportHoldsTheSlotOnlyUntilHeadersAndOnlyForTheGatedHost(t *testing.T) {
	headersSent := make(chan struct{})
	finish := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(headersSent)
		<-finish // the body keeps streaming long after the start
	}))
	defer upstream.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer other.Close()

	gate := startGate(1, 100*time.Millisecond)
	transport := gatedTransport{base: http.DefaultTransport, gate: gate,
		applies: func(req *http.Request) bool { return req.URL.Host == strings.TrimPrefix(upstream.URL, "http://") }}
	client := &http.Client{Transport: transport}

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	<-headersSent
	// The first response is still streaming its body, yet its slot is free: a second start is admitted.
	release, gateErr := gate(context.Background())
	if gateErr != nil {
		t.Fatalf("a streaming body must not hold the start slot: %v", gateErr)
	}
	// With the only slot taken, a gated request waits out and fails busy; an ungated host is untouched.
	if _, err := client.Get(upstream.URL); !errors.Is(err, errUpstreamBusy) {
		t.Fatalf("a full gate must report busy, got %v", err)
	}
	other2, err := client.Get(other.URL)
	if err != nil {
		t.Fatalf("a request to another host must not be gated: %v", err)
	}
	other2.Body.Close()
	release()
	close(finish)
	resp.Body.Close()
}

func TestAnyRequestToJellyfinIsGatedWhicheverClientSendsIt(t *testing.T) {
	var peak, inFlight int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		w.Write([]byte("{}"))
	}))
	defer server.Close()
	oldBase, oldGate := getJellyfinBaseURL, jellyfinStartGate
	getJellyfinBaseURL = func() string { return server.URL }
	jellyfinStartGate = startGate(2, 5*time.Second)
	defer func() { getJellyfinBaseURL, jellyfinStartGate = oldBase, oldGate }()

	var done sync.WaitGroup
	for i := 0; i < 10; i++ {
		done.Add(2)
		go func() { // an API call
			defer done.Done()
			resp, err := jellyfinGET(context.Background(), "/Users", "")
			if err != nil {
				t.Errorf("jellyfinGET: %v", err)
				return
			}
			resp.Body.Close()
		}()
		go func() { // a proxied media request
			defer done.Done()
			target, _ := url.Parse(server.URL + "/Audio/x/stream")
			rec := httptest.NewRecorder()
			proxyUpstream(rec, httptest.NewRequest("GET", "/x", nil), target, ProxyPolicy{ResponseHeaders: mediaResponseHeaders, AllowRanges: true}, nil)
		}()
	}
	done.Wait()
	if p := atomic.LoadInt32(&peak); p > 2 {
		t.Fatalf("%d Jellyfin requests were in flight at once; the gate allows 2", p)
	}
}
