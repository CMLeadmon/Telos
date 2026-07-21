package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func testSecurityConfig(t *testing.T, env, publicOrigin string, devOrigins ...string) SecurityConfig {
	t.Helper()
	cfg := SecurityConfig{
		Environment:       env,
		AllowedDevOrigins: map[string]struct{}{},
		MaxHeaderBytes:    16 << 10,
		MaxInFlightHTTP:   128,
		AuthJSONBytes:     16 << 10,
		JSONBytes:         64 << 10,
		BodyReadTimeout:   15 * time.Second,
		UploadReadIdle:    15 * time.Second,
	}
	if publicOrigin != "" {
		o, err := normalizeOrigin(publicOrigin)
		if err != nil {
			t.Fatalf("normalizeOrigin(%q): %v", publicOrigin, err)
		}
		cfg.PublicOrigin = o
	}
	for _, d := range devOrigins {
		o, err := normalizeOrigin(d)
		if err != nil {
			t.Fatalf("normalizeOrigin(%q): %v", d, err)
		}
		cfg.AllowedDevOrigins[o.String()] = struct{}{}
	}
	return cfg
}

// --- decodeJSON --------------------------------------------------------------

func TestDecodeJSONLimits(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	cases := []struct {
		name       string
		body       string
		max        int64
		wantCode   string
		wantStatus int
	}{
		{"within limit", `{"name":"ok"}`, 16 << 10, "", 200},
		{"auth body too large", `{"name":"` + strings.Repeat("a", 17<<10) + `"}`, 16 << 10, "body_too_large", http.StatusRequestEntityTooLarge},
		{"json body too large", `{"name":"` + strings.Repeat("a", 65<<10) + `"}`, 64 << 10, "body_too_large", http.StatusRequestEntityTooLarge},
		{"invalid json", `{"name"`, 16 << 10, "invalid_json", http.StatusBadRequest},
		{"trailing document", `{"name":"a"}{"name":"b"}`, 16 << 10, "invalid_json", http.StatusBadRequest},
		{"trailing garbage", `{"name":"a"} extra`, 16 << 10, "invalid_json", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/x", strings.NewReader(tc.body))
			var dst payload
			err := decodeJSON(rec, req, &dst, tc.max)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			var body apiErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not the public error shape: %v", err)
			}
			if body.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", body.Error.Code, tc.wantCode)
			}
		})
	}
}

// TestBodyReadDeadline proves a stalled body times out while an active
// renewed-idle reader keeps going.
func TestBodyReadDeadline(t *testing.T) {
	old := securityConfig
	securityConfig.BodyReadTimeout = 300 * time.Millisecond
	t.Cleanup(func() { securityConfig = old })

	result := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var dst map[string]any
		if err := decodeJSON(w, r, &dst, 1<<20); err != nil {
			result <- "timeout"
			return
		}
		result <- "ok"
	}))
	defer srv.Close()

	// Raw client that sends headers plus a partial body, then stalls.
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /x HTTP/1.1\r\nHost: t\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"a\":")
	select {
	case got := <-result:
		if got != "timeout" {
			t.Fatalf("expected stalled body to time out, handler said %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled body was never rejected")
	}
}

func TestRenewedIdleReaderContinues(t *testing.T) {
	received := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := withRenewedBodyReadDeadline(w, r, 500*time.Millisecond)
		if err != nil {
			t.Errorf("withRenewedBodyReadDeadline: %v", err)
			return
		}
		n, _ := io.Copy(io.Discard, body)
		received <- int(n)
	}))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	total := 40
	fmt.Fprintf(conn, "POST /x HTTP/1.1\r\nHost: t\r\nContent-Length: %d\r\n\r\n", total)
	// Trickle 40 bytes over ~1.2s — each chunk arrives inside the 500ms idle
	// window, but the total exceeds it. A fixed 500ms total deadline would
	// kill this upload; the renewed idle deadline must not.
	for i := 0; i < 8; i++ {
		if _, err := conn.Write([]byte("xxxxx")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	select {
	case n := <-received:
		if n != total {
			t.Fatalf("server received %d bytes, want %d", n, total)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active trickled upload never completed")
	}
}

// --- admission ---------------------------------------------------------------

func TestHTTPAdmissionSaturation(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	active := 0
	h := admissionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		active++
		mu.Unlock()
		<-release
	}), 2)

	srv := httptest.NewServer(requestIDMiddleware(h))
	defer srv.Close()
	defer close(release)

	for i := 0; i < 2; i++ {
		go http.Get(srv.URL) //nolint:errcheck
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		a := active
		mu.Unlock()
		if a == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("workers never became active")
		}
		time.Sleep(10 * time.Millisecond)
	}

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("saturated status = %d, want 503", resp.StatusCode)
	}
	var body apiErrorBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Error.Code != "server_busy" {
		t.Fatalf("expected server_busy public error, got %+v err=%v", body, err)
	}
}

// --- header limit ------------------------------------------------------------

func TestHeaderLimit(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Config.MaxHeaderBytes = 16 << 10
	srv.Start()
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: t\r\nX-Big: %s\r\n\r\n", strings.Repeat("a", 20<<10))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "431") {
		t.Fatalf("oversized header response = %q, want 431", strings.TrimSpace(line))
	}
}

// --- origin policy -----------------------------------------------------------

func TestOriginPolicy(t *testing.T) {
	prod := testSecurityConfig(t, "production", "https://community.example.org")
	prodPort := testSecurityConfig(t, "production", "https://community.example.org:8443")
	dev := testSecurityConfig(t, "development", "http://localhost:8080", "http://localhost:3000")

	cases := []struct {
		name   string
		cfg    SecurityConfig
		origin string
		want   bool
	}{
		{"exact production origin", prod, "https://community.example.org", true},
		{"default-port alias accepted", prod, "https://community.example.org:443", true},
		{"case and IDNA folding", prod, "https://COMMUNITY.example.org", true},
		{"sibling subdomain rejected", prod, "https://evil.community.example.org", false},
		{"different host rejected", prod, "https://attacker.example", false},
		{"http downgrade rejected", prod, "http://community.example.org", false},
		{"wrong explicit port rejected", prodPort, "https://community.example.org", false},
		{"matching explicit port", prodPort, "https://community.example.org:8443", true},
		{"empty origin rejected", prod, "", false},
		{"null origin rejected", prod, "null", false},
		{"dev allowlisted origin", dev, "http://localhost:3000", true},
		{"dev non-allowlisted origin", dev, "http://localhost:5173", false},
		{"dev list ignored in production", prod, "http://localhost:3000", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := originAllowed(tc.cfg, tc.origin); got != tc.want {
				t.Fatalf("originAllowed(%q) = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

func TestRequireTrustedOriginMiddleware(t *testing.T) {
	cfg := testSecurityConfig(t, "production", "https://community.example.org")
	ok := false
	h := requestIDMiddleware(requireTrustedOrigin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok = true
	}), cfg))

	req := httptest.NewRequest("POST", "/api/v1/x", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if ok || rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST passed (code %d)", rec.Code)
	}

	req = httptest.NewRequest("POST", "/api/v1/x", nil)
	req.Header.Set("Origin", "https://community.example.org")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !ok || rec.Code != http.StatusOK {
		t.Fatalf("same-origin POST blocked (code %d)", rec.Code)
	}

	// GET passes without an Origin header.
	ok = false
	req = httptest.NewRequest("GET", "/api/v1/x", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !ok {
		t.Fatal("GET without Origin was blocked")
	}
}

// --- CSP / security headers ----------------------------------------------------

func TestCSPRendersExactWSSOrigin(t *testing.T) {
	cfg := testSecurityConfig(t, "production", "https://community.example.org:8443")
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), cfg)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "connect-src 'self' wss://community.example.org:8443;") {
		t.Fatalf("CSP lacks the exact wss origin: %q", csp)
	}
	for _, banned := range []string{"wss:;", "wss://*", "connect-src 'self' wss: "} {
		if strings.Contains(csp, banned) {
			t.Fatalf("CSP contains forbidden pattern %q: %q", banned, csp)
		}
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Fatalf("HSTS = %q", got)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		rec.Header().Get("Referrer-Policy") != "strict-origin-when-cross-origin" ||
		rec.Header().Get("Permissions-Policy") == "" {
		t.Fatalf("missing security headers: %+v", rec.Header())
	}
}

func TestNoReflectiveCORSInProduction(t *testing.T) {
	cfg := testSecurityConfig(t, "production", "https://community.example.org")
	h := devCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), cfg)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("production reflected a CORS origin")
	}
}

// --- client IP trust -----------------------------------------------------------

func TestClientIP(t *testing.T) {
	traefik := []netip.Prefix{netip.MustParsePrefix("10.89.0.0/16")}
	cases := []struct {
		name    string
		remote  string
		xff     string
		trusted []netip.Prefix
		want    string
		wantErr bool
	}{
		{"direct peer, no proxy", "203.0.113.9:41000", "", traefik, "203.0.113.9", false},
		{"spoofed XFF from untrusted peer ignored", "203.0.113.9:41000", "198.51.100.7", traefik, "203.0.113.9", false},
		{"trusted proxy forwards client", "10.89.0.2:5555", "198.51.100.7", traefik, "198.51.100.7", false},
		{"rightmost untrusted entry wins", "10.89.0.2:5555", "1.2.3.4, 198.51.100.7", traefik, "198.51.100.7", false},
		{"trusted chain collapses to peer", "10.89.0.2:5555", "10.89.0.3", traefik, "10.89.0.2", false},
		{"mapped IPv6 peer unmapped", "[::ffff:203.0.113.9]:41000", "", traefik, "203.0.113.9", false},
		{"malformed forwarded entry fails", "10.89.0.2:5555", "not-an-ip", traefik, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			got, err := clientIP(r, tc.trusted)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tc.want {
				t.Fatalf("clientIP = %s, want %s", got, tc.want)
			}
		})
	}
}

// --- SSRF public URL validation -------------------------------------------------

func TestPublicURLIPs(t *testing.T) {
	mustURL := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	fakeDNS := func(answers ...string) func(context.Context, string) ([]net.IPAddr, error) {
		return func(context.Context, string) ([]net.IPAddr, error) {
			out := make([]net.IPAddr, 0, len(answers))
			for _, a := range answers {
				out = append(out, net.IPAddr{IP: net.ParseIP(a)})
			}
			return out, nil
		}
	}

	cases := []struct {
		name    string
		url     string
		answers []string
		wantErr bool
	}{
		{"public answer accepted", "https://api.example.org/x", []string{"93.184.216.34"}, false},
		{"loopback rejected", "https://api.example.org/x", []string{"127.0.0.1"}, true},
		{"private rejected", "https://api.example.org/x", []string{"10.1.2.3"}, true},
		{"link-local rejected", "https://api.example.org/x", []string{"169.254.10.10"}, true},
		{"CGNAT rejected", "https://api.example.org/x", []string{"100.64.1.1"}, true},
		{"multicast rejected", "https://api.example.org/x", []string{"224.0.0.5"}, true},
		{"IPv6 loopback rejected", "https://api.example.org/x", []string{"::1"}, true},
		{"IPv6 unique-local rejected", "https://api.example.org/x", []string{"fd00::1"}, true},
		{"IPv6 link-local rejected", "https://api.example.org/x", []string{"fe80::1"}, true},
		{"mapped IPv6 loopback rejected", "https://api.example.org/x", []string{"::ffff:127.0.0.1"}, true},
		{"mixed answers rejected entirely", "https://api.example.org/x", []string{"93.184.216.34", "10.0.0.1"}, true},
		{"literal loopback rejected", "https://127.0.0.1/x", nil, true},
		{"literal private rejected", "http://192.168.1.10/x", nil, true},
		{"literal public accepted", "https://93.184.216.34/x", nil, false},
		{"file scheme rejected", "file:///etc/passwd", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := lookupIPAddr
			if tc.answers != nil {
				lookupIPAddr = fakeDNS(tc.answers...)
			}
			t.Cleanup(func() { lookupIPAddr = old })
			_, err := publicURLIPs(context.Background(), mustURL(tc.url))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestPinnedDialRejectsNonPublicAndHostnames(t *testing.T) {
	if _, err := dialValidatedPublic(context.Background(), "tcp", "127.0.0.1:80", ""); err == nil {
		t.Fatal("dialing loopback succeeded")
	}
	if _, err := dialValidatedPublic(context.Background(), "tcp", "example.org:443", ""); err == nil {
		t.Fatal("dialing a hostname (re-resolution) succeeded")
	}
	if _, err := dialValidatedPublic(context.Background(), "tcp", "10.0.0.1:80", ""); err == nil {
		t.Fatal("dialing private space succeeded")
	}
}

// --- public error shape ----------------------------------------------------------

func TestAPIErrorShape(t *testing.T) {
	h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, r, http.StatusBadGateway, "upstream_unavailable", "An upstream service is unavailable.")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	var body apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if body.Error.Code != "upstream_unavailable" || body.Error.RequestID == "" || body.Error.RequestID == "unknown" {
		t.Fatalf("bad public error: %+v", body)
	}
	raw := rec.Body.String()
	for _, leak := range []string{"SQLSTATE", "connection refused", "dial tcp", "no such file"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("public error leaks infrastructure text: %s", raw)
		}
	}
}

func TestNormalizeOrigin(t *testing.T) {
	got, err := normalizeOrigin("HTTPS://Community.EXAMPLE.org")
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "https://community.example.org:443" {
		t.Fatalf("normalized = %q", got.String())
	}
	for _, bad := range []string{"ftp://x.example", "https://user:pw@x.example", "https://x.example/path", "not a url", ""} {
		if _, err := normalizeOrigin(bad); err == nil {
			t.Fatalf("normalizeOrigin(%q) accepted", bad)
		}
	}
}
