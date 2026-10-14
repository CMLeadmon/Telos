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

func TestProxyUpstreamHoldsTheStartGateOnlyUntilHeadersArrive(t *testing.T) {
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
	defer close(finish)

	var inFlight, peak int32
	inner := startGate(1, 200*time.Millisecond)
	gate := func(ctx context.Context) (func(), error) {
		release, err := inner(ctx)
		if err != nil {
			return nil, err
		}
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		return func() { atomic.AddInt32(&inFlight, -1); release() }, nil
	}
	target, _ := url.Parse(upstream.URL + "/Audio/x/stream")
	policy := ProxyPolicy{ResponseHeaders: mediaResponseHeaders, AllowRanges: true, StartGate: gate}
	done := make(chan struct{})
	go func() {
		defer close(done)
		rec := httptest.NewRecorder()
		proxyUpstream(rec, httptest.NewRequest("GET", "/x", nil), target, policy, nil)
	}()
	<-headersSent
	// The first request is still streaming its body. A second start must be admitted: the slot was
	// released when the headers arrived.
	release, err := gate(context.Background())
	if err != nil {
		t.Fatalf("a streaming body must not hold the start slot: %v", err)
	}
	release()
	if atomic.LoadInt32(&peak) > 1 {
		t.Fatalf("more than one start was in flight at once (peak %d)", peak)
	}
}

func TestProxyUpstreamAnswers503WhenTheGateTimesOut(t *testing.T) {
	reached := int32(0)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&reached, 1) }))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL + "/x")
	busy := func(context.Context) (func(), error) { return nil, errUpstreamBusy }
	rec := httptest.NewRecorder()
	proxyUpstream(rec, httptest.NewRequest("GET", "/x", nil), target, ProxyPolicy{StartGate: busy}, nil)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("a busy upstream must answer 503 with Retry-After, got %d %v", rec.Code, rec.Header())
	}
	if atomic.LoadInt32(&reached) != 0 {
		t.Fatal("the upstream was contacted although the gate refused")
	}
}

func TestJellyfinGETHoldsTheSharedGateOnlyUntilHeaders(t *testing.T) {
	previous := jellyfinStartGate
	defer func() { jellyfinStartGate = previous }()
	var inFlight, peak int32
	inner := startGate(2, 2*time.Second)
	jellyfinStartGate = func(ctx context.Context) (func(), error) {
		release, err := inner(ctx)
		if err != nil {
			return nil, err
		}
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		return func() { atomic.AddInt32(&inFlight, -1); release() }, nil
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.Write([]byte("{}"))
	}))
	defer upstream.Close()
	oldBase := getJellyfinBaseURL
	getJellyfinBaseURL = func() string { return upstream.URL }
	defer func() { getJellyfinBaseURL = oldBase }()
	var done sync.WaitGroup
	for i := 0; i < 8; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			resp, err := jellyfinGET(context.Background(), "/x", "")
			if err != nil {
				t.Errorf("jellyfinGET: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}
	done.Wait()
	if p := atomic.LoadInt32(&peak); p > 2 {
		t.Fatalf("%d Jellyfin API starts overlapped; the gate allows 2", p)
	}
	jellyfinStartGate = func(context.Context) (func(), error) { return nil, errUpstreamBusy }
	if _, err := jellyfinGET(context.Background(), "/x", ""); !errors.Is(err, errUpstreamBusy) {
		t.Fatalf("a refused start must surface as busy, got %v", err)
	}
}
